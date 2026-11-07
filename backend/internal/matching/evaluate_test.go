package matching_test

import (
	"slices"
	"strconv"
	"testing"

	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/matching"
)

func profile() contract.ReviewedGuideProfile {
	return contract.ReviewedGuideProfile{SceneIDs: []contract.SceneID{"home-woodworking"}, Materials: []string{"木材"}, Substrates: []string{"木方"}, ExcludedMaterials: []string{"钢板"}, ExcludedSubstrates: []string{"混凝土"}, PowerSource: new(contract.PowerSource("pneumatic")), NailType: new(contract.NailType("brad")), FastenerLengthMM: &contract.LengthRange{Min: 10, Max: 30}, LoadBearing: new(false), LongNose: new(true), Blower: new(false), Limitations: []string{}, ConsultationNotes: []string{}, RequiredRequirements: []contract.RequirementField{"material", "substrate", "nail_type", "fastener_length_mm"}, Sources: []contract.ReviewedSource{}, UnknownFields: []contract.GuideField{}}
}
func requirements() contract.GuideRequirements {
	return contract.GuideRequirements{SceneIDs: []contract.SceneID{"home-woodworking"}, Material: new("木材"), Substrate: new("木方"), NailType: new(contract.NailType("brad")), FastenerLengthMM: new(20.0), AirSupply: new(true), LoadBearing: new(false), LongNose: new(false), Blower: new(false)}
}
func TestClosedLengthBoundsAndConflictPrecedence(t *testing.T) {
	for _, tc := range []struct {
		length float64
		want   string
	}{{9.999, "no_match"}, {10, "matched"}, {30, "matched"}, {30.001, "no_match"}} {
		t.Run(strconv.FormatFloat(tc.length, 'g', -1, 64), func(t *testing.T) {
			req := requirements()
			req.FastenerLengthMM = new(tc.length)
			got := matching.Evaluate(req, profile())
			if got.MatchLevel != tc.want {
				t.Fatalf("%g: %+v", tc.length, got)
			}
		})
	}
	req := requirements()
	req.Material = new("钢板")
	req.AirSupply = nil
	p := profile()
	p.UnknownFields = []contract.GuideField{"required_requirements"}
	got := matching.Evaluate(req, p)
	if got.MatchLevel != "no_match" || len(got.Conflicts) == 0 || !slices.Contains(got.MissingFields, "air_supply") {
		t.Fatalf("conflict lost to unknowns: %+v", got)
	}
}
func TestPneumaticAirAndOtherEvidenceRemainIndependent(t *testing.T) {
	req := requirements()
	req.AirSupply = nil
	got := matching.Evaluate(req, profile())
	if got.MatchLevel != "needs_confirmation" || !slices.Equal(got.MissingFields, []contract.MissingField{"air_supply"}) {
		t.Fatalf("unknown air: %+v", got)
	}
	req.AirSupply = new(false)
	got = matching.Evaluate(req, profile())
	if got.MatchLevel != "no_match" || len(got.Conflicts) == 0 {
		t.Fatalf("missing conflict: %+v", got)
	}
	req.AirSupply = new(true)
	req.Material = nil
	got = matching.Evaluate(req, profile())
	if got.MatchLevel != "needs_confirmation" || !slices.Contains(got.MissingFields, "material") || slices.Contains(got.MissingFields, "air_supply") {
		t.Fatalf("air erased material: %+v", got)
	}
	p := profile()
	p.PowerSource = nil
	p.UnknownFields = []contract.GuideField{"power_source"}
	got = matching.Evaluate(requirements(), p)
	if got.MatchLevel != "needs_confirmation" || !slices.Contains(got.MissingFields, "guide_profile.power_source") {
		t.Fatalf("unknown power inferred: %+v", got)
	}
}
func TestPolicyCannotBypassKnownConditionsAndFalseFeaturesAreOptional(t *testing.T) {
	p := profile()
	p.RequiredRequirements = []contract.RequirementField{}
	req := requirements()
	req.Blower = new(true)
	got := matching.Evaluate(req, p)
	if got.MatchLevel != "no_match" {
		t.Fatalf("empty policy bypassed true blower: %+v", got)
	}
	req.Blower = new(false)
	req.LongNose = new(false)
	got = matching.Evaluate(req, p)
	if got.MatchLevel != "matched" {
		t.Fatalf("false prohibited product features: %+v", got)
	}
	p.UnknownFields = []contract.GuideField{"required_requirements"}
	got = matching.Evaluate(req, p)
	if got.MatchLevel != "needs_confirmation" || !slices.Contains(got.MissingFields, "guide_profile.required_requirements") {
		t.Fatalf("unreviewed policy matched: %+v", got)
	}
}
func TestMaterialNamesAndMissingOrderAreDeterministic(t *testing.T) {
	p := profile()
	req := requirements()
	req.Material = new(" 木材 ")
	if got := matching.Evaluate(req, p); got.MatchLevel != "matched" {
		t.Fatalf("trim: %+v", got)
	}
	req.Material = new("实木")
	if got := matching.Evaluate(req, p); got.MatchLevel != "needs_confirmation" || !slices.Contains(got.MissingFields, "guide_profile.materials") {
		t.Fatalf("guessed synonym: %+v", got)
	}
	p.RequiredRequirements = []contract.RequirementField{"fastener_length_mm", "material", "scene_ids", "air_supply"}
	p.UnknownFields = []contract.GuideField{"power_source", "materials", "scene_ids", "fastener_length_mm", "excluded_materials"}
	p.PowerSource = nil
	got := matching.Evaluate(contract.UnknownRequirements(), p)
	want := []contract.MissingField{"scene_ids", "material", "fastener_length_mm", "air_supply", "guide_profile.excluded_materials", "guide_profile.fastener_length_mm", "guide_profile.materials", "guide_profile.power_source", "guide_profile.scene_ids"}
	if !slices.Equal(got.MissingFields, want) {
		t.Fatalf("order/dedup: got %v want %v", got.MissingFields, want)
	}
}
func TestUnrelatedLengthIsNotRequiredForRemoval(t *testing.T) {
	p := profile()
	p.PowerSource = new(contract.PowerSource("manual"))
	p.RequiredRequirements = []contract.RequirementField{}
	p.FastenerLengthMM = nil
	p.UnknownFields = []contract.GuideField{"fastener_length_mm"}
	got := matching.Evaluate(contract.UnknownRequirements(), p)
	if got.MatchLevel != "matched" {
		t.Fatalf("irrelevant specification forced: %+v", got)
	}
}
