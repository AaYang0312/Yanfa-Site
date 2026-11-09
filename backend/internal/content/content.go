// Package content owns the only writable product workspace. All callers use the
// shared contracts; authorization finishes before the single SQLite connection
// enters a transaction. Final writes and media reference checks share Gate.
//
// New products use unlisted visibility. Replace, Review and Delete require the
// version returned by Get; a conflict is never automatically retried. Reviews
// bind one non-null fact and selected source hashes. Display text/media hashes
// are recomputed on save; archive document IDs come from the verified seed.
// Dictionaries and archive hashes live only in workspace.dictionaries_json.
// Snapshot requires a caller-owned transaction and Gate; it performs no nested
// authorization or database connection acquisition. Its media paths are relative
// to DATA_DIR/media and are private build inputs, never public URLs.
package content

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"yuanfa-official/backend/internal/auth"
	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/coordination"
	"yuanfa-official/backend/internal/media"
)

type Service struct {
	db    *sql.DB
	auth  *auth.Service
	gate  *coordination.Gate
	media *media.Service
	dir   string
	hosts []string
}

func New(db *sql.DB, identity *auth.Service, gate *coordination.Gate, images *media.Service, dataDir string, allowedHosts []string) *Service {
	return &Service{db: db, auth: identity, gate: gate, media: images, dir: dataDir, hosts: slices.Clone(allowedHosts)}
}
func invalid(fields ...string) error {
	return contract.NewBusinessError(422, "VALIDATION_FAILED", "字段或引用不合法。", fields...)
}
func missing() error {
	return contract.NewBusinessError(404, "PRODUCT_NOT_FOUND", "产品不存在。")
}
func conflict(version int64) error {
	e := contract.NewBusinessError(412, "VERSION_CONFLICT", "产品已被其他操作修改，请重新读取后合并。")
	e.Detail.CurrentVersion = &version
	return e
}
func expectedVersion(expected, current int64) error {
	if expected < 1 {
		return contract.NewBusinessError(428, "PRECONDITION_REQUIRED", "请提供当前产品版本。")
	}
	if expected != current {
		return conflict(current)
	}
	return nil
}
func randomID(prefix string) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(b[:]), nil
}

