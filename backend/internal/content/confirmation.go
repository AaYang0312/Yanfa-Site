package content

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"yuanfa-official/backend/internal/auth"
	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/media"
)

type requestKey struct{}

// WithRequestKey is used by the browser adapter, never by a product document.
func WithRequestKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, requestKey{}, key)
}
func replay[T any](ctx context.Context, tx *sql.Tx, actor auth.Actor, op string, input any) (T, bool, error) {
	var out T
	key, _ := ctx.Value(requestKey{}).(string)
	if key == "" {
		return out, false, nil
	}
	if len(key) > 200 || strings.ContainsAny(key, "\r\n\x00") {
		return out, false, invalid("Idempotency-Key")
	}
	var hash, result string
	err := tx.QueryRowContext(ctx, `SELECT request_sha256,result_json FROM product_write_requests WHERE actor_id=? AND operation=? AND request_key=?`, actor.IdentityKey(), op, key).Scan(&hash, &result)
	if errors.Is(err, sql.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	b, err := canonical(input)
	if err != nil {
		return out, false, err
	}
	if hash != digest(b) {
		return out, false, contract.NewBusinessError(409, "IDEMPOTENCY_CONFLICT", "此操作已提交，请刷新后重新保存。")
	}
	err = json.Unmarshal([]byte(result), &out)
	return out, true, err
}
func rememberWrite(ctx context.Context, tx *sql.Tx, actor auth.Actor, op string, input, result any) error {
	key, _ := ctx.Value(requestKey{}).(string)
	if key == "" {
		return nil
	}
	b, err := canonical(input)
	if err != nil {
		return err
	}
	data, err := canonical(result)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO product_write_requests VALUES(?,?,?,?,?,?)`, actor.IdentityKey(), op, key, digest(b), string(data), time.Now().UTC().Format(time.RFC3339Nano))
	return err
}
func confirm(ctx context.Context, tx *sql.Tx, p contract.ProductDocument, deleted bool, state string) error {
	b, err := canonical(p)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO product_confirmations(product_id,version,document_json,deleted,state,confirmed_at) VALUES(?,?,?,?,?,?) ON CONFLICT(product_id) DO UPDATE SET version=excluded.version,document_json=excluded.document_json,deleted=excluded.deleted,state=excluded.state,confirmed_at=excluded.confirmed_at`, p.ID, p.Version, string(b), deleted, state, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

// effectiveDocuments resolves only confirmed/public data inside the save gate.
// Draft model changes must not free a model that the public catalog still owns.
func (s *Service) effectiveDocuments(ctx context.Context, tx *sql.Tx) (map[string]contract.ProductDocument, error) {
	docs := map[string]contract.ProductDocument{}
	b, err := os.ReadFile(filepath.Join(s.dir, "state", "active.json"))
	if err == nil {
		active, e := contract.Decode[contract.ActiveRelease]("catalog-snapshot.schema.json#/$defs/ActiveRelease", b)
		if e != nil {
			return nil, e
		}
		var raw string
		if e = tx.QueryRowContext(ctx, `SELECT snapshot_json FROM candidates WHERE id=?`, active.ReleaseID).Scan(&raw); e != nil {
			return nil, e
		}
		var snapshot contract.CatalogSnapshot
		if e = json.Unmarshal([]byte(raw), &snapshot); e != nil {
			return nil, e
		}
		for _, p := range snapshot.Products {
			docs[p.ID] = documentFromSnapshot(p)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT document_json,deleted FROM product_confirmations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var deleted bool
		if err = rows.Scan(&raw, &deleted); err != nil {
			return nil, err
		}
		var p contract.ProductDocument
		if err = json.Unmarshal([]byte(raw), &p); err != nil {
			return nil, err
		}
		if current, ok := docs[p.ID]; ok && current.Version > p.Version {
			continue
		}
		if deleted {
			delete(docs, p.ID)
		} else {
			docs[p.ID] = p
		}
	}
	return docs, rows.Err()
}

func (s *Service) confirmBrowser(ctx context.Context, tx *sql.Tx, p contract.ProductDocument, deleted bool, d dictionaries) error {
	if deleted {
		return confirm(ctx, tx, p, true, "pending")
	}
	docs, err := s.effectiveDocuments(ctx, tx)
	if err != nil {
		return err
	}
	for id, other := range docs {
		if id != p.ID && p.Model != "" && strings.EqualFold(other.Model, p.Model) {
			return contract.NewBusinessError(409, "MODEL_CONFLICT", "该型号仍由另一个已确认产品使用，请先确认其型号修改。", "model")
		}
	}
	// The simplified editor cannot review these fields. Preserve trusted data
	// while leaving hidden MCP edits in the private workspace for explicit review.
	baseline, exists := docs[p.ID]
	if exists {
		p.Kind = baseline.Kind
		p.LegacySeriesIDs = slices.Clone(baseline.LegacySeriesIDs)
		p.SortOrder = baseline.SortOrder
		p.PurchaseOptions = slices.Clone(baseline.PurchaseOptions)
		b, _ := json.Marshal(baseline.GuideProfile)
		var guide contract.GuideProfile
		if err = json.Unmarshal(b, &guide); err != nil {
			return err
		}
		p.GuideProfile = guide
	} else {
		p.PurchaseOptions = []contract.PurchaseOption{}
		p.LegacySeriesIDs = []string{}
		p.GuideProfile = contract.GuideProfile{SceneIDs: []contract.SceneID{}, Materials: []string{}, Substrates: []string{}, ExcludedMaterials: []string{}, ExcludedSubstrates: []string{}, Limitations: []string{}, ConsultationNotes: []string{}, RequiredRequirements: []contract.RequirementField{}, Sources: []contract.GuideSource{}, Reviews: []contract.GuideReview{}}
	}
	// Visible nail/tool categories determine the public type in both directions.
	// "other" can contain different types, so preserve its trusted baseline kind.
	if p.CategoryID == "nails" {
		p.Kind = "consumable"
	} else if p.CategoryID != "other" {
		p.Kind = "tool"
	}
	// Removing a visible list item also removes references to that vanished item.
	pruneTextSources(&p)
	if err = s.references(ctx, tx, &p, d); err != nil {
		return err
	}
	p.GuideProfile.Reviews = retainedReviews(baseline.GuideProfile, p.GuideProfile)
	// Report editable omissions before committing a save, rather than creating
	// an update that can never succeed just by retrying the build.
	b, err := canonical(p)
	if err != nil {
		return err
	}
	if err = contract.ValidateJSON("product.schema.json#/$defs/PublishedProduct", b); err != nil {
		return err
	}
	return confirm(ctx, tx, p, false, "pending")
}

func pruneTextSources(p *contract.ProductDocument) {
	p.GuideProfile.Sources = slices.DeleteFunc(p.GuideProfile.Sources, func(source contract.GuideSource) bool {
		if source.Kind != "product_text" {
			return false
		}
		_, err := textReference(*p, source.Reference)
		return err != nil
	})
}

// ConfirmedSnapshot overlays frozen browser confirmations on the last public
// snapshot. Neither product documents nor legacy memberships come from drafts.
// The caller owns Gate and the transaction. Baseline is borrowed read-only.
func (s *Service) ConfirmedSnapshot(ctx context.Context, tx *sql.Tx, id string, baseline *contract.CatalogSnapshot) (contract.CatalogSnapshot, error) {
	revision, d, err := workspace(ctx, tx)
	if err != nil {
		return contract.CatalogSnapshot{}, err
	}
	out := contract.CatalogSnapshot{SchemaVersion: 1, ReleaseID: id, WorkspaceRevision: revision, Categories: d.Categories, Scenes: d.Scenes, NailTypes: d.NailTypes, LegacySeries: d.LegacySeries, Products: []contract.SnapshotProduct{}, PreviouslyPublishedRoutes: []contract.PublishedRoute{}}
	products := map[string]contract.SnapshotProduct{}
	if baseline != nil {
		for _, p := range baseline.Products {
			products[p.ID] = p
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT document_json,deleted FROM product_confirmations ORDER BY product_id`)
	if err != nil {
		return out, err
	}
	type confirmation struct {
		p       contract.ProductDocument
		deleted bool
	}
	confirmed := []confirmation{}
	for rows.Next() {
		var text string
		var c confirmation
		if err = rows.Scan(&text, &c.deleted); err != nil {
			break
		}
		if err = json.Unmarshal([]byte(text), &c.p); err != nil {
			break
		}
		confirmed = append(confirmed, c)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return out, err
	}
	for _, c := range confirmed {
		p := c.p
		if current, ok := products[p.ID]; ok && current.Version > p.Version {
			continue
		}
		if c.deleted {
			delete(products, p.ID)
			continue
		}
		snap := snapshotFromDocument(p)
		for i, img := range p.Images {
			m, e := media.Metadata(ctx, tx, img.MediaID)
			if e != nil {
				return out, e
			}
			snap.Images[i] = contract.SnapshotProductImage{MediaID: m.MediaID, SHA256: m.SHA256, Format: m.Format, Bytes: m.Bytes, Width: m.Width, Height: m.Height, Path: m.Path, Label: img.Label, Description: img.Description}
		}
		products[p.ID] = snap
	}
	for _, p := range products {
		out.Products = append(out.Products, p)
	}
	slices.SortFunc(out.Products, func(a, b contract.SnapshotProduct) int {
		if a.SortOrder < b.SortOrder {
			return -1
		}
		if a.SortOrder > b.SortOrder {
			return 1
		}
		return strings.Compare(a.ID, b.ID)
	})
	for i := range out.LegacySeries {
		series := &out.LegacySeries[i]
		series.ProductIDs = []string{}
		for _, p := range out.Products {
			if slices.Contains(p.LegacySeriesIDs, series.ID) {
				series.ProductIDs = append(series.ProductIDs, p.ID)
			}
		}
	}
	rows, err = tx.QueryContext(ctx, `SELECT owner_id,slug FROM reserved_routes WHERE owner_kind='product' AND ever_published=1 ORDER BY slug`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var r contract.PublishedRoute
		if err = rows.Scan(&r.ProductID, &r.Slug); err != nil {
			return out, err
		}
		out.PreviouslyPublishedRoutes = append(out.PreviouslyPublishedRoutes, r)
	}
	return out, rows.Err()
}
