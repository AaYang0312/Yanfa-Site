package contract

import (
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestSharedBoundaryFixtures(t *testing.T) {
	data, err := os.ReadFile("../../../tests/contracts/fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Name   string          `json:"name"`
		Schema string          `json:"schema"`
		Data   json.RawMessage `json:"data"`
		Valid  bool            `json:"valid"`
		Fields []string        `json:"fields"`
	}
	if err = json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			err := ValidateJSON(fixture.Schema, fixture.Data)
			if fixture.Valid {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var business *BusinessError
			if !errors.As(err, &business) {
				t.Fatalf("expected field error, got %v", err)
			}
			if business.HTTPStatus != 422 {
				t.Fatalf("status %d", business.HTTPStatus)
			}
			for _, field := range fixture.Fields {
				if !slices.ContainsFunc(business.Detail.Fields, func(path string) bool { return path == field || strings.HasPrefix(path, field+".") }) {
					t.Errorf("missing field %s in %v", field, business.Detail.Fields)
				}
			}
		})
	}
}

func TestTypedRequirementsPreserveUnknownAndNormalize(t *testing.T) {
	const data = `{"scene_ids":[],"material":" 木材 ","substrate":null,"nail_type":null,"fastener_length_mm":null,"air_supply":false,"load_bearing":null,"long_nose":null,"blower":null}`
	requirements, err := Decode[GuideRequirements]("guide.schema.json#/$defs/GuideRequirements", []byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if requirements.AirSupply == nil || *requirements.AirSupply {
		t.Fatal("explicit false lost")
	}
	if requirements.LoadBearing != nil {
		t.Fatal("null became false")
	}
	if requirements.Material == nil || *requirements.Material != "木材" {
		t.Fatal("material not normalized")
	}
	if requirements.SceneIDs == nil {
		t.Fatal("scene_ids became null")
	}
}

func TestInvalidJSONAndTrailingDocument(t *testing.T) {
	for _, input := range []string{"{", "{} {}", "null true"} {
		err := ValidateJSON("guide.schema.json#/$defs/GuideRequirements", []byte(input))
		var business *BusinessError
		if !errors.As(err, &business) || business.HTTPStatus != 400 {
			t.Fatalf("%s: %v", input, err)
		}
	}
}

func TestPurchaseHostBoundary(t *testing.T) {
	for _, raw := range []string{"http://shop.example/item", "https://shop.example.evil/item", "https://shop.example@evil.example/item", "https://user:pass@shop.example/item", "//shop.example/item", "javascript:alert(1)"} {
		if err := ValidatePurchaseURL(raw, []string{"shop.example"}); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	if err := ValidatePurchaseURL("https://SHOP.example/item?id=1", []string{"shop.example"}); err != nil {
		t.Fatal(err)
	}
}

func TestIntegerRepresentationsAndOverflow(t *testing.T) {
	const schema = "admin.schema.json#/$defs/CandidateInput"
	for _, input := range []string{`{"expected_workspace_revision":1.0}`, `{"expected_workspace_revision":1e0}`} {
		value, err := Decode[CandidateInput](schema, []byte(input))
		if err != nil || value.ExpectedWorkspaceRevision != 1 {
			t.Fatalf("%s: %+v %v", input, value, err)
		}
	}
	_, err := Decode[CandidateInput](schema, []byte(`{"expected_workspace_revision":999999999999999999999999}`))
	var business *BusinessError
	if !errors.As(err, &business) || business.HTTPStatus != 422 || !slices.Contains(business.Detail.Fields, "expected_workspace_revision") {
		t.Fatalf("overflow: %v", err)
	}
}
