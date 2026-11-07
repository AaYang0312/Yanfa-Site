package contract

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

// Consume the concrete DTO and serialize it as the downstream business caller
// would. Comparing after typed consumption catches float underflow and null loss.
func consumeTyped[T any](schema string, raw []byte) (any, error) {
	value, err := Decode[T](schema, raw)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var result any
	err = json.Unmarshal(encoded, &result)
	return result, err
}

func TestConcreteConsumerBoundaries(t *testing.T) {
	data, err := os.ReadFile("../../../tests/contracts/consumer-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name     string   `json:"name"`
		Schema   string   `json:"schema"`
		Raw      string   `json:"raw"`
		Valid    bool     `json:"valid"`
		Fields   []string `json:"fields"`
		Expected any      `json:"expected"`
	}
	if err = json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, entry := range cases {
		t.Run(entry.Name, func(t *testing.T) {
			var value any
			var err error
			raw := []byte(entry.Raw)
			switch entry.Schema {
			case "admin.schema.json#/$defs/CandidateInput":
				value, err = consumeTyped[CandidateInput](entry.Schema, raw)
			case "guide.schema.json#/$defs/GuideRequirements":
				value, err = consumeTyped[GuideRequirements](entry.Schema, raw)
			case "guide.schema.json#/$defs/GuideResult":
				value, err = consumeTyped[GuideResult](entry.Schema, raw)
			case "product.schema.json#/$defs/ProductCreate", "mcp-tools.schema.json#/$defs/MCPCreateProductInput":
				value, err = consumeTyped[ProductCreate](entry.Schema, raw)
			case "product.schema.json#/$defs/PublishedProduct", "product.schema.json#/$defs/ProductDocument":
				value, err = consumeTyped[ProductDocument](entry.Schema, raw)
			case "catalog-snapshot.schema.json#/$defs/CatalogSnapshot", "catalog-snapshot.schema.json#/$defs/PublishableSnapshot":
				value, err = consumeTyped[CatalogSnapshot](entry.Schema, raw)
			case "mcp-tools.schema.json#/$defs/MCPGetProductOutput", "mcp-tools.schema.json#/$defs/MCPCreateProductOutput":
				value, err = consumeTyped[ProductEnvelope](entry.Schema, raw)
			case "guide.schema.json#/$defs/GuideModelProposal":
				value, err = consumeTyped[GuideModelProposal](entry.Schema, raw)
			case "mcp-tools.schema.json#/$defs/MCPSearchProductsInput":
				value, err = consumeTyped[MCPSearchProductsInput](entry.Schema, raw)
			case "admin.schema.json#/$defs/AdminProductQuery":
				value, err = consumeTyped[AdminProductQuery](entry.Schema, raw)
			default:
				t.Fatalf("missing concrete consumer for %s", entry.Schema)
			}
			if entry.Valid {
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(value, entry.Expected) {
					t.Fatalf("typed consumer changed value: got %#v, want %#v", value, entry.Expected)
				}
				return
			}
			for _, checkErr := range []error{ValidateJSON(entry.Schema, raw), err} {
				var business *BusinessError
				if !errors.As(checkErr, &business) || business.HTTPStatus != 422 {
					t.Fatalf("expected 422, got %v", checkErr)
				}
				for _, field := range entry.Fields {
					found := false
					for _, path := range business.Detail.Fields {
						if path == field || strings.HasPrefix(path, field+".") {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("missing %s in %v", field, business.Detail.Fields)
					}
				}
			}
		})
	}
}
