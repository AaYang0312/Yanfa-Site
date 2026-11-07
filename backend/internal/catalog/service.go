// Package catalog exposes only the active release's independent public
// projection. It has no workspace, authentication, SQL or model dependency.
// Service results are caller-owned mutable DTOs, isolated from the cached public
// projection and private release. HTTP serializes only allowlisted contract DTOs.
package catalog

import (
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/matching"
	"yuanfa-official/backend/internal/release"
)

type product struct {
	public contract.PublicProduct
	sort   int64
	text   string
}
type projection struct {
	handle   *release.Handle
	products []product
	byID     map[string]int
}
type Service struct {
	releases *release.Manager
	hosts    []string
	mu       sync.Mutex
	current  *projection
}

// New binds the sole active authority and a copy of the exact deployment host
// allowlist. Preview handles are rejected even for an authenticated caller.
func New(releases *release.Manager, allowedHosts []string) *Service {
	return &Service{releases: releases, hosts: slices.Clone(allowedHosts)}
}
func notFound() error {
	return contract.NewBusinessError(404, "PRODUCT_NOT_FOUND", "商品或购买渠道当前不可用。")
}
func invalid(field string) error {
	return contract.NewBusinessError(422, "VALIDATION_FAILED", "字段或引用不合法。", field)
}
func (s *Service) view(h *release.Handle) (*projection, error) {
	if h == nil {
		return nil, notFound()
	}
	active, err := s.releases.Capture(h.ID())
	if err != nil {
		return nil, err
	}
	if active != h {
		return nil, notFound()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current != nil && s.current.handle == h {
		return s.current, nil
	}
	v := &projection{handle: h, products: []product{}, byID: map[string]int{}}
	for _, p := range h.Catalog().Products {
		if p.Visibility != "listed" {
			continue
		}
		text := strings.Join([]string{p.Model, p.Name, p.Summary, p.Series, strings.Join(p.Applications, " "), strings.Join(p.Features, " "), strings.Join(p.Notes, " ")}, " ")
		v.products = append(v.products, product{public: s.project(h, p), sort: p.SortOrder, text: normalizeSearch(text)})
	}
	slices.SortFunc(v.products, func(a, b product) int {
		if d := cmp.Compare(a.sort, b.sort); d != 0 {
			return d
		}
		return strings.Compare(a.public.ProductID, b.public.ProductID)
	})
	for i, p := range v.products {
		v.byID[p.public.ProductID] = i
	}
	s.current = v
	return v, nil
}

// Get returns only a currently listed product, without consulting cookies or
// expanding permissions. All facts are projected from the captured release.
// The returned product and all nested mutable data belong to the caller.
func (s *Service) Get(ctx context.Context, h *release.Handle, id string) (contract.PublicProduct, error) {
	p, err := s.get(ctx, h, id)
	if err != nil {
		return contract.PublicProduct{}, err
	}
	return cloneProduct(p), nil
}

// get borrows the cached product for internal use; only public return boundaries
// transfer ownership, and channel-only reads need not copy an entire product.
func (s *Service) get(ctx context.Context, h *release.Handle, id string) (contract.PublicProduct, error) {
	if err := ctx.Err(); err != nil {
		return contract.PublicProduct{}, err
	}
	v, err := s.view(h)
	if err != nil {
		return contract.PublicProduct{}, err
	}
	i, ok := v.byID[id]
	if !ok {
		return contract.PublicProduct{}, notFound()
	}
	return v.products[i].public, nil
}

// Export is a consistent public view, never the private catalog snapshot. Its
// dictionary slices are also independent of the private release's nested data.
func (s *Service) Export(ctx context.Context, h *release.Handle) (contract.PublicExport, error) {
	out := contract.PublicExport{SchemaVersion: contract.SchemaVersion, Products: []contract.PublicProduct{}}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	v, err := s.view(h)
	if err != nil {
		return out, err
	}
	snapshot := h.Catalog()
	out.ReleaseID = h.ID()
	out.GeneratedAt = time.Now().UTC().Format(time.RFC3339Nano)
	out.Categories = make([]contract.Category, len(snapshot.Categories))
	for i, c := range snapshot.Categories {
		c.SceneIDs = slices.Clone(c.SceneIDs)
		c.Nail = copyPointer(c.Nail)
		out.Categories[i] = c
	}
	out.Scenes = slices.Clone(snapshot.Scenes)
	out.NailTypes = slices.Clone(snapshot.NailTypes)
	for _, p := range v.products {
		out.Products = append(out.Products, cloneProduct(p.public))
	}
	return out, nil
}

type cursor struct {
	Release string `json:"r"`
	Query   string `json:"q"`
	Sort    int64  `json:"s"`
	ID      string `json:"id"`
}

func encodeCursor(c cursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}
func decodeCursor(raw string) (cursor, error) {
	var c cursor
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return c, invalid("cursor")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || c.Release == "" || len(c.Query) != 64 || c.Sort < 0 || c.ID == "" {
		return c, invalid("cursor")
	}
	return c, nil
}

// Search uses Evaluate for all suitability evidence. include_unknown controls
// uncertainty in explicitly requested hard filters only: plain browse/keywords
// keep unreviewed products visible with needs_confirmation. It never admits a
// conflict. Cursor binds content release, normalized query (including limit),
// and the last stable sort_order/product_id pair.
func (s *Service) Search(ctx context.Context, h *release.Handle, input contract.CatalogQuery) (contract.ProductList, error) {
	out := contract.ProductList{Items: []contract.PublicProduct{}}
	b, err := json.Marshal(input)
	if err != nil {
		return out, invalid("query")
	}
	q, err := contract.Decode[contract.CatalogQuery]("public-catalog.schema.json#/$defs/CatalogQuery", b)
	if err != nil {
		return out, err
	}
	v, err := s.view(h)
	if err != nil {
		return out, err
	}
	out.ReleaseID = h.ID()
	if q.ReleaseID != nil && *q.ReleaseID != h.ID() {
		return out, release.Changed()
	}
	text := ""
	if q.Q != nil {
		text = normalizeSearch(*q.Q)
	}
	limit := int64(20)
	if q.Limit != nil {
		limit = *q.Limit
	}
	include := q.IncludeUnknown != nil && *q.IncludeUnknown
	normalized := q
	normalized.Cursor = nil
	normalized.ReleaseID = nil
	normalized.Q = &text
	normalized.Limit = &limit
	normalized.IncludeUnknown = &include
	b, err = json.Marshal(normalized)
	if err != nil {
		return out, err
	}
	queryHash := release.Hash(b)
	var after *cursor
	if q.Cursor != nil {
		c, e := decodeCursor(*q.Cursor)
		if e != nil {
			return out, e
		}
		if c.Release != h.ID() || c.Query != queryHash {
			return out, release.Changed()
		}
		i, exists := v.byID[c.ID]
		if !exists || v.products[i].sort != c.Sort {
			return out, invalid("cursor")
		}
		after = &c
	}
	req := contract.UnknownRequirements()
	if q.Scene != nil {
		req.SceneIDs = []contract.SceneID{*q.Scene}
	}
	req.NailType = q.NailType
	req.Material = q.Material
	req.FastenerLengthMM = q.FastenerLengthMM
	for _, item := range v.products {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		p := item.public
		if after != nil && (item.sort < after.Sort || item.sort == after.Sort && p.ProductID <= after.ID) {
			continue
		}
		if q.Category != nil && p.CategoryID != *q.Category || text != "" && !strings.Contains(item.text, text) {
			continue
		}
		decision := matching.Evaluate(req, p.GuideProfile)
		if decision.MatchLevel == "no_match" {
			continue
		}
		if q.PowerSource != nil && p.GuideProfile.PowerSource != nil && *q.PowerSource != *p.GuideProfile.PowerSource {
			continue
		}
		if !include && explicitUnknown(q, decision) {
			continue
		}
		if int64(len(out.Items)) == limit {
			last := out.Items[len(out.Items)-1]
			lastItem := v.products[v.byID[last.ProductID]]
			next := encodeCursor(cursor{Release: h.ID(), Query: queryHash, Sort: lastItem.sort, ID: last.ProductID})
			out.NextCursor = &next
			break
		}
		p = cloneProductContent(p)
		p.MatchLevel = decision.MatchLevel
		p.MatchedFacts = decision.MatchedFacts
		p.MissingFields = decision.MissingFields
		out.Items = append(out.Items, p)
	}
	return out, nil
}
func explicitUnknown(q contract.CatalogQuery, d contract.MatchDecision) bool {
	for _, field := range d.MissingFields {
		switch field {
		case "guide_profile.scene_ids":
			if q.Scene != nil {
				return true
			}
		case "guide_profile.materials", "guide_profile.excluded_materials":
			if q.Material != nil {
				return true
			}
		case "guide_profile.nail_type":
			if q.NailType != nil {
				return true
			}
		case "guide_profile.fastener_length_mm":
			if q.FastenerLengthMM != nil {
				return true
			}
		case "guide_profile.power_source":
			if q.PowerSource != nil {
				return true
			}
		}
	}
	return false
}
