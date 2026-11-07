package content_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"yuanfa-official/backend/internal/app"
	"yuanfa-official/backend/internal/auth"
	"yuanfa-official/backend/internal/config"
	"yuanfa-official/backend/internal/content"
	"yuanfa-official/backend/internal/contract"
)

func fixture(t *testing.T) (*app.App, auth.Actor) {
	t.Helper()
	ctx := context.Background()
	a, err := app.Open(ctx, config.Config{DataDir: t.TempDir(), SiteURL: "http://127.0.0.1:18004", PurchaseAllowedHosts: []string{"item.example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	password := rand.Text()
	if err = a.Auth.InitAdmin(ctx, "admin", password); err != nil {
		t.Fatal(err)
	}
	login, err := a.Auth.Login(ctx, contract.LoginInput{Username: "admin", Password: password})
	if err != nil {
		t.Fatal(err)
	}
	return a, login.Session.Actor
}
func draft(slug string) contract.ProductCreate {
	return contract.ProductCreate{Slug: slug, Kind: "tool", CategoryID: "straight", Visibility: "unlisted", LegacySeriesIDs: []string{}, Applications: []string{}, Features: []string{}, Notes: []string{}, SceneIDs: []contract.SceneID{}, Images: []contract.ProductImage{}, PurchaseOptions: []contract.PurchaseOptionEdit{}, GuideProfile: contract.GuideProfileEdit{SceneIDs: []contract.SceneID{}, Materials: []string{}, Substrates: []string{}, ExcludedMaterials: []string{}, ExcludedSubstrates: []string{}, Limitations: []string{}, ConsultationNotes: []string{}, RequiredRequirements: []contract.RequirementField{}, Sources: []contract.GuideSource{}}}
}
func edit(p contract.ProductDocument) contract.ProductEdit {
	b, _ := json.Marshal(p)
	var e contract.ProductEdit
	json.Unmarshal(b, &e)
	return e
}
func status(t *testing.T, err error, want int) {
	t.Helper()
	var b *contract.BusinessError
	if !errors.As(err, &b) || b.HTTPStatus != want {
		t.Fatalf("want status %d, got %v", want, err)
	}
}
func sha(value any) string {
	b, _ := json.Marshal(value)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func reviewInput(t *testing.T, p contract.ProductDocument, field string, source string) contract.GuideReviewInput {
	t.Helper()
	hash, err := content.FactSHA256(p.GuideProfile, contract.GuideField(field))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]any{"version": p.Version, "facts": []any{map[string]any{"field": field, "fact_sha256": hash, "source_sha256s": []string{source}}}})
	input, err := contract.Decode[contract.GuideReviewInput]("product.schema.json#/$defs/GuideReviewInput", b)
	if err != nil {
		t.Fatal(err)
	}
	return input
}
func TestDraftConcurrencyAndPermanentRoutes(t *testing.T) {
	a, actor := fixture(t)
	ctx := context.Background()
	p, err := a.Content.Create(ctx, actor, draft("unfinished"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Product.Name != "" || p.Product.Visibility != "unlisted" {
		t.Fatal("incomplete draft changed")
	}
	changes := edit(p.Product)
	changes.Summary = "first save"
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := a.Content.Replace(ctx, actor, p.Product.ID, p.Version, changes)
			results <- e
		}()
	}
	wg.Wait()
	close(results)
	passed, conflicted := 0, 0
	for err := range results {
		if err == nil {
			passed++
		} else {
			status(t, err, 412)
			conflicted++
		}
	}
	if passed != 1 || conflicted != 1 {
		t.Fatal("both stale writers committed")
	}
	current, err := a.Content.Get(ctx, actor, p.Product.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Version != 2 || current.WorkspaceRevision != 2 {
		t.Fatal("atomic version/revision invariant")
	}
	_, err = a.Content.Replace(ctx, actor, p.Product.ID, 0, changes)
	status(t, err, 428)
	if _, err = a.Content.Delete(ctx, actor, p.Product.ID, current.Version); err != nil {
		t.Fatal(err)
	}
	_, err = a.Content.Create(ctx, actor, draft("unfinished"))
	status(t, err, 409)
	if _, err = a.Store.DB().Exec(`INSERT INTO reserved_routes(slug,owner_kind,owner_id,created_at) VALUES('legacy-series','series','s_legacy',?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	_, err = a.Content.Create(ctx, actor, draft("legacy-series"))
	status(t, err, 409)
}
func TestReviewInvalidatesOnlyChangedFactsAndReferencedSources(t *testing.T) {
	a, actor := fixture(t)
	ctx := context.Background()
	c := draft("review")
	c.Summary = "verified wood"
	c.Notes = []string{"unchanged origin"}
	c.GuideProfile.Materials = []string{"wood"}
	c.GuideProfile.Substrates = []string{"panel"}
	c.GuideProfile.Sources = []contract.GuideSource{{ID: "summary", Kind: "product_text", Reference: "/summary", SHA256: sha(c.Summary)}, {ID: "notes", Kind: "product_text", Reference: "/notes", SHA256: sha(c.Notes)}}
	p, err := a.Content.Create(ctx, actor, c)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ field, source string }{{"materials", sha(c.Summary)}, {"substrates", sha(c.Notes)}} {
		p, err = a.Content.Review(ctx, actor, p.Product.ID, p.Version, reviewInput(t, p.Product, item.field, item.source))
		if err != nil {
			t.Fatal(err)
		}
	}
	e := edit(p.Product)
	e.Summary = "changed origin"
	p, err = a.Content.Replace(ctx, actor, p.Product.ID, p.Version, e)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Product.GuideProfile.Reviews) != 1 || p.Product.GuideProfile.Reviews[0].Field != "substrates" {
		t.Fatal("source edit did not selectively invalidate")
	}
	e = edit(p.Product)
	e.GuideProfile.Substrates = []string{"steel"}
	p, err = a.Content.Replace(ctx, actor, p.Product.ID, p.Version, e)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Product.GuideProfile.Reviews) != 0 {
		t.Fatal("changed fact retained review")
	}
	var count int
	if err = a.Store.DB().QueryRow(`SELECT count(*) FROM guide_reviews WHERE product_id=?`, p.Product.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("review table not synchronized")
	}
	issued, err := a.Auth.IssueToken(ctx, actor, contract.TokenInput{Name: "content test", ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), Scopes: []contract.TokenScope{"products:read", "products:write"}})
	if err != nil {
		t.Fatal(err)
	}
	token, err := a.Auth.AuthenticateToken(ctx, issued.Secret)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.Content.Review(ctx, token, p.Product.ID, p.Version, reviewInput(t, p.Product, "materials", p.Product.GuideProfile.Sources[0].SHA256))
	status(t, err, 403)
	if err = a.Auth.RevokeToken(ctx, actor, issued.Token.ID); err != nil {
		t.Fatal(err)
	}
	_, err = a.Content.Get(ctx, token, p.Product.ID)
	status(t, err, 401)
}
func TestPurchaseOwnershipRemovalAndReordering(t *testing.T) {
	a, actor := fixture(t)
	ctx := context.Background()
	c := draft("channels")
	c.PurchaseOptions = []contract.PurchaseOptionEdit{{Platform: "Test", StoreName: "Verified", URL: "https://item.example.test/product/1", MatchScope: "exact_model", Enabled: true}, {Platform: "Test", StoreName: "Variants", URL: "https://item.example.test/product/2", MatchScope: "listing_with_variants", SkuHint: "pick model", Enabled: true}}
	p, err := a.Content.Create(ctx, actor, c)
	if err != nil {
		t.Fatal(err)
	}
	first := p.Product.PurchaseOptions[0].ID
	second := p.Product.PurchaseOptions[1].ID
	e := edit(p.Product)
	e.PurchaseOptions[0], e.PurchaseOptions[1] = e.PurchaseOptions[1], e.PurchaseOptions[0]
	p, err = a.Content.Replace(ctx, actor, p.Product.ID, p.Version, e)
	if err != nil {
		t.Fatal(err)
	}
	if p.Product.PurchaseOptions[0].ID != second || p.Product.PurchaseOptions[1].ID != first {
		t.Fatal("channel identity changed during reorder")
	}
	e = edit(p.Product)
	e.PurchaseOptions = e.PurchaseOptions[:1]
	p, err = a.Content.Replace(ctx, actor, p.Product.ID, p.Version, e)
	if err != nil {
		t.Fatal(err)
	}
	e = edit(p.Product)
	e.PurchaseOptions[0].ID = &first
	_, err = a.Content.Replace(ctx, actor, p.Product.ID, p.Version, e)
	status(t, err, 422)
	other := draft("other-channels")
	other.PurchaseOptions = e.PurchaseOptions
	_, err = a.Content.Create(ctx, actor, other)
	status(t, err, 422)
	for _, url := range []string{"http://item.example.test/a", "https://item.example.test.evil/a", "https://user:pass@item.example.test/a", "https://short.example/a"} {
		e = edit(p.Product)
		e.PurchaseOptions[0].URL = url
		_, err = a.Content.Replace(ctx, actor, p.Product.ID, p.Version, e)
		status(t, err, 422)
	}
	e = edit(p.Product)
	e.PurchaseOptions[0].SkuHint = ""
	_, err = a.Content.Replace(ctx, actor, p.Product.ID, p.Version, e)
	status(t, err, 422)
}
func TestSearchCursorBindsQueryAndWorkspace(t *testing.T) {
	a, actor := fixture(t)
	ctx := context.Background()
	for _, slug := range []string{"one", "two", "three"} {
		c := draft(slug)
		c.Model = "MODEL-" + slug
		if _, err := a.Content.Create(ctx, actor, c); err != nil {
			t.Fatal(err)
		}
	}
	limit := int64(1)
	q := contract.AdminProductQuery{Limit: &limit}
	page, err := a.Content.Search(ctx, actor, q)
	if err != nil {
		t.Fatal(err)
	}
	if page.NextCursor == nil {
		t.Fatal("missing continuation")
	}
	q.Cursor = page.NextCursor
	next, err := a.Content.Search(ctx, actor, q)
	if err != nil {
		t.Fatal(err)
	}
	if next.Items[0].ID == page.Items[0].ID {
		t.Fatal("duplicate cursor boundary")
	}
	query := "model one"
	_, err = a.Content.Search(ctx, actor, contract.AdminProductQuery{Q: &query, Limit: &limit, Cursor: page.NextCursor})
	status(t, err, 409)
	found, err := a.Content.Search(ctx, actor, contract.AdminProductQuery{Q: &query})
	if err != nil || len(found.Items) != 1 || found.Items[0].Model != "MODEL-one" {
		t.Fatal("normalization failed", err)
	}
	if _, err = a.Content.Create(ctx, actor, draft("four")); err != nil {
		t.Fatal(err)
	}
	_, err = a.Content.Search(ctx, actor, q)
	status(t, err, 409)
}
func TestImportIsIdempotentAndKeepsEditedWorkspace(t *testing.T) {
	a, actor := fixture(t)
	ctx := context.Background()
	root := filepath.Join("..", "..", "..")
	seed := filepath.Join(root, "seed", "initial-catalog.json")
	images := filepath.Join(root, "src", "assets", "products")
	if err := a.Content.ImportSeed(ctx, seed, images); err != nil {
		t.Fatal(err)
	}
	var initialDictionaries string
	if err := a.Store.DB().QueryRow(`SELECT dictionaries_json FROM workspace WHERE id=1`).Scan(&initialDictionaries); err != nil {
		t.Fatal(err)
	}
	var beforeSeries struct {
		LegacySeries []contract.LegacySeries `json:"legacy_series"`
	}
	if err := json.Unmarshal([]byte(initialDictionaries), &beforeSeries); err != nil {
		t.Fatal(err)
	}
	page, err := a.Content.Search(ctx, actor, contract.AdminProductQuery{})
	if err != nil {
		t.Fatal(err)
	}
	p, err := a.Content.Get(ctx, actor, page.Items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	e := edit(p.Product)
	e.Summary = "edited after seed"
	p, err = a.Content.Replace(ctx, actor, p.Product.ID, p.Version, e)
	if err != nil {
		t.Fatal(err)
	}
	var changedDictionaries string
	if err := a.Store.DB().QueryRow(`SELECT dictionaries_json FROM workspace WHERE id=1`).Scan(&changedDictionaries); err != nil {
		t.Fatal(err)
	}
	var afterSeries struct {
		LegacySeries []contract.LegacySeries `json:"legacy_series"`
	}
	if err := json.Unmarshal([]byte(changedDictionaries), &afterSeries); err != nil {
		t.Fatal(err)
	}
	for i, series := range beforeSeries.LegacySeries {
		if !slices.Equal(series.ProductIDs, afterSeries.LegacySeries[i].ProductIDs) {
			t.Fatal("text edit changed legacy series member order")
		}
	}
	if err = a.Content.ImportSeed(ctx, seed, images); err != nil {
		t.Fatal(err)
	}
	after, err := a.Content.Get(ctx, actor, p.Product.ID)
	if err != nil || after.Version != p.Version || after.WorkspaceRevision != p.WorkspaceRevision || after.Product.Summary != "edited after seed" {
		t.Fatal("seed overwrote edit", err)
	}
	bytes, err := os.ReadFile(seed)
	if err != nil {
		t.Fatal(err)
	}
	changed := filepath.Join(t.TempDir(), "changed.json")
	if err = os.WriteFile(changed, append(bytes, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	status(t, a.Content.ImportSeed(ctx, changed, images), 409)
	_, err = a.Content.Create(ctx, actor, draft("f30"))
	status(t, err, 409)
	tx, err := a.Store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := a.Content.Snapshot(ctx, tx, "candidate_test")
	tx.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err = contract.ValidateJSON("catalog-snapshot.schema.json#/$defs/PublishableSnapshot", b); err != nil {
		t.Fatal("freeze contract", err)
	}
	if strings.Contains(string(b), "/home/") {
		t.Fatal("private path in snapshot")
	}
}

func TestModelUniquenessIncludesUnicodeCase(t *testing.T) {
	a, actor := fixture(t)
	c := draft("unicode-one")
	c.Model = "Ä MODEL"
	if _, err := a.Content.Create(context.Background(), actor, c); err != nil {
		t.Fatal(err)
	}
	c = draft("unicode-two")
	c.Model = "ä model"
	_, err := a.Content.Create(context.Background(), actor, c)
	status(t, err, 409)
}
