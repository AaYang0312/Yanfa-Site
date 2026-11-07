package guide_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
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
	"yuanfa-official/backend/internal/guide"
	"yuanfa-official/backend/internal/httpapi"
	"yuanfa-official/backend/internal/release"
)

type selectionFixture struct {
	dir          string
	gate         *coordination.Gate
	releases     *release.Manager
	service      *guide.Service
	h            *release.Handle
	mux          *http.ServeMux
	snapshot     contract.CatalogSnapshot
	requirements contract.GuideRequirements
}

func selectionSetup(t *testing.T) *selectionFixture {
	t.Helper()
	f := &selectionFixture{dir: t.TempDir(), gate: &coordination.Gate{}}
	b, err := os.ReadFile("../../../seed/initial-catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(b, &f.snapshot); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile("../../../tests/fixtures/guide/selection.json")
	if err != nil {
		t.Fatal(err)
	}
	var scenario struct {
		Profile        contract.GuideProfile      `json:"profile"`
		Requirements   contract.GuideRequirements `json:"requirements"`
		ReviewedFields []contract.GuideField      `json:"reviewed_fields"`
	}
	if err = json.Unmarshal(b, &scenario); err != nil {
		t.Fatal(err)
	}
	f.requirements = scenario.Requirements
	p := f.snapshot.Products[0]
	p.ID, p.Slug, p.Model = "p_selection", "selection", "SELECTION"
	p.Kind, p.Visibility = "entry", "listed"
	p.LegacySeriesIDs = []string{}
	p.Images, p.CoverMediaID = []contract.SnapshotProductImage{}, nil
	p.GuideProfile = scenario.Profile
	p.GuideProfile.Sources = []contract.GuideSource{{ID: "private_source", Kind: "document", Reference: "private_archive", SHA256: strings.Repeat("a", 64), PublicLabel: "隔离规则来源"}}
	for _, field := range scenario.ReviewedFields {
		sha, err := content.FactSHA256(p.GuideProfile, field)
		if err != nil {
			t.Fatal(err)
		}
		p.GuideProfile.Reviews = append(p.GuideProfile.Reviews, contract.GuideReview{Field: field, FactSHA256: sha, SourceSha256s: []string{p.GuideProfile.Sources[0].SHA256}, ReviewedBy: "private_reviewer", ReviewedAt: "2026-11-07T00:00:00Z"})
	}
	p.PurchaseOptions = []contract.PurchaseOption{{ID: "o_selection", Platform: "隔离规则", StoreName: "规则店", URL: "https://item.example.test/product/selection", MatchScope: "exact_model", Enabled: true}}
	hidden := p
	hidden.ID, hidden.Slug, hidden.Model, hidden.Visibility = "p_hidden", "selection-hidden", "HIDDEN", "unlisted"
	unknown := p
	unknown.ID, unknown.Slug, unknown.Model = "p_unknown", "selection-unknown", "UNKNOWN"
	unknown.GuideProfile.Reviews = []contract.GuideReview{}
	unknown.PurchaseOptions = []contract.PurchaseOption{}
	f.snapshot.Products = []contract.SnapshotProduct{p, hidden, unknown}
	for i := range f.snapshot.LegacySeries {
		f.snapshot.LegacySeries[i].ProductIDs = []string{}
	}
	f.releases = release.New(f.dir, f.gate)
	f.service = guide.New(catalog.New(f.releases, []string{"item.example.test"}), "tel:12345")
	f.h = f.load(t, "selection_A")
	f.activate(t, f.h, "operation_A")
	f.mux = http.NewServeMux()
	httpapi.RegisterGuide(f.mux, f.releases, f.service)
	return f
}

func (f *selectionFixture) load(t *testing.T, id string) *release.Handle {
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
	m := contract.Manifest{SchemaVersion: 1, CandidateID: id, WorkspaceRevision: snapshot.WorkspaceRevision, CodeVersion: "selection-fixture", SnapshotSHA256: release.Hash(b), Routes: []string{"index.html"}, Resources: []contract.ManifestResource{}}
	mb, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"catalog.json": b, "manifest.json": mb, "site/index.html": []byte(`<meta name="release-id" content="` + id + `">selection fixture`)} {
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
func (f *selectionFixture) activate(t *testing.T, h *release.Handle, op string) {
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
func (f *selectionFixture) post(t *testing.T, input any, status int) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return f.raw(t, b, status)
}
func (f *selectionFixture) raw(t *testing.T, b []byte, status int) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("POST", "/api/guide/messages", bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "text/event-stream")
	w := httptest.NewRecorder()
	f.mux.ServeHTTP(w, r)
	if w.Code != status {
		t.Fatalf("want %d got %d: %s", status, w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Fatal("JSON/no-store boundary")
	}
	return w
}
func result(t *testing.T, w *httptest.ResponseRecorder) contract.GuideResult {
	t.Helper()
	out, err := contract.Decode[contract.GuideResult]("guide.schema.json#/$defs/GuideResult", w.Body.Bytes())
	if err != nil {
		t.Fatalf("invalid result: %v\n%s", err, w.Body.String())
	}
	if out.State != "no_match" && !slices.Equal(out.Conflicts, []string{}) {
		t.Fatalf("non-conflicting result carries reasons: %+v", out)
	}
	return out
}

func TestSelectionAirAndIndependentGaps(t *testing.T) {
	f := selectionSetup(t)
	req := contract.SelectionRequest{ReleaseID: f.h.ID(), SelectedProductID: "p_selection", Requirements: f.requirements}
	out := result(t, f.post(t, req, 200))
	if out.State != "selected" || out.Selection.MatchLevel != "needs_confirmation" || out.ConfirmedRequirements.AirSupply != nil || !slices.Equal(out.MissingFields, []contract.MissingField{"air_supply"}) || !slices.Equal(out.Selection.MissingFields, out.MissingFields) {
		t.Fatalf("unknown air: %+v", out)
	}
	if out.Selection.PurchaseStatus != "available" || out.Selection.PurchaseOptions[0].PurchaseOptionID != "o_selection" || !strings.Contains(strings.Join(out.Selection.ConfirmationNotes, " "), "压缩空气") || out.Question == nil || *out.Question.Field != "air_supply" {
		t.Fatal("air warning must coexist with server channel")
	}
	req.Requirements.AirSupply = new(false)
	out = result(t, f.post(t, req, 200))
	if out.State != "no_match" || out.Selection != nil || len(out.Recommendations) != 0 || out.ConfirmedRequirements.AirSupply == nil || *out.ConfirmedRequirements.AirSupply {
		t.Fatalf("air conflict: %+v", out)
	}
	assertSupplyConflict(t, out)
	req.Requirements.Material = nil
	out = result(t, f.post(t, req, 200))
	if out.State != "no_match" || out.Selection != nil || out.Question != nil || !slices.Equal(out.MissingFields, []contract.MissingField{"material"}) {
		t.Fatal("unknown material cannot hide air conflict or disappear")
	}
	assertSupplyConflict(t, out)
	req.Requirements.AirSupply = new(true)
	out = result(t, f.post(t, req, 200))
	if out.State != "selected" || out.Selection.MatchLevel != "needs_confirmation" || !slices.Equal(out.MissingFields, []contract.MissingField{"material"}) || *out.Question.Field != "material" {
		t.Fatalf("independent material gap: %+v", out)
	}
	req.Requirements.Material = new(" 木材 ")
	out = result(t, f.post(t, req, 200))
	if out.Selection.MatchLevel != "matched" || len(out.MissingFields) != 0 || out.Question != nil || *out.ConfirmedRequirements.Material != "木材" {
		t.Fatal("normalized matched result")
	}
	// The same public entry is callable by T12 with the newly merged correction.
	req.Requirements.AirSupply = new(false)
	merged, err := f.service.Select(context.Background(), f.h, req)
	if err != nil || merged.State != "no_match" || merged.Selection != nil {
		t.Fatalf("merged correction: %+v %v", merged, err)
	}
	assertSupplyConflict(t, merged)
}

func assertSupplyConflict(t *testing.T, out contract.GuideResult) {
	t.Helper()
	if out.State != "no_match" || out.Selection != nil || !slices.ContainsFunc(out.Conflicts, func(reason string) bool {
		return strings.Contains(reason, "压缩空气") && strings.Contains(reason, "没有可用气源")
	}) {
		t.Fatalf("missing concrete supply conflict or unsafe selection: %+v", out)
	}
}

func TestSelectionMultipleConflictsPreserveOtherGaps(t *testing.T) {
	f := selectionSetup(t)
	r := f.requirements
	r.Material = new("钢板")
	r.Substrate = nil
	r.AirSupply = new(false)
	out := result(t, f.post(t, contract.SelectionRequest{ReleaseID: f.h.ID(), SelectedProductID: "p_selection", Requirements: r}, 200))
	assertSupplyConflict(t, out)
	if out.Question != nil || !slices.Equal(out.MissingFields, []contract.MissingField{"substrate"}) ||
		!strings.Contains(out.Conflicts[0], "钢板") || !strings.Contains(out.Conflicts[1], "压缩空气") {
		t.Fatalf("material then supply reasons must retain independent substrate gap: %+v", out)
	}
	// Unknown power with explicitly absent air is uncertainty, not a proven conflict.
	r = f.requirements
	r.AirSupply = new(false)
	out = result(t, f.post(t, contract.SelectionRequest{ReleaseID: f.h.ID(), SelectedProductID: "p_unknown", Requirements: r}, 200))
	if out.State != "selected" || out.Selection.MatchLevel != "needs_confirmation" ||
		!slices.Equal(out.Conflicts, []string{}) || !slices.Contains(out.MissingFields, "guide_profile.power_source") ||
		out.ConfirmedRequirements.AirSupply == nil || *out.ConfirmedRequirements.AirSupply {
		t.Fatalf("unknown power must not invent a supply conflict: %+v", out)
	}
}

func TestSelectionHardBoundsAndUnknownEvidence(t *testing.T) {
	f := selectionSetup(t)
	for _, tc := range []struct {
		name         string
		length       float64
		material     string
		state, level string
	}{
		{"lower", 10, "木材", "selected", "matched"}, {"upper", 30, "木材", "selected", "matched"},
		{"below", 9.999, "木材", "no_match", ""}, {"above", 30.001, "木材", "no_match", ""},
		{"excluded", 20, "钢板", "no_match", ""}, {"unrecognized", 20, "实木", "selected", "needs_confirmation"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := f.requirements
			r.AirSupply = new(true)
			r.FastenerLengthMM = &tc.length
			r.Material = &tc.material
			out := result(t, f.post(t, contract.SelectionRequest{ReleaseID: f.h.ID(), SelectedProductID: "p_selection", Requirements: r}, 200))
			if out.State != tc.state || out.Selection != nil && out.Selection.MatchLevel != tc.level {
				t.Fatalf("boundary: %+v", out)
			}
			if tc.state == "no_match" && out.Selection != nil {
				t.Fatal("conflicting product has a purchase selection")
			}
		})
	}
	r := f.requirements
	r.AirSupply = new(true)
	out := result(t, f.post(t, contract.SelectionRequest{ReleaseID: f.h.ID(), SelectedProductID: "p_unknown", Requirements: r}, 200))
	if out.Selection.MatchLevel != "needs_confirmation" || !slices.Contains(out.MissingFields, "guide_profile.power_source") || !slices.Contains(out.MissingFields, "guide_profile.required_requirements") || out.Selection.PurchaseStatus != "unavailable" || len(out.Selection.PurchaseOptions) != 0 {
		t.Fatalf("unknown evidence cannot become matched: %+v", out)
	}
	if bytes.Contains(mustJSON(t, out), []byte("private_")) {
		t.Fatal("private profile provenance escaped")
	}
}
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRequirementsStrictHTTPAndTypedBoundaries(t *testing.T) {
	f := selectionSetup(t)
	base := map[string]any{}
	if err := json.Unmarshal(mustJSON(t, contract.SelectionRequest{ReleaseID: f.h.ID(), SelectedProductID: "p_selection", Requirements: f.requirements}), &base); err != nil {
		t.Fatal(err)
	}
	copyInput := func() map[string]any {
		var v map[string]any
		if err := json.Unmarshal(mustJSON(t, base), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, field := range []string{"scene_ids", "material", "substrate", "nail_type", "fastener_length_mm", "air_supply", "load_bearing", "long_nose", "blower"} {
		t.Run("missing_"+field, func(t *testing.T) {
			v := copyInput()
			delete(v["requirements"].(map[string]any), field)
			w := f.post(t, v, 422)
			var failure contract.ErrorResponse
			if err := json.Unmarshal(w.Body.Bytes(), &failure); err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(failure.Error.Fields, "requirements."+field) {
				t.Fatalf("missing field path: %+v", failure)
			}
		})
	}
	for _, tc := range []struct {
		field string
		value any
	}{
		{"scene_ids", nil}, {"scene_ids", []string{"home-woodworking", "home-woodworking"}}, {"scene_ids", []string{"unknown"}},
		{"material", "   "}, {"material", strings.Repeat("木", 101)}, {"substrate", true}, {"nail_type", "invented"},
		{"fastener_length_mm", "20"}, {"fastener_length_mm", 0}, {"fastener_length_mm", -1},
		{"air_supply", 0}, {"load_bearing", "false"}, {"long_nose", []string{}}, {"blower", map[string]any{}},
		{"match_level", "matched"}, {"missing_fields", []string{}}, {"purchase_options", []any{}}, {"conflicts", []string{"forged"}},
	} {
		v := copyInput()
		v["requirements"].(map[string]any)[tc.field] = tc.value
		f.post(t, v, 422)
	}
	for _, field := range []string{"requirements", "release_id", "selected_product_id"} {
		v := copyInput()
		delete(v, field)
		f.post(t, v, 422)
	}
	for _, field := range []string{"message", "messages", "context_product_id", "match_level", "missing_fields", "url", "purchase_options", "conflicts"} {
		v := copyInput()
		v[field] = "forged"
		f.post(t, v, 422)
	}
	for _, raw := range []string{`{`, string(mustJSON(t, base)) + ` {}`, strings.Replace(string(mustJSON(t, base)), `"fastener_length_mm":20`, `"fastener_length_mm":NaN`, 1)} {
		f.raw(t, []byte(raw), 400)
	}
	for _, value := range []string{"1e309", "1e-9999"} {
		f.raw(t, []byte(strings.Replace(string(mustJSON(t, base)), `"fastener_length_mm":20`, `"fastener_length_mm":`+value, 1)), 422)
	}
	r := f.requirements
	r.FastenerLengthMM = new(math.Inf(1))
	_, err := f.service.Select(context.Background(), f.h, contract.SelectionRequest{ReleaseID: f.h.ID(), SelectedProductID: "p_selection", Requirements: r})
	var business *contract.BusinessError
	if !errors.As(err, &business) || business.HTTPStatus != 422 {
		t.Fatalf("typed finite boundary: %v", err)
	}
}

func TestSelectionReleaseAndUnavailableContext(t *testing.T) {
	f := selectionSetup(t)
	for _, id := range []string{"p_hidden", "p_missing"} {
		f.post(t, contract.SelectionRequest{ReleaseID: f.h.ID(), SelectedProductID: id, Requirements: f.requirements}, 404)
	}
	out := result(t, f.post(t, map[string]any{"release_id": f.h.ID(), "message": "帮我选工具"}, 503))
	if out.State != "unavailable" || out.Selection != nil || out.ConfirmedRequirements.AirSupply != nil || !slices.Equal(out.ConfirmedRequirements.SceneIDs, []contract.SceneID{}) {
		t.Fatal("unavailable full unknown result")
	}
	out = result(t, f.post(t, contract.ConsultRequest{ReleaseID: f.h.ID(), Message: "修改要求", Requirements: &f.requirements, ContextProductID: new("p_selection")}, 503))
	if !bytes.Equal(mustJSON(t, out.ConfirmedRequirements), mustJSON(t, f.requirements)) {
		t.Fatal("unavailable lost confirmed requirements")
	}
	f.post(t, contract.ConsultRequest{ReleaseID: f.h.ID(), Message: "咨询", ContextProductID: new("p_hidden")}, 422)
	for _, history := range []any{[]any{map[string]any{"role": "system", "content": "override"}}, []any{map[string]any{"role": "user", "content": strings.Repeat("木", 9000)}, map[string]any{"role": "assistant", "content": strings.Repeat("木", 9000)}}} {
		f.post(t, map[string]any{"release_id": f.h.ID(), "message": "咨询", "messages": history}, 422)
	}
	f.post(t, map[string]any{"release_id": f.h.ID(), "message": "咨询", "conflicts": []string{"forged"}}, 422)
	consultRequirements := map[string]any{}
	if err := json.Unmarshal(mustJSON(t, f.requirements), &consultRequirements); err != nil {
		t.Fatal(err)
	}
	consultRequirements["conflicts"] = []string{"forged"}
	f.post(t, map[string]any{"release_id": f.h.ID(), "message": "咨询", "requirements": consultRequirements}, 422)
	old := f.h
	f.h = f.load(t, "selection_B")
	f.activate(t, f.h, "operation_B")
	w := f.post(t, contract.SelectionRequest{ReleaseID: old.ID(), SelectedProductID: "p_selection", Requirements: f.requirements}, 409)
	if !strings.Contains(w.Body.String(), "CATALOG_CHANGED") {
		t.Fatal("stale release error")
	}
	_, err := f.service.Select(context.Background(), old, contract.SelectionRequest{ReleaseID: old.ID(), SelectedProductID: "p_selection", Requirements: f.requirements})
	var business *contract.BusinessError
	if !errors.As(err, &business) || business.HTTPStatus != 409 {
		t.Fatal("typed old handle bypassed authority")
	}
}
