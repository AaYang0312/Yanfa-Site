package catalog_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"yuanfa-official/backend/internal/catalog"
	"yuanfa-official/backend/internal/content"
	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/coordination"
	"yuanfa-official/backend/internal/httpapi"
	"yuanfa-official/backend/internal/release"
)

type fixture struct {
	dir      string
	gate     *coordination.Gate
	releases *release.Manager
	service  *catalog.Service
	h        *release.Handle
	snapshot contract.CatalogSnapshot
}

func setup(t *testing.T) *fixture {
	t.Helper()
	b, err := os.ReadFile("../../../seed/initial-catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{dir: t.TempDir(), gate: &coordination.Gate{}}
	if err = json.Unmarshal(b, &f.snapshot); err != nil {
		t.Fatal(err)
	}
	seed := f.snapshot.Products[0]
	seed.Kind = "entry"
	seed.Images = []contract.SnapshotProductImage{}
	seed.CoverMediaID = nil
	seed.LegacySeriesIDs = []string{}
	seed.SortOrder = 1
	known := seed
	known.ID = "p_a"
	known.Slug = "unit-a"
	known.Model = "UNIT-A"
	known.GuideProfile = reviewed(t, seed.GuideProfile)
	known.PurchaseOptions = []contract.PurchaseOption{{ID: "o_a", Platform: "隔离规则夹具", StoreName: "A", URL: "https://item.example.test/product/a", MatchScope: "exact_model", Enabled: true}, {ID: "o_disabled", Platform: "隔离规则夹具", StoreName: "disabled", URL: "https://item.example.test/product/b", MatchScope: "exact_model", Enabled: false}, {ID: "o_bad", Platform: "隔离规则夹具", StoreName: "bad", URL: "https://item.example.test.evil/product/a", MatchScope: "exact_model", Enabled: true}}
	unknown := seed
	unknown.ID = "p_b"
	unknown.Slug = "unit-b"
	unknown.Model = "UNIT-B"
	unknown.GuideProfile.Materials = []string{"PRIVATE_UNREVIEWED"}
	unknown.GuideProfile.Limitations = []string{"PRIVATE_LIMIT"}
	hidden := seed
	hidden.ID = "p_hidden"
	hidden.Slug = "unit-hidden"
	hidden.Model = "HIDDEN"
	hidden.Visibility = "unlisted"
	f.snapshot.Products = []contract.SnapshotProduct{unknown, hidden, known}
	for i := range f.snapshot.LegacySeries {
		f.snapshot.LegacySeries[i].ProductIDs = []string{}
	}
	f.releases = release.New(f.dir, f.gate)
	f.service = catalog.New(f.releases, []string{"item.example.test"})
	f.h = f.load(t, "unit_A")
	f.activate(t, f.h, "operation_A")
	return f
}
func reviewed(t *testing.T, g contract.GuideProfile) contract.GuideProfile {
	t.Helper()
	g.SceneIDs = []contract.SceneID{"home-woodworking"}
	g.Materials = []string{"木材"}
	g.ExcludedMaterials = []string{"钢板"}
	g.PowerSource = new(contract.PowerSource("pneumatic"))
	g.NailType = new(contract.NailType("brad"))
	g.FastenerLengthMM = &contract.LengthRange{Min: 10, Max: 30}
	g.RequiredRequirements = []contract.RequirementField{"material", "fastener_length_mm"}
	g.Sources = []contract.GuideSource{{ID: "private_source", Kind: "document", Reference: "private_archive", SHA256: strings.Repeat("a", 64), PublicLabel: "隔离审核夹具", PublicURL: new("https://docs.example.test/public")}}
	g.Reviews = []contract.GuideReview{}
	for _, field := range []contract.GuideField{"scene_ids", "materials", "excluded_materials", "power_source", "nail_type", "fastener_length_mm", "required_requirements"} {
		sha, err := content.FactSHA256(g, field)
		if err != nil {
			t.Fatal(err)
		}
		g.Reviews = append(g.Reviews, contract.GuideReview{Field: field, FactSHA256: sha, SourceSha256s: []string{g.Sources[0].SHA256}, ReviewedBy: "private_reviewer", ReviewedAt: "2026-11-06T00:00:00Z"})
	}
	return g
}
func (f *fixture) load(t *testing.T, id string) *release.Handle {
	t.Helper()
	snapshot := f.snapshot
	snapshot.ReleaseID = id
	b, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err = contract.ValidateJSON("catalog-snapshot.schema.json#/$defs/PublishableSnapshot", b); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(f.dir, "artifacts", id)
	if err = os.MkdirAll(filepath.Join(dir, "site"), 0700); err != nil {
		t.Fatal(err)
	}
	resources := []contract.ManifestResource{}
	seen := map[string]bool{}
	for _, product := range snapshot.Products {
		if product.Visibility != "listed" {
			continue
		}
		for _, image := range product.Images {
			ext := image.Format
			if ext == "jpeg" {
				ext = "jpg"
			}
			name := "media/" + image.SHA256 + "." + ext
			if seen[name] {
				continue
			}
			data, err := os.ReadFile(filepath.Join("../../..", image.Path))
			if err != nil {
				t.Fatal(err)
			}
			if err = os.MkdirAll(filepath.Join(dir, "site", "media"), 0700); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(dir, "site", name), data, 0600); err != nil {
				t.Fatal(err)
			}
			resources = append(resources, contract.ManifestResource{Path: name, SHA256: release.Hash(data), Bytes: int64(len(data))})
			seen[name] = true
		}
	}
	manifest := contract.Manifest{SchemaVersion: 1, CandidateID: id, WorkspaceRevision: snapshot.WorkspaceRevision, CodeVersion: "unit-fixture", SnapshotSHA256: release.Hash(b), Routes: []string{"index.html"}, Resources: resources}
	mb, _ := json.Marshal(manifest)
	for name, data := range map[string][]byte{"catalog.json": b, "manifest.json": mb, "site/index.html": []byte(`<meta name="release-id" content="` + id + `">unit fixture`)} {
		if err = os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	h, err := release.Load(f.dir, id)
	if err != nil {
		t.Fatal(err)
	}
	return h
}
func (f *fixture) activate(t *testing.T, h *release.Handle, op string) {
	t.Helper()
	p, err := f.releases.Prepare(h, op)
	if err != nil {
		t.Fatal(err)
	}
	f.gate.Lock()
	_, err = f.releases.ActivateLocked(context.Background(), p)
	f.gate.Unlock()
	if err != nil {
		t.Fatal(err)
	}
}
func code(t *testing.T, err error, want int) {
	t.Helper()
	var e *contract.BusinessError
	if !errors.As(err, &e) || e.HTTPStatus != want {
		t.Fatalf("want %d got %v", want, err)
	}
}
func TestUnreviewedBrowseExplicitUnknownAndStableCursor(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	page, err := f.service.Search(ctx, f.h, contract.CatalogQuery{Limit: new(int64(1))})
	if err != nil {
		t.Fatal(err)
	}
	if page.Items[0].ProductID != "p_a" || page.NextCursor == nil {
		t.Fatal("stable tie-order/page")
	}
	second, err := f.service.Search(ctx, f.h, contract.CatalogQuery{Limit: new(int64(1)), Cursor: page.NextCursor})
	if err != nil || second.Items[0].ProductID != "p_b" || second.NextCursor != nil {
		t.Fatalf("second page: %+v %v", second, err)
	}
	if second.Items[0].MatchLevel != "needs_confirmation" {
		t.Fatal("unreviewed browse presented as matched")
	}
	_, err = f.service.Search(ctx, f.h, contract.CatalogQuery{Limit: new(int64(1)), Cursor: page.NextCursor, Category: new(contract.CategoryID("steel"))})
	code(t, err, 409)
	keyword, err := f.service.Search(ctx, f.h, contract.CatalogQuery{Q: new("u_n i-t_b")})
	if err != nil || len(keyword.Items) != 1 || keyword.Items[0].ProductID != "p_b" {
		t.Fatalf("unreviewed keyword: %+v %v", keyword, err)
	}
	for _, tc := range []struct {
		include bool
		ids     []string
	}{{false, []string{"p_a"}}, {true, []string{"p_a", "p_b"}}} {
		result, e := f.service.Search(ctx, f.h, contract.CatalogQuery{Material: new("木材"), IncludeUnknown: new(tc.include)})
		if e != nil {
			t.Fatal(e)
		}
		ids := []string{}
		for _, p := range result.Items {
			ids = append(ids, p.ProductID)
		}
		if !slices.Equal(ids, tc.ids) {
			t.Fatalf("unknown filter: %v", ids)
		}
	}
	result, err := f.service.Search(ctx, f.h, contract.CatalogQuery{Material: new("钢板"), IncludeUnknown: new(true)})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range result.Items {
		if p.ProductID == "p_a" {
			t.Fatal("include_unknown bypassed explicit exclusion")
		}
	}
	for _, length := range []float64{10, 30} {
		result, err = f.service.Search(ctx, f.h, contract.CatalogQuery{FastenerLengthMM: new(length)})
		if err != nil || len(result.Items) != 1 || result.Items[0].ProductID != "p_a" {
			t.Fatalf("boundary %g: %+v %v", length, result, err)
		}
	}
}
func TestPublicProjectionHasNoPrivateOrUnreviewedData(t *testing.T) {
	f := setup(t)
	out, err := f.service.Export(context.Background(), f.h)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if err = contract.ValidateJSON("public-catalog.schema.json#/$defs/PublicExport", b); err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"PRIVATE_UNREVIEWED", "PRIVATE_LIMIT", "private_archive", "private_reviewer", "private_source", "reviewed_by", "fact_sha256", "workspace_revision", "p_hidden", "snapshot_sha256", "\"url\":\"https://item"} {
		if strings.Contains(string(b), private) {
			t.Fatal("private projection leak: " + private)
		}
	}
	// A stale fact digest cannot grant a public value, even in a frozen input.
	f.snapshot.Products[2].GuideProfile.Materials = []string{"CHANGED_PRIVATE"}
	next := f.load(t, "unit_stale_review")
	f.activate(t, next, "operation_stale_review")
	p, err := f.service.Get(context.Background(), next, "p_a")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.GuideProfile.Materials) != 0 || !slices.Contains(p.GuideProfile.UnknownFields, "materials") {
		t.Fatal("stale review published changed fact")
	}
}
func TestHTTPCurrentAuthorityAndPurchaseOwnership(t *testing.T) {
	f := setup(t)
	mux := http.NewServeMux()
	httpapi.RegisterCatalog(mux, f.releases, f.service)
	httpapi.RegisterPurchase(mux, f.releases, f.service, "tel:123")
	request := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Cookie", "yuanfa_session=admin; yuanfa_preview=private")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	for path, want := range map[string]int{"/api/catalog/products/p_hidden": 404, "/api/catalog/products/p_a/purchase-options": 422, "/api/catalog/products/p_a/purchase-options?release_id=unit_A": 200, "/go/products/p_a/o_a?release_id=unit_A": 303, "/go/products/p_b/o_a?release_id=unit_A": 404, "/go/products/p_a/o_disabled?release_id=unit_A": 404, "/go/products/p_a/o_bad?release_id=unit_A": 404, "/go/products/p_a/o_a?release_id=unit_A&url=https://evil.test": 422, "/api/catalog/products?limit=101": 422, "/api/catalog/products?include_unknown=1": 422, "/api/catalog/products?fastener_length_mm=NaN": 422, "/api/catalog/products?limit=1&limit=2": 422} {
		w := request(path)
		if w.Code != want {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("cache bypass")
		}
		if want != 303 && w.Header().Get("Location") != "" {
			t.Fatal("rejected target redirected")
		}
	}
	if got := request("/go/products/p_a/o_a?release_id=unit_A").Header().Get("Location"); got != "https://item.example.test/product/a" {
		t.Fatal("wrong target")
	}
	untrusted := catalog.New(f.releases, []string{"another.example.test"})
	_, err := untrusted.Destination(context.Background(), f.h, "p_a", "o_a")
	code(t, err, 404)
	preview := f.load(t, "unit_preview")
	_, err = f.service.Get(context.Background(), preview, "p_a")
	code(t, err, 409)
	next := f.load(t, "unit_B")
	f.activate(t, next, "operation_B")
	_, err = f.service.Get(context.Background(), f.h, "p_a")
	code(t, err, 409)
	stale := request("/go/products/p_a/o_a?release_id=unit_A")
	if stale.Code != 409 || !strings.Contains(stale.Body.String(), "重新查看") || stale.Header().Get("Location") != "" {
		t.Fatal("old link silently redirected")
	}
}
