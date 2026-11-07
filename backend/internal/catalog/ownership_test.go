package catalog_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"yuanfa-official/backend/internal/content"
	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/httpapi"
	"yuanfa-official/backend/internal/matching"
	"yuanfa-official/backend/internal/release"
)

// Populate every public nested content family with real frozen inputs, including
// an actual manifest-authorized PNG and independently reviewed source facts.
func ownershipFixture(t *testing.T) *fixture {
	t.Helper()
	f := setup(t)
	p := &f.snapshot.Products[2]
	series := &f.snapshot.LegacySeries[0]
	series.ProductIDs = []string{p.ID}
	p.LegacySeriesIDs = []string{series.ID}
	p.Applications = []string{"木材钉合"}
	p.Features = []string{"夹具特点"}
	p.Notes = []string{"夹具说明"}
	p.SceneIDs = []contract.SceneID{"home-woodworking"}
	p.NailType = new(contract.NailType("brad"))
	imagePath := "tests/fixtures/media/valid.png"
	data, err := os.ReadFile("../../../" + imagePath)
	if err != nil {
		t.Fatal(err)
	}
	dimensions, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	sha := release.Hash(data)
	p.Images = []contract.SnapshotProductImage{{MediaID: "m_" + sha, SHA256: sha, Format: "png", Bytes: int64(len(data)), Width: int64(dimensions.Width), Height: int64(dimensions.Height), Path: imagePath, Label: "真实夹具图", Description: "公开图片说明"}}
	p.CoverMediaID = new(p.Images[0].MediaID)
	g := &p.GuideProfile
	g.Substrates = []string{"木方"}
	g.ExcludedSubstrates = []string{"混凝土"}
	g.PowerSource = new(contract.PowerSource("manual"))
	g.LoadBearing = new(false)
	g.LongNose = new(true)
	g.Blower = new(false)
	g.Limitations = []string{"夹具限制"}
	g.ConsultationNotes = []string{"夹具咨询提示"}
	g.RequiredRequirements = []contract.RequirementField{"material", "nail_type", "fastener_length_mm"}
	// Exercise a null source URL alongside an independently owned non-null URL.
	g.Sources = append(g.Sources, contract.GuideSource{ID: "private_label", Kind: "document", Reference: "private_label_archive", SHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", PublicLabel: "仅公开标签", PublicURL: nil})
	g.Reviews = []contract.GuideReview{}
	for _, field := range []contract.GuideField{"scene_ids", "materials", "substrates", "excluded_materials", "excluded_substrates", "power_source", "nail_type", "fastener_length_mm", "load_bearing", "long_nose", "blower", "limitations", "consultation_notes", "required_requirements"} {
		factSHA, err := content.FactSHA256(*g, field)
		if err != nil {
			t.Fatal(err)
		}
		g.Reviews = append(g.Reviews, contract.GuideReview{Field: field, FactSHA256: factSHA, SourceSha256s: []string{g.Sources[0].SHA256, g.Sources[1].SHA256}, ReviewedBy: "private_reviewer", ReviewedAt: "2026-11-06T00:00:00Z"})
	}
	f.h = f.load(t, "owned_release")
	f.activate(t, f.h, "owned_operation")
	return f
}

func mutateSlice[T any](values *[]T, replacement T) {
	if len(*values) == 0 {
		*values = append(*values, replacement)
	} else {
		(*values)[0] = replacement
	}
}
func mutateFacts(facts []contract.MatchedFact) {
	for i := range facts {
		fact := &facts[i]
		fact.Field = "limitations"
		fact.Text = "consumer changed fact"
		for j := range fact.Sources {
			source := &fact.Sources[j]
			source.PublicLabel = "consumer source"
			if source.PublicURL != nil {
				*source.PublicURL = "https://unmapped.invalid/consumer-source"
			}
		}
		if len(fact.Sources) > 0 {
			fact.Sources[0].PublicURL = new("https://unmapped.invalid/replaced-pointer")
		}
	}
}
func mutateProduct(p *contract.PublicProduct) {
	p.Name = "consumer product"
	mutateSlice(&p.LegacySeriesIDs, "consumer-series")
	mutateSlice(&p.Applications, "consumer application")
	mutateSlice(&p.Features, "consumer feature")
	mutateSlice(&p.Notes, "consumer note")
	mutateSlice(&p.SceneIDs, contract.SceneID("wall-mesh"))
	if p.NailType != nil {
		*p.NailType = "steel-nail"
	}
	for i := range p.Images {
		p.Images[i] = contract.PublicImage{URL: "https://unmapped.invalid/image", Label: "consumer image", Description: "consumer image description", Width: 999, Height: 999}
	}
	g := &p.GuideProfile
	mutateSlice(&g.SceneIDs, contract.SceneID("wall-mesh"))
	mutateSlice(&g.Materials, "CONSUMER_INJECTED_MATERIAL")
	mutateSlice(&g.Substrates, "consumer substrate")
	mutateSlice(&g.ExcludedMaterials, "consumer exclusion")
	mutateSlice(&g.ExcludedSubstrates, "consumer substrate exclusion")
	if g.PowerSource != nil {
		*g.PowerSource = "gas"
	}
	if g.NailType != nil {
		*g.NailType = "steel-nail"
	}
	if g.FastenerLengthMM != nil {
		g.FastenerLengthMM.Min = 1
		g.FastenerLengthMM.Max = 999
	}
	if g.LoadBearing != nil {
		*g.LoadBearing = !*g.LoadBearing
	}
	if g.LongNose != nil {
		*g.LongNose = !*g.LongNose
	}
	if g.Blower != nil {
		*g.Blower = !*g.Blower
	}
	mutateSlice(&g.Limitations, "consumer limitation")
	mutateSlice(&g.ConsultationNotes, "consumer consultation")
	mutateSlice(&g.RequiredRequirements, contract.RequirementField("blower"))
	mutateSlice(&g.UnknownFields, contract.GuideField("materials"))
	for i := range g.Sources {
		source := &g.Sources[i]
		source.Field = "limitations"
		source.PublicLabel = "consumer reviewed source"
		if source.PublicURL != nil {
			*source.PublicURL = "https://unmapped.invalid/reviewed-source"
		}
	}
	mutateFacts(p.MatchedFacts)
	mutateSlice(&p.MissingFields, contract.MissingField("blower"))
	mutateOptions(p.PurchaseOptions)
}
func mutateOptions(options []contract.PublicPurchaseOption) {
	for i := range options {
		options[i] = contract.PublicPurchaseOption{PurchaseOptionID: "consumer-option", Platform: "consumer platform", StoreName: "consumer store", MatchScope: "listing_with_variants", SkuHint: "consumer sku", DestinationHost: "unmapped.invalid", HandoffURL: "https://unmapped.invalid/consumer-target"}
	}
}
func encoded(t *testing.T, value any) []byte {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestReturnedCatalogDTOsOwnNestedData(t *testing.T) {
	for _, path := range []string{"Get", "Search", "Export", "PurchaseOptions", "EvaluateMatchedFactSource"} {
		t.Run(path, func(t *testing.T) {
			f := ownershipFixture(t)
			ctx := context.Background()
			mux := http.NewServeMux()
			httpapi.RegisterCatalog(mux, f.releases, f.service)
			httpapi.RegisterPurchase(mux, f.releases, f.service, "tel:123")
			public := func(path string) []byte {
				t.Helper()
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
				if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" {
					t.Fatalf("public response %s: %d %s", path, rec.Code, rec.Body.String())
				}
				if path == "/api/catalog/export" {
					out, err := contract.Decode[contract.PublicExport]("public-catalog.schema.json#/$defs/PublicExport", rec.Body.Bytes())
					if err != nil {
						t.Fatal(err)
					}
					out.GeneratedAt = ""
					return encoded(t, out)
				}
				return bytes.Clone(rec.Body.Bytes())
			}
			routes := []string{"/api/catalog/products/p_a", "/api/catalog/products/p_b", "/api/catalog/products?limit=1", "/api/catalog/products?q=UNIT-A&material=%E6%9C%A8%E6%9D%90&fastener_length_mm=20", "/api/catalog/products?q=UNIT-A&material=CONSUMER_INJECTED_MATERIAL&fastener_length_mm=500", "/api/catalog/products/p_a/purchase-options?release_id=owned_release", "/api/catalog/export"}
			baseline := make(map[string][]byte, len(routes))
			for _, route := range routes {
				baseline[route] = public(route)
			}
			retained, err := f.service.Get(ctx, f.h, "p_a")
			if err != nil {
				t.Fatal(err)
			}
			retainedBytes := encoded(t, retained)
			privateBytes := encoded(t, f.h.Catalog())
			switch path {
			case "Get":
				for _, id := range []string{"p_a", "p_b"} {
					out, err := f.service.Get(ctx, f.h, id)
					if err != nil {
						t.Fatal(err)
					}
					mutateProduct(&out)
				}
			case "Search":
				out, err := f.service.Search(ctx, f.h, contract.CatalogQuery{Limit: new(int64(1)), Material: new("木材"), IncludeUnknown: new(true)})
				if err != nil {
					t.Fatal(err)
				}
				if len(out.Items) != 1 || out.Items[0].ProductID != "p_a" || out.NextCursor == nil {
					t.Fatal("first owned page not returned")
				}
				next, err := f.service.Search(ctx, f.h, contract.CatalogQuery{Limit: new(int64(1)), Material: new("木材"), IncludeUnknown: new(true), Cursor: out.NextCursor})
				if err != nil {
					t.Fatal(err)
				}
				if len(next.Items) != 1 || next.Items[0].ProductID != "p_b" {
					t.Fatal("second owned page not returned")
				}
				mutateProduct(&out.Items[0])
				mutateProduct(&next.Items[0])
				*out.NextCursor = "consumer cursor"
			case "Export":
				out, err := f.service.Export(ctx, f.h)
				if err != nil {
					t.Fatal(err)
				}
				for i := range out.Products {
					mutateProduct(&out.Products[i])
				}
				for i := range out.Categories {
					category := &out.Categories[i]
					category.Name = "consumer category"
					mutateSlice(&category.SceneIDs, contract.SceneID("wall-mesh"))
					if category.Nail != nil {
						*category.Nail = "steel-nail"
					}
				}
				for i := range out.Scenes {
					out.Scenes[i].Name = "consumer scene"
				}
				for i := range out.NailTypes {
					out.NailTypes[i].Name = "consumer nail"
				}
			case "PurchaseOptions":
				out, err := f.service.PurchaseOptions(ctx, f.h, "p_a")
				if err != nil {
					t.Fatal(err)
				}
				mutateOptions(out.PurchaseOptions)
			case "EvaluateMatchedFactSource":
				profile, err := f.service.Get(ctx, f.h, "p_a")
				if err != nil {
					t.Fatal(err)
				}
				profileBytes := encoded(t, profile.GuideProfile)
				decision := matching.Evaluate(contract.GuideRequirements{SceneIDs: []contract.SceneID{}, Material: new("木材"), NailType: new(contract.NailType("brad")), FastenerLengthMM: new(20.0)}, profile.GuideProfile)
				if decision.MatchLevel != "matched" {
					t.Fatalf("reviewed fixture failed: %+v", decision)
				}
				mutateFacts(decision.MatchedFacts)
				if !bytes.Equal(encoded(t, profile.GuideProfile), profileBytes) {
					t.Fatal("Evaluate output changed its input profile")
				}
			}
			if !bytes.Equal(encoded(t, retained), retainedBytes) {
				t.Fatal("another caller's retained product changed")
			}
			if !bytes.Equal(encoded(t, f.h.Catalog()), privateBytes) {
				t.Fatal("private frozen release changed")
			}
			for _, route := range routes {
				if !bytes.Equal(public(route), baseline[route]) {
					t.Errorf("consumer mutation changed subsequent HTTP %s", route)
				}
			}
			redirect := httptest.NewRecorder()
			mux.ServeHTTP(redirect, httptest.NewRequest("GET", "/go/products/p_a/o_a?release_id=owned_release", nil))
			if redirect.Code != 303 || redirect.Header().Get("Location") != "https://item.example.test/product/a" {
				t.Fatal("consumer mutation changed server purchase mapping")
			}
		})
	}
}
