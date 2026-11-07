package contract

import (
	"encoding/json"
	"fmt"
	"strings"
)

// JSONSchema returns a self-contained source schema for MCP/model consumers.
// All references are local $defs; no consumer needs a network schema loader.
// Extensions remain present; callers must still use Decode for their semantics.
func JSONSchema(name string) (json.RawMessage, error) {
	initialize.Do(loadSchemas)
	if initializationError != nil {
		return nil, initializationError
	}
	if _, ok := compiled[name]; !ok {
		return nil, fmt.Errorf("unknown contract schema %q", name)
	}
	parts := strings.SplitN(name, "#/$defs/", 2)
	source := sourceSchemas[parts[0]]["$defs"].(map[string]any)[parts[1]].(map[string]any)
	definitions := make(map[string]any)
	var bundle func(any, string) any
	bundle = func(value any, document string) any {
		switch value := value.(type) {
		case map[string]any:
			result := make(map[string]any, len(value))
			for key, child := range value {
				if key != "$ref" {
					result[key] = bundle(child, document)
					continue
				}
				reference := strings.TrimPrefix(child.(string), schemaBase)
				parts := strings.SplitN(reference, "#/$defs/", 2)
				origin := document
				if parts[0] != "" {
					origin = parts[0]
				}
				definition := parts[1]
				result[key] = "#/$defs/" + definition
				if _, ok := definitions[definition]; !ok {
					definitions[definition] = nil // reserve before descending
					target := sourceSchemas[origin]["$defs"].(map[string]any)[definition]
					definitions[definition] = bundle(target, origin)
				}
			}
			return result
		case []any:
			result := make([]any, len(value))
			for i, item := range value {
				result[i] = bundle(item, document)
			}
			return result
		default:
			return value
		}
	}
	root := bundle(source, parts[0]).(map[string]any)
	resolved, document := source, parts[0]
	for resolved["$ref"] != nil {
		resolved, document = resolveSource(resolved, document)
	}
	if kind, ok := resolved["type"]; ok {
		root["type"] = kind
	}
	root["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	root["$defs"] = definitions
	return json.Marshal(root)
}
