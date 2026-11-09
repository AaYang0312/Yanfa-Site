package guide_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"yuanfa-official/backend/internal/catalog"
	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/guide"
)

func TestPublishedDescriptionsIntroduceProductsWithoutFactReview(t *testing.T) {
	for _, reviewed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unknown", true: "reviewed"}[reviewed], func(t *testing.T) {
			f := selectionSetup(t)
			p := f.snapshot.Products[0]
			p.Summary = "便携工具的公开介绍"
			p.Applications = []string{"目录外型号的用途文字"}
			p.Features = []string{"便于收纳"}
			p.Notes = []string{"使用前阅读说明"}
			if !reviewed {
				p.GuideProfile.Reviews = []contract.GuideReview{}
			}
			f.snapshot.Products = []contract.SnapshotProduct{p}
			f.h = f.load(t, "introduction")
			f.activate(t, f.h, "introduction")
			f.requirements = contract.UnknownRequirements()
			// Unsaved changes must never enter search or the introduction.
			f.snapshot.Products[0].Summary = "尚未发布的文字"
			public := catalog.New(f.releases, nil)
			found, err := public.Search(context.Background(), f.h, contract.CatalogQuery{Q: new("目录外型号")})
			if err != nil || len(found.Items) != 1 || found.Items[0].Summary != p.Summary {
				t.Fatalf("published text search: %+v %v", found, err)
			}
			if !reviewed && (found.Items[0].GuideProfile.Materials == nil || len(found.Items[0].GuideProfile.Materials) != 0 || !slices.Contains(found.Items[0].GuideProfile.UnknownFields, "materials")) {
				t.Fatal("display text must not verify materials")
			}
			m := &consultModel{query: `{"q":"目录外型号"}`, proposal: proposal(t, f.requirements, []string{p.ID}, nil)}
			out := consult(t, f, m, "介绍一下这款产品")
			if out.State != "recommendations" || out.Question != nil || len(out.Recommendations) != 1 || out.Recommendations[0].MatchLevel != "needs_confirmation" {
				t.Fatalf("introduction forced a suitability questionnaire: %+v", out)
			}
			text := guide.ResultText(out)
			for _, value := range []string{p.Summary, p.Applications[0], p.Features[0], p.Notes[0]} {
				if !strings.Contains(text, value) {
					t.Fatalf("published description missing: %s", value)
				}
			}
			if strings.Contains(text, "尚未发布的文字") || strings.Contains(text, "private_archive") || strings.Contains(text, "guide_profile.") {
				t.Fatalf("internal or unpublished text leaked: %s", text)
			}
			if !reviewed && len(out.Recommendations[0].MatchedFacts) != 0 {
				t.Fatal("description was promoted to a verified fact")
			}
		})
	}
}

func TestIntroductionKeepsConditionalSuitabilityUnknown(t *testing.T) {
	f := selectionSetup(t)
	p := f.snapshot.Products[2]
	p.Summary = "适用于木材与木方，可承重，兼容所有型号"
	f.snapshot.Products = []contract.SnapshotProduct{p}
	f.h = f.load(t, "conditional_introduction")
	f.activate(t, f.h, "conditional_introduction")
	req := contract.UnknownRequirements()
	req.Material, req.Substrate = new("木材"), new("木方")
	req.NailType, req.FastenerLengthMM = new(contract.NailType("brad")), new(20.0)
	req.LoadBearing = new(true)
	f.requirements = req
	out := consult(t, f, &consultModel{proposal: proposal(t, req, []string{p.ID}, nil)}, "能用在木材和木方上承重吗？")
	if len(out.Recommendations) != 1 || out.Recommendations[0].MatchLevel != "needs_confirmation" || len(out.Recommendations[0].MatchedFacts) != 0 {
		t.Fatalf("ordinary claims became suitability evidence: %+v", out)
	}
	for _, field := range []contract.MissingField{"guide_profile.materials", "guide_profile.substrates", "guide_profile.nail_type", "guide_profile.fastener_length_mm", "guide_profile.load_bearing"} {
		if !slices.Contains(out.MissingFields, field) {
			t.Fatalf("lost unknown safety field %s", field)
		}
	}
	selected, err := f.service.Select(context.Background(), f.h, contract.SelectionRequest{ReleaseID: f.h.ID(), SelectedProductID: p.ID, Requirements: req})
	if err != nil || selected.Selection == nil || selected.Selection.MatchLevel != "needs_confirmation" || !strings.Contains(guide.ResultText(selected), p.Summary) {
		t.Fatalf("selection lost introduction or safety boundary: %+v %v", selected, err)
	}
	encoded, _ := json.Marshal(selected)
	if err = contract.ValidateJSON("guide.schema.json#/$defs/GuideResult", encoded); err != nil {
		t.Fatal(err)
	}
}

func TestIntroductionBoundsLongPublishedText(t *testing.T) {
	f := selectionSetup(t)
	f.snapshot.Products[0].Applications = []string{strings.Repeat("说明", 500), strings.Repeat("用途", 500)}
	f.h = f.load(t, "long_introduction")
	f.activate(t, f.h, "long_introduction")
	f.requirements = contract.UnknownRequirements()
	out := consult(t, f, &consultModel{proposal: proposal(t, f.requirements, []string{"p_selection"}, nil)}, "介绍一下")
	if !strings.Contains(guide.ResultText(out), "说明") || !strings.Contains(guide.ResultText(out), "详情") {
		t.Fatal("long introduction needs a bounded excerpt and detail reference")
	}
}
