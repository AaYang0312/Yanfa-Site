package contract

import (
	"encoding/json"
	"fmt"
	"math/big"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

func validateInvariants(schema string, value any) error {
	fields := make([]string, 0)
	document := strings.Split(schema, "#")[0]
	walkInvariants(map[string]any{"$ref": schema}, value, document, "", &fields)
	if len(fields) == 0 {
		return nil
	}
	slices.Sort(fields)
	fields = slices.Compact(fields)
	return NewBusinessError(422, "VALIDATION_FAILED", "字段或引用不合法。", fields...)
}

// normalizeValue follows source-declared trimming and integer types. It preserves
// null, false, missing keys and unknown keys for strict validation to distinguish.
func normalizeValue(schema map[string]any, value any, document string) any {
	if value == nil {
		return nil
	}
	// Visit the reference without replacing this node or its document context.
	if _, ok := schema["$ref"].(string); ok {
		target, targetDocument := resolveSource(schema, document)
		value = normalizeValue(target, value, targetDocument)
	}
	for _, keyword := range []string{"allOf", "anyOf", "oneOf"} {
		if branches, ok := schema[keyword].([]any); ok {
			for _, branch := range branches {
				value = normalizeValue(branch.(map[string]any), value, document)
			}
		}
	}
	if text, ok := value.(string); ok && schema["x-trim"] == true {
		return strings.TrimSpace(text)
	}
	// Validate floating-point constraints against the representation consumers get.
	// Underflow becomes zero and is rejected by the source's positive minimum;
	// overflow stays json.Number so its source maximum reports the field path.
	if number, ok := value.(json.Number); ok && schema["type"] == "number" {
		if converted, err := number.Float64(); err == nil {
			return converted
		}
	}
	if number, ok := value.(json.Number); ok && schema["type"] == "integer" {
		if _, err := number.Int64(); err != nil {
			if rational, ok := new(big.Rat).SetString(string(number)); ok && rational.IsInt() && rational.Num().IsInt64() {
				return json.Number(rational.Num().String())
			}
		}
	}
	if object, ok := value.(map[string]any); ok {
		if properties, ok := schema["properties"].(map[string]any); ok {
			for key, child := range properties {
				if v, present := object[key]; present {
					object[key] = normalizeValue(child.(map[string]any), v, document)
				}
			}
		}
	}
	if array, ok := value.([]any); ok {
		if child, ok := schema["items"].(map[string]any); ok {
			for i, v := range array {
				array[i] = normalizeValue(child, v, document)
			}
		}
	}
	return value
}

func fieldPath(path, key string) string {
	if path == "" {
		return key
	}
	if key == "" {
		return path
	}
	return path + "." + key
}

func resolveSource(schema map[string]any, document string) (map[string]any, string) {
	reference, ok := schema["$ref"].(string)
	if !ok {
		return schema, document
	}
	reference = strings.TrimPrefix(reference, schemaBase)
	parts := strings.SplitN(reference, "#", 2)
	if parts[0] != "" {
		document = parts[0]
	}
	var node any = sourceSchemas[document]
	if len(parts) == 2 {
		for _, part := range strings.Split(strings.TrimPrefix(parts[1], "/"), "/") {
			node = node.(map[string]any)[part]
		}
	}
	return node.(map[string]any), document
}

func walkInvariants(schema map[string]any, value any, document, path string, fields *[]string) {
	if value == nil {
		return
	}
	if _, ok := schema["$ref"].(string); ok {
		target, targetDocument := resolveSource(schema, document)
		walkInvariants(target, value, targetDocument, path, fields)
	}
	if branches, ok := schema["allOf"].([]any); ok {
		for _, branch := range branches {
			walkInvariants(branch.(map[string]any), value, document, path, fields)
		}
	}
	fail := func(key string) {
		p := fieldPath(path, key)
		if p == "" {
			p = "$"
		}
		*fields = append(*fields, p)
	}
	if branches, ok := schema["anyOf"].([]any); ok {
		for _, branch := range branches {
			s := branch.(map[string]any)
			if s["type"] != "null" {
				walkInvariants(s, value, document, path, fields)
				break
			}
		}
	}
	if branches, ok := schema["oneOf"].([]any); ok {
		for _, branch := range branches {
			s := branch.(map[string]any)
			r := s["$ref"].(string)
			name := r
			if strings.HasPrefix(r, "#") {
				name = document + r
			}
			if sch, ok := compiled[name]; ok && sch.Validate(value) == nil {
				walkInvariants(s, value, document, path, fields)
				break
			}
		}
	}
	if schema["x-safe-relative-path"] == true {
		for _, part := range strings.Split(value.(string), "/") {
			if part == "." || part == ".." {
				fail("")
				break
			}
		}
	}
	if object, ok := value.(map[string]any); ok {
		if maximum, ok := schema["x-max-pixels"].(float64); ok {
			width, _ := object["width"].(json.Number).Int64()
			height, _ := object["height"].(json.Number).Int64()
			if width*height > int64(maximum) {
				fail("width")
				fail("height")
			}
		}
		if schema["x-product-version"] == true && object["version"] != object["product"].(map[string]any)["version"] {
			fail("version")
		}
		if schema["x-release-consistency"] == true {
			if product, ok := object["product"].(map[string]any); ok && product["release_id"] != object["release_id"] {
				fail("product.release_id")
			}
			for _, key := range []string{"items", "products"} {
				if items, ok := object[key].([]any); ok {
					for i, item := range items {
						if item.(map[string]any)["release_id"] != object["release_id"] {
							fail(fmt.Sprintf("%s.%d.release_id", key, i))
						}
					}
				}
			}
		}
		if keys, ok := schema["x-ordered-range"].([]any); ok {
			low := object[keys[0].(string)].(float64)
			high := object[keys[1].(string)].(float64)
			if low > high {
				fail(keys[1].(string))
			}
		}
		if limit, ok := schema["x-history-max-length"].(float64); ok {
			total := 0
			if history, ok := object["messages"].([]any); ok {
				for _, item := range history {
					total += utf8.RuneCountInString(item.(map[string]any)["content"].(string))
				}
			}
			if total > int(limit) {
				fail("messages")
			}
		}
		if schema["x-cover-membership"] == true && object["cover_media_id"] != nil {
			found := false
			for _, image := range object["images"].([]any) {
				if image.(map[string]any)["media_id"] == object["cover_media_id"] {
					found = true
					break
				}
			}
			if !found {
				fail("cover_media_id")
			}
		}
		if schema["x-selection-missing-fields"] == true && object["state"] == "selected" {
			if !slices.Equal(object["missing_fields"].([]any), object["selection"].(map[string]any)["missing_fields"].([]any)) {
				fail("missing_fields")
			}
		}
		if schema["x-active-membership"] == true && !containsValue(object["successful_release_ids"].([]any), object["release_id"]) {
			fail("successful_release_ids")
		}
		if schema["x-catalog-references"] == true {
			catalogReferences(object, fail)
		}
		if properties, ok := schema["properties"].(map[string]any); ok {
			for key, child := range properties {
				if v, present := object[key]; present {
					walkInvariants(child.(map[string]any), v, document, fieldPath(path, key), fields)
				}
			}
		}
	}
	if array, ok := value.([]any); ok {
		if key, ok := schema["x-unique-by"].(string); ok {
			seen := make(map[any]bool, len(array))
			for i, item := range array {
				v := item.(map[string]any)[key]
				if v == nil {
					continue
				}
				if seen[v] {
					fail(strconv.Itoa(i) + "." + key)
				}
				seen[v] = true
			}
		}
		if items, ok := schema["items"].(map[string]any); ok {
			for i, item := range array {
				walkInvariants(items, item, document, fieldPath(path, strconv.Itoa(i)), fields)
			}
		}
	}
}

func containsValue(values []any, want any) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func catalogReferences(object map[string]any, fail func(string)) {
	products := object["products"].([]any)
	series := object["legacy_series"].([]any)
	productIDs := make(map[any]bool, len(products))
	seriesIDs := make(map[any]bool, len(series))
	slugs := make(map[any]bool, len(products)+len(series))
	models := make(map[string]bool, len(products))
	for _, item := range products {
		productIDs[item.(map[string]any)["id"]] = true
	}
	for i, item := range series {
		s := item.(map[string]any)
		seriesIDs[s["id"]] = true
		if slugs[s["slug"]] {
			fail(fmt.Sprintf("legacy_series.%d.slug", i))
		}
		slugs[s["slug"]] = true
		for _, id := range s["product_ids"].([]any) {
			if !productIDs[id] {
				fail(fmt.Sprintf("legacy_series.%d.product_ids", i))
				break
			}
		}
	}
	for i, item := range products {
		p := item.(map[string]any)
		if slugs[p["slug"]] {
			fail(fmt.Sprintf("products.%d.slug", i))
		}
		slugs[p["slug"]] = true
		model := strings.ToLower(p["model"].(string))
		if model != "" {
			if models[model] {
				fail(fmt.Sprintf("products.%d.model", i))
			}
			models[model] = true
		}
		for _, id := range p["legacy_series_ids"].([]any) {
			if !seriesIDs[id] {
				fail(fmt.Sprintf("products.%d.legacy_series_ids", i))
			}
		}
		for _, item := range series {
			s := item.(map[string]any)
			if containsValue(p["legacy_series_ids"].([]any), s["id"]) != containsValue(s["product_ids"].([]any), p["id"]) {
				fail(fmt.Sprintf("products.%d.legacy_series_ids", i))
			}
		}
	}
}

// ValidatePurchaseURL uses parsed HTTPS hostname membership. The deployment's
// exact host allowlist must contain only customer-verified product hosts, never
// short-link/redirect services. It does not fetch or guess a destination.
func ValidatePurchaseURL(raw string, allowedHosts []string) error {
	target, err := url.Parse(raw)
	if err == nil && target.Scheme == "https" && target.Hostname() != "" && target.User == nil {
		for _, host := range allowedHosts {
			if strings.EqualFold(target.Hostname(), host) {
				return nil
			}
		}
	}
	return NewBusinessError(422, "INVALID_PURCHASE_URL", "商城地址必须是已核实主机上的 HTTPS 商品地址。", "purchase_options.url")
}