// Match browser JSON.stringify for text digests, including U+2028/U+2029.
func canonical(value any) ([]byte, error) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(value); err != nil {
		return nil, err
	}
	data := bytes.TrimSuffix(b.Bytes(), []byte{'\n'})
	if !bytes.Contains(data, []byte(`\u202`)) {
		return data, nil
	}
	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); {
		if data[i] == '\\' && i+1 < len(data) {
			if i+6 <= len(data) && (string(data[i:i+6]) == `\u2028` || string(data[i:i+6]) == `\u2029`) {
				if data[i+5] == '8' {
					out = append(out, "\u2028"...)
				} else {
					out = append(out, "\u2029"...)
				}
				i += 6
				continue
			}
			out = append(out, data[i:i+2]...)
			i += 2
			continue
		}
		out = append(out, data[i])
		i++
	}
	return out, nil
}
func digest(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func normalize[T any](name string, value T) (T, error) {
	b, err := canonical(value)
	if err != nil {
		var zero T
		return zero, err
	}
	return contract.Decode[T](name, b)
}

type dictionaries struct {
	Categories     []contract.Category      `json:"categories"`
	Scenes         []contract.Scene         `json:"scenes"`
	NailTypes      []contract.NailTypeEntry `json:"nail_types"`
	LegacySeries   []contract.LegacySeries  `json:"legacy_series"`
	ArchiveSources map[string]string        `json:"archive_sources"`
}

func workspace(ctx context.Context, tx *sql.Tx) (int64, dictionaries, error) {
	var revision int64
	var text string
	var d dictionaries
	err := tx.QueryRowContext(ctx, `SELECT revision,dictionaries_json FROM workspace WHERE id=1`).Scan(&revision, &text)
	if err == nil {
		err = json.Unmarshal([]byte(text), &d)
	}
	return revision, d, err
}
func get(ctx context.Context, tx *sql.Tx, id string) (contract.ProductDocument, error) {
	var text string
	err := tx.QueryRowContext(ctx, `SELECT document_json FROM products WHERE id=? AND deleted_at IS NULL`, id).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return contract.ProductDocument{}, missing()
	}
	if err != nil {
		return contract.ProductDocument{}, err
	}
	return contract.Decode[contract.ProductDocument]("product.schema.json#/$defs/ProductDocument", []byte(text))
}
func (s *Service) Get(ctx context.Context, actor auth.Actor, id string) (contract.ProductEnvelope, error) {
	var out contract.ProductEnvelope
	if err := s.auth.Authorize(ctx, actor, "products:read"); err != nil {
		return out, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	out.Product, err = get(ctx, tx, id)
	if err != nil {
		return out, err
	}
	out.Version = out.Product.Version
	err = tx.QueryRowContext(ctx, `SELECT revision FROM workspace WHERE id=1`).Scan(&out.WorkspaceRevision)
	return out, err
}

func apply(p *contract.ProductDocument, e contract.ProductEdit) {
	p.Kind = e.Kind
	p.Name = e.Name
	p.Model = e.Model
	p.CategoryID = e.CategoryID
	p.Series = e.Series
	p.LegacySeriesIDs = e.LegacySeriesIDs
	p.Summary = e.Summary
	p.Applications = e.Applications
	p.Features = e.Features
	p.Notes = e.Notes
	p.SceneIDs = e.SceneIDs
	p.NailType = e.NailType
	p.FastenerText = e.FastenerText
	p.LongNose = e.LongNose
	p.Blower = e.Blower
	p.Images = e.Images
	p.CoverMediaID = e.CoverMediaID
	p.Visibility = e.Visibility
	p.SortOrder = e.SortOrder
	g := e.GuideProfile
	p.GuideProfile = contract.GuideProfile{SceneIDs: g.SceneIDs, Materials: g.Materials, Substrates: g.Substrates, ExcludedMaterials: g.ExcludedMaterials, ExcludedSubstrates: g.ExcludedSubstrates, PowerSource: g.PowerSource, NailType: g.NailType, FastenerLengthMM: g.FastenerLengthMM, LoadBearing: g.LoadBearing, LongNose: g.LongNose, Blower: g.Blower, Limitations: g.Limitations, ConsultationNotes: g.ConsultationNotes, RequiredRequirements: g.RequiredRequirements, Sources: g.Sources, Reviews: []contract.GuideReview{}}
}
func createEdit(c contract.ProductCreate) contract.ProductEdit {
	return contract.ProductEdit{Kind: c.Kind, Name: c.Name, Model: c.Model, CategoryID: c.CategoryID, Series: c.Series, LegacySeriesIDs: c.LegacySeriesIDs, Summary: c.Summary, Applications: c.Applications, Features: c.Features, Notes: c.Notes, SceneIDs: c.SceneIDs, NailType: c.NailType, FastenerText: c.FastenerText, LongNose: c.LongNose, Blower: c.Blower, Images: c.Images, CoverMediaID: c.CoverMediaID, Visibility: c.Visibility, SortOrder: c.SortOrder, GuideProfile: c.GuideProfile, PurchaseOptions: c.PurchaseOptions}
}
func (s *Service) channels(old []contract.PurchaseOption, input []contract.PurchaseOptionEdit) ([]contract.PurchaseOption, error) {
	out := make([]contract.PurchaseOption, 0, len(input))
	for _, c := range input {
		id := ""
		if c.ID != nil {
			found := false
			for _, v := range old {
				if v.ID == *c.ID {
					found = true
					break
				}
			}
			if !found {
				return nil, invalid("purchase_options.id")
			}
			id = *c.ID
		} else {
			var err error
			id, err = randomID("po_")
			if err != nil {
				return nil, err
			}
		}
		if c.URL != "" {
			if err := purchaseTarget(c.URL, s.hosts); err != nil {
				return nil, err
			}
		}
		if c.Enabled && (strings.TrimSpace(c.Platform) == "" || strings.TrimSpace(c.StoreName) == "" || c.URL == "" || c.MatchScope == "listing_with_variants" && strings.TrimSpace(c.SkuHint) == "") {
			return nil, invalid("purchase_options")
		}
		out = append(out, contract.PurchaseOption{ID: id, Platform: c.Platform, StoreName: c.StoreName, URL: c.URL, MatchScope: c.MatchScope, SkuHint: c.SkuHint, Enabled: c.Enabled})
	}
	return out, nil
}
func purchaseTarget(raw string, hosts []string) error {
	if err := contract.ValidatePurchaseURL(raw, hosts); err != nil {
		return err
	}
	u, err := url.Parse(raw)
	if err != nil {
		return invalid("purchase_options.url")
	}
	path := strings.ToLower(strings.Trim(u.Path, "/"))
	if path == "" || u.Opaque != "" || u.Port() != "" && u.Port() != "443" {
		return invalid("purchase_options.url")
	}
	for _, segment := range strings.Split(path, "/") {
		switch segment {
		case "search", "search.htm", "search.html", "redirect", "redirect.htm", "redirect.html", "shop", "store", "view_shop.htm":
			return invalid("purchase_options.url")
		}
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return invalid("purchase_options.url")
	}
	for key := range query {
		switch strings.ToLower(key) {
		case "url", "target", "redirect", "redirect_url", "redirect_uri", "return_url":
			return invalid("purchase_options.url")
		}
	}
	return nil
}
func (s *Service) references(ctx context.Context, tx *sql.Tx, p *contract.ProductDocument, d dictionaries) error {
	for _, id := range p.LegacySeriesIDs {
		found := false
		for _, series := range d.LegacySeries {
			if series.ID == id {
				found = true
				break
			}
		}
		if !found {
			return invalid("legacy_series_ids")
		}
	}
	for _, img := range p.Images {
		if _, err := media.Metadata(ctx, tx, img.MediaID); err != nil {
			return err
		}
	}
	for i := range p.GuideProfile.Sources {
		source := &p.GuideProfile.Sources[i]
		switch source.Kind {
		case "media":
			m, err := media.Metadata(ctx, tx, source.Reference)
			if err != nil {
				return err
			}
			source.SHA256 = m.SHA256
		case "document":
			sha, ok := d.ArchiveSources[source.Reference]
			if !ok || sha != source.SHA256 {
				return invalid("guide_profile.sources")
			}
		case "product_text":
			value, err := textReference(*p, source.Reference)
			if err != nil {
				return err
			}
			b, err := canonical(value)
			if err != nil {
				return err
			}
			source.SHA256 = digest(b)
		default:
			return invalid("guide_profile.sources")
		}
	}
	return nil
}

// Text sources are JSON Pointers into editable display data, never server fields
// or guide facts themselves. Nested pointers may select an array item or caption.
func textReference(p contract.ProductDocument, pointer string) (any, error) {
	parts := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
	if !strings.HasPrefix(pointer, "/") || len(parts) == 0 {
		return nil, invalid("guide_profile.sources.reference")
	}
	var value any
	switch parts[0] {
	case "name":
		value = p.Name
	case "model":
		value = p.Model
	case "series":
		value = p.Series
	case "summary":
		value = p.Summary
	case "applications":
		value = p.Applications
	case "features":
		value = p.Features
	case "notes":
		value = p.Notes
	case "fastener_text":
		value = p.FastenerText
	case "scene_ids":
		value = p.SceneIDs
	case "nail_type":
		value = p.NailType
	case "long_nose":
		value = p.LongNose
	case "blower":
		value = p.Blower
	case "images":
		value = p.Images
	default:
		return nil, invalid("guide_profile.sources.reference")
	}
	if len(parts) == 1 {
		return value, nil
	}
	b, err := canonical(value)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(b, &value); err != nil {
		return nil, err
	}
	for _, part := range parts[1:] {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		switch v := value.(type) {
		case []any:
			n, err := strconv.Atoi(part)
			if err != nil || n < 0 || n >= len(v) {
				return nil, invalid("guide_profile.sources.reference")
			}
			value = v[n]
		case map[string]any:
			var ok bool
			value, ok = v[part]
			if !ok {
				return nil, invalid("guide_profile.sources.reference")
			}
		default:
			return nil, invalid("guide_profile.sources.reference")
		}
	}
	return value, nil
}

// FactSHA256 is the digest expected by GuideReviewInput: compact UTF-8 JSON of
// exactly one typed fact, with HTML escaping disabled and no trailing newline.
func FactSHA256(g contract.GuideProfile, field contract.GuideField) (string, error) {
	var value any
	switch field {
	case "scene_ids":
		value = g.SceneIDs
	case "materials":
		value = g.Materials
	case "substrates":
		value = g.Substrates
	case "excluded_materials":
		value = g.ExcludedMaterials
	case "excluded_substrates":
		value = g.ExcludedSubstrates
	case "power_source":
		value = g.PowerSource
	case "nail_type":
		value = g.NailType
	case "fastener_length_mm":
		value = g.FastenerLengthMM
	case "load_bearing":
		value = g.LoadBearing
	case "long_nose":
		value = g.LongNose
	case "blower":
		value = g.Blower
	case "limitations":
		value = g.Limitations
	case "consultation_notes":
		value = g.ConsultationNotes
	case "required_requirements":
		value = g.RequiredRequirements
	default:
		return "", invalid("facts.field")
	}
	b, err := canonical(value)
	if err != nil {
		return "", err
	}
	if bytes.Equal(b, []byte("null")) {
		return "", invalid("facts.field")
	}
	return digest(b), nil
}
func sourcesByHash(sources []contract.GuideSource, hash string) []contract.GuideSource {
	out := []contract.GuideSource{}
	for _, s := range sources {
		if s.SHA256 == hash {
			out = append(out, s)
		}
	}
	slices.SortFunc(out, func(a, b contract.GuideSource) int { return strings.Compare(a.ID, b.ID) })
	return out
}
func retainedReviews(old, new contract.GuideProfile) []contract.GuideReview {
	out := []contract.GuideReview{}
	for _, r := range old.Reviews {
		hash, err := FactSHA256(new, r.Field)
		if err != nil || hash != r.FactSHA256 {
			continue
		}
		valid := true
		for _, sha := range r.SourceSha256s {
			before := sourcesByHash(old.Sources, sha)
			after := sourcesByHash(new.Sources, sha)
			b, _ := canonical(before)
			a, _ := canonical(after)
			if len(after) == 0 || !bytes.Equal(a, b) {
				valid = false
				break
			}
		}
		if valid {
			out = append(out, r)
		}
	}
	return out
}
func updateSeries(d *dictionaries, p contract.ProductDocument, deleted bool) {
	for i := range d.LegacySeries {
		series := &d.LegacySeries[i]
		wanted := !deleted && slices.Contains(p.LegacySeriesIDs, series.ID)
		present := slices.Contains(series.ProductIDs, p.ID)
		if !wanted && present {
			series.ProductIDs = slices.DeleteFunc(series.ProductIDs, func(id string) bool { return id == p.ID })
		} else if wanted && !present {
			series.ProductIDs = append(series.ProductIDs, p.ID)
		}
	}
}
func save(ctx context.Context, tx *sql.Tx, p contract.ProductDocument, d dictionaries, create bool) (contract.ProductEnvelope, error) {
	var out contract.ProductEnvelope
	b, err := canonical(p)
	if err != nil {
		return out, err
	}
	if err = contract.ValidateJSON("product.schema.json#/$defs/ProductDocument", b); err != nil {
		return out, err
	}
	if create {
		_, err = tx.ExecContext(ctx, `INSERT INTO products(id,slug,model,version,document_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, p.ID, p.Slug, p.Model, p.Version, string(b), p.UpdatedAt, p.UpdatedAt)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE products SET model=?,version=?,document_json=?,updated_at=? WHERE id=?`, p.Model, p.Version, string(b), p.UpdatedAt, p.ID)
	}
	if err != nil {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM guide_reviews WHERE product_id=?`, p.ID); err != nil {
		return out, err
	}
	for _, r := range p.GuideProfile.Reviews {
		sources, _ := canonical(r.SourceSha256s)
		if _, err = tx.ExecContext(ctx, `INSERT INTO guide_reviews(product_id,field,fact_sha256,source_sha256s_json,reviewed_by,reviewed_at) VALUES(?,?,?,?,?,?)`, p.ID, r.Field, r.FactSHA256, string(sources), r.ReviewedBy, r.ReviewedAt); err != nil {
			return out, err
		}
	}
	updateSeries(&d, p, false)
	dict, err := canonical(d)
	if err != nil {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE workspace SET revision=revision+1,dictionaries_json=? WHERE id=1`, string(dict)); err != nil {
		return out, err
	}
	out.Product = p
	out.Version = p.Version
	err = tx.QueryRowContext(ctx, `SELECT revision FROM workspace WHERE id=1`).Scan(&out.WorkspaceRevision)
	return out, err
}
func uniqueModel(ctx context.Context, tx *sql.Tx, p contract.ProductDocument) error {
	if p.Model == "" {
		return nil
	}
	// SQLite NOCASE indexes ASCII only; the shared snapshot contract folds Unicode.
	rows, err := tx.QueryContext(ctx, `SELECT model FROM products WHERE deleted_at IS NULL AND model<>'' AND id<>?`, p.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	key := strings.ToLower(p.Model)
	for rows.Next() {
		var model string
		if err := rows.Scan(&model); err != nil {
			return err
		}
		if strings.ToLower(model) == key {
			return contract.NewBusinessError(409, "MODEL_CONFLICT", "型号已被其他产品使用。", "model")
		}
	}
	return rows.Err()
}
func (s *Service) Create(ctx context.Context, actor auth.Actor, input contract.ProductCreate) (contract.ProductEnvelope, error) {
	var out contract.ProductEnvelope
	if err := s.auth.Authorize(ctx, actor, "products:write"); err != nil {
		return out, err
	}
	c, err := normalize("product.schema.json#/$defs/ProductCreate", input)
	if err != nil {
		return out, err
	}
	if c.Visibility != "unlisted" && !actor.IsBrowser() {
		return out, invalid("visibility")
	}
	id, err := randomID("p_")
	if err != nil {
		return out, err
	}
	p := contract.ProductDocument{ID: id, Slug: c.Slug, Version: 1, UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	apply(&p, createEdit(c))
	p.PurchaseOptions, err = s.channels(nil, c.PurchaseOptions)
	if err != nil {
		return out, err
	}
	s.gate.Lock()
	defer s.gate.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if previous, found, e := replay[contract.ProductEnvelope](ctx, tx, actor, "create", input); e != nil || found {
		return previous, e
	}
	_, d, err := workspace(ctx, tx)
	if err != nil {
		return out, err
	}
	if err = s.references(ctx, tx, &p, d); err != nil {
		return out, err
	}
	if err = uniqueModel(ctx, tx, p); err != nil {
		return out, err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM reserved_routes WHERE slug=?`, p.Slug).Scan(&count); err != nil {
		return out, err
	}
	if count != 0 {
		return out, contract.NewBusinessError(409, "ROUTE_CONFLICT", "网址已被产品或旧系列永久占用。", "slug")
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO reserved_routes(slug,owner_kind,owner_id,created_at) VALUES(?,'product',?,?)`, p.Slug, p.ID, p.UpdatedAt); err != nil {
		return out, err
	}
	out, err = save(ctx, tx, p, d, true)
	if err != nil {
		return out, err
	}
	if actor.IsBrowser() {
		if err = s.confirmBrowser(ctx, tx, p, false, d); err != nil {
			return out, err
		}
	}
	if err = rememberWrite(ctx, tx, actor, "create", input, out); err != nil {
		return out, err
	}
	return out, tx.Commit()
}
func (s *Service) Replace(ctx context.Context, actor auth.Actor, id string, expected int64, input contract.ProductEdit) (contract.ProductEnvelope, error) {
	var out contract.ProductEnvelope
	if err := s.auth.Authorize(ctx, actor, "products:write"); err != nil {
		return out, err
	}
	edit, err := normalize("product.schema.json#/$defs/ProductEdit", input)
	if err != nil {
		return out, err
	}
	s.gate.Lock()
	defer s.gate.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	request := []any{id, expected, input}
	if previous, found, e := replay[contract.ProductEnvelope](ctx, tx, actor, "replace", request); e != nil || found {
		return previous, e
	}
	old, err := get(ctx, tx, id)
	if err != nil {
		return out, err
	}
	if err = expectedVersion(expected, old.Version); err != nil {
		return out, err
	}
	_, d, err := workspace(ctx, tx)
	if err != nil {
		return out, err
	}
	p := contract.ProductDocument{ID: old.ID, Slug: old.Slug, Version: old.Version + 1, UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	apply(&p, edit)
	p.PurchaseOptions, err = s.channels(old.PurchaseOptions, edit.PurchaseOptions)
	if err != nil {
		return out, err
	}
	if actor.IsBrowser() {
		pruneTextSources(&p)
	}
	if err = s.references(ctx, tx, &p, d); err != nil {
		return out, err
	}
	if err = uniqueModel(ctx, tx, p); err != nil {
		return out, err
	}
	p.GuideProfile.Reviews = retainedReviews(old.GuideProfile, p.GuideProfile)
	out, err = save(ctx, tx, p, d, false)
	if err != nil {
		return out, err
	}
	if actor.IsBrowser() {
		if err = s.confirmBrowser(ctx, tx, p, false, d); err != nil {
			return out, err
		}
	}
	if err = rememberWrite(ctx, tx, actor, "replace", request, out); err != nil {
		return out, err
	}
	return out, tx.Commit()
}
func (s *Service) Review(ctx context.Context, actor auth.Actor, id string, expected int64, input contract.GuideReviewInput) (contract.ProductEnvelope, error) {
	var out contract.ProductEnvelope
	if err := actor.RequireBrowser(); err != nil {
		return out, err
	}
	if err := s.auth.Authorize(ctx, actor, "products:write"); err != nil {
		return out, err
	}
	input, err := normalize("product.schema.json#/$defs/GuideReviewInput", input)
	if err != nil {
		return out, err
	}
	s.gate.Lock()
	defer s.gate.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	p, err := get(ctx, tx, id)
	if err != nil {
		return out, err
	}
	if err = expectedVersion(expected, p.Version); err != nil {
		return out, err
	}
	if err = expectedVersion(input.Version, p.Version); err != nil {
		return out, err
	}
	_, d, err := workspace(ctx, tx)
	if err != nil {
		return out, err
	}
	if err = s.references(ctx, tx, &p, d); err != nil {
		return out, err
	}
	for _, fact := range input.Facts {
		hash, e := FactSHA256(p.GuideProfile, fact.Field)
		if e != nil {
			return out, e
		}
		if hash != fact.FactSHA256 {
			return out, invalid("facts.fact_sha256")
		}
		for _, sha := range fact.SourceSha256s {
			if len(sourcesByHash(p.GuideProfile.Sources, sha)) == 0 {
				return out, invalid("facts.source_sha256s")
			}
		}
		p.GuideProfile.Reviews = slices.DeleteFunc(p.GuideProfile.Reviews, func(r contract.GuideReview) bool { return r.Field == fact.Field })
		p.GuideProfile.Reviews = append(p.GuideProfile.Reviews, contract.GuideReview{Field: fact.Field, FactSHA256: hash, SourceSha256s: fact.SourceSha256s, ReviewedBy: actor.AdminID(), ReviewedAt: time.Now().UTC().Format(time.RFC3339Nano)})
	}
	slices.SortFunc(p.GuideProfile.Reviews, func(a, b contract.GuideReview) int { return strings.Compare(string(a.Field), string(b.Field)) })
	p.Version++
	p.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	out, err = save(ctx, tx, p, d, false)
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}
func (s *Service) Delete(ctx context.Context, actor auth.Actor, id string, expected int64) (contract.DeletionResult, error) {
	var out contract.DeletionResult
	if err := s.auth.Authorize(ctx, actor, "products:write"); err != nil {
		return out, err
	}
	s.gate.Lock()
	defer s.gate.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	request := []any{id, expected}
	if previous, found, e := replay[contract.DeletionResult](ctx, tx, actor, "delete", request); e != nil || found {
		return previous, e
	}
	p, err := get(ctx, tx, id)
	if err != nil {
		return out, err
	}
	if err = expectedVersion(expected, p.Version); err != nil {
		return out, err
	}
	// A browser confirmation protects the product before its first activation,
	// including when a newer machine draft has not changed that confirmation.
	var protected bool
	if err = tx.QueryRowContext(ctx, `SELECT ever_published<>0 OR EXISTS(SELECT 1 FROM product_confirmations WHERE product_id=? AND deleted=0) FROM reserved_routes WHERE slug=?`, p.ID, p.Slug).Scan(&protected); err != nil {
		return out, err
	}
	if protected && !actor.IsBrowser() {
		return out, contract.NewBusinessError(409, "PRODUCT_PUBLISHED", "已确认或已公开的产品，请在浏览器中确认删除。")
	}
	_, d, err := workspace(ctx, tx)
	if err != nil {
		return out, err
	}
	updateSeries(&d, p, true)
	dict, err := canonical(d)
	if err != nil {
		return out, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	p.Version++
	p.UpdatedAt = now
	b, err := canonical(p)
	if err != nil {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE products SET deleted_at=?,version=?,updated_at=?,document_json=? WHERE id=?`, now, p.Version, now, string(b), id); err != nil {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM guide_reviews WHERE product_id=?`, id); err != nil {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE workspace SET revision=revision+1,dictionaries_json=? WHERE id=1`, string(dict)); err != nil {
		return out, err
	}
	out.DeletedID = id
	if err = tx.QueryRowContext(ctx, `SELECT revision FROM workspace WHERE id=1`).Scan(&out.WorkspaceRevision); err != nil {
		return out, err
	}
	if actor.IsBrowser() {
		if err = s.confirmBrowser(ctx, tx, p, true, d); err != nil {
			return out, err
		}
	}
	if err = rememberWrite(ctx, tx, actor, "delete", request, out); err != nil {
		return out, err
	}
	return out, tx.Commit()
}

func normalizeSearch(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r == '_' || r == '-' {
			return -1
		}
		return unicode.ToLower(r)
	}, value)
}

type productCursor struct {
	Revision int64  `json:"r"`
	Query    string `json:"q"`
	Sort     int64  `json:"s"`
	ID       string `json:"id"`
}

func encodeCursor(c productCursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}
func parseCursor(value string) (productCursor, error) {
	var c productCursor
	b, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return c, invalid("cursor")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || c.Revision < 0 || c.Query == "" || c.ID == "" {
		return c, invalid("cursor")
	}
	if d.Decode(new(any)) != io.EOF {
		return c, invalid("cursor")
	}
	return c, nil
}
func (s *Service) Search(ctx context.Context, actor auth.Actor, input contract.AdminProductQuery) (contract.AdminProductList, error) {
	out := contract.AdminProductList{Items: []contract.ProductSummary{}}
	if err := s.auth.Authorize(ctx, actor, "products:read"); err != nil {
		return out, err
	}
	q, err := normalize("admin.schema.json#/$defs/AdminProductQuery", input)
	if err != nil {
		return out, err
	}
	text := ""
	if q.Q != nil {
		text = normalizeSearch(*q.Q)
	}
	category, visibility := "", ""
	if q.Category != nil {
		category = string(*q.Category)
	}
	if q.Visibility != nil {
		visibility = *q.Visibility
	}
	limit := int64(20)
	if q.Limit != nil {
		limit = *q.Limit
	}
	queryBytes, _ := canonical([]any{text, category, visibility, limit})
	queryHash := digest(queryBytes)
	s.gate.RLock()
	defer s.gate.RUnlock()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = tx.QueryRowContext(ctx, `SELECT revision FROM workspace WHERE id=1`).Scan(&out.WorkspaceRevision); err != nil {
		return out, err
	}
	var cursor productCursor
	if q.Cursor != nil {
		cursor, err = parseCursor(*q.Cursor)
		if err != nil {
			return out, err
		}
		if cursor.Revision != out.WorkspaceRevision || cursor.Query != queryHash {
			return out, contract.NewBusinessError(409, "WORKSPACE_CHANGED", "工作区或查询条件已变化，请重新查询。")
		}
	}
	versions := map[string]int64{}
	active, err := os.ReadFile(filepath.Join(s.dir, "state", "active.json"))
	if err == nil {
		authority, e := contract.Decode[contract.ActiveRelease]("catalog-snapshot.schema.json#/$defs/ActiveRelease", active)
		if e != nil {
			return out, e
		}
		out.ActiveReleaseID = &authority.ReleaseID
		var data string
		if e = tx.QueryRowContext(ctx, `SELECT snapshot_json FROM candidates WHERE id=?`, authority.ReleaseID).Scan(&data); e != nil {
			return out, e
		}
		var snapshot contract.CatalogSnapshot
		if e = json.Unmarshal([]byte(data), &snapshot); e != nil {
			return out, e
		}
		for _, p := range snapshot.Products {
			versions[p.ID] = p.Version
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT p.document_json,r.ever_published FROM products p JOIN reserved_routes r ON r.slug=p.slug WHERE p.deleted_at IS NULL ORDER BY json_extract(p.document_json,'$.sort_order'),p.id`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	var last productCursor
	for rows.Next() {
		var data string
		var published int
		if err = rows.Scan(&data, &published); err != nil {
			return out, err
		}
		var p contract.ProductDocument
		if err = json.Unmarshal([]byte(data), &p); err != nil {
			return out, err
		}
		if q.Cursor != nil && (p.SortOrder < cursor.Sort || p.SortOrder == cursor.Sort && p.ID <= cursor.ID) {
			continue
		}
		if category != "" && string(p.CategoryID) != category || visibility != "" && p.Visibility != visibility {
			continue
		}
		if text != "" {
			haystack := p.Name + " " + p.Model + " " + p.Slug + " " + p.Summary + " " + p.Series + " " + strings.Join(p.Applications, " ") + " " + strings.Join(p.Features, " ") + " " + strings.Join(p.Notes, " ")
			if !strings.Contains(normalizeSearch(haystack), text) {
				continue
			}
		}
		if int64(len(out.Items)) == limit {
			next := encodeCursor(last)
			out.NextCursor = &next
			break
		}
		out.Items = append(out.Items, contract.ProductSummary{ID: p.ID, Slug: p.Slug, Name: p.Name, Model: p.Model, CategoryID: p.CategoryID, Visibility: p.Visibility, SortOrder: p.SortOrder, Version: p.Version, UpdatedAt: p.UpdatedAt, EverPublished: published != 0, HasUnpublishedChanges: versions[p.ID] != p.Version})
		last = productCursor{Revision: out.WorkspaceRevision, Query: queryHash, Sort: p.SortOrder, ID: p.ID}
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	return out, nil
}

// Snapshot reads the complete workspace inside the caller's transaction. T06
// must authorize before BeginTx and hold the same Gate while freezing. It returns
// draft-valid data; publication separately applies PublishableSnapshot rules.
func (s *Service) Snapshot(ctx context.Context, tx *sql.Tx, releaseID string) (contract.CatalogSnapshot, error) {
	revision, d, err := workspace(ctx, tx)
	if err != nil {
		return contract.CatalogSnapshot{}, err
	}
	out := contract.CatalogSnapshot{SchemaVersion: 1, ReleaseID: releaseID, WorkspaceRevision: revision, Categories: d.Categories, Scenes: d.Scenes, NailTypes: d.NailTypes, LegacySeries: d.LegacySeries, Products: []contract.SnapshotProduct{}, PreviouslyPublishedRoutes: []contract.PublishedRoute{}}
	rows, err := tx.QueryContext(ctx, `SELECT document_json FROM products WHERE deleted_at IS NULL ORDER BY json_extract(document_json,'$.sort_order'),id`)
	if err != nil {
		return out, err
	}
	documents := []contract.ProductDocument{}
	for rows.Next() {
		var text string
		if err = rows.Scan(&text); err != nil {
			rows.Close()
			return out, err
		}
		var p contract.ProductDocument
		if err = json.Unmarshal([]byte(text), &p); err != nil {
			rows.Close()
			return out, err
		}
		documents = append(documents, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	for _, p := range documents {
		snapshot := snapshotFromDocument(p)
		for i, img := range p.Images {
			m, err := media.Metadata(ctx, tx, img.MediaID)
			if err != nil {
				return out, err
			}
			snapshot.Images[i] = contract.SnapshotProductImage{MediaID: m.MediaID, SHA256: m.SHA256, Format: m.Format, Bytes: m.Bytes, Width: m.Width, Height: m.Height, Path: m.Path, Label: img.Label, Description: img.Description}
		}
		out.Products = append(out.Products, snapshot)
	}
	rows, err = tx.QueryContext(ctx, `SELECT owner_id,slug FROM reserved_routes WHERE owner_kind='product' AND ever_published=1 ORDER BY slug`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var route contract.PublishedRoute
		if err = rows.Scan(&route.ProductID, &route.Slug); err != nil {
			return out, err
		}
		out.PreviouslyPublishedRoutes = append(out.PreviouslyPublishedRoutes, route)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	return out, nil
}
