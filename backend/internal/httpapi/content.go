package httpapi

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"yuanfa-official/backend/internal/auth"
	"yuanfa-official/backend/internal/content"
	"yuanfa-official/backend/internal/contract"
)

// DecodeQuery applies the same source-schema rules as JSON/MCP. Duplicate and
// unknown keys are rejected rather than silently choosing a conflicting value.
func DecodeQuery[T any](r *http.Request, schema string) (T, error) {
	var zero T
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return zero, contract.NewBusinessError(400, "INVALID_QUERY", "查询格式不合法。")
	}
	object := map[string]any{}
	for name, items := range values {
		if len(items) != 1 {
			return zero, contract.NewBusinessError(422, "VALIDATION_FAILED", "查询参数不能重复。", name)
		}
		if name == "limit" {
			n, e := strconv.ParseInt(items[0], 10, 64)
			if e != nil {
				return zero, contract.NewBusinessError(422, "VALIDATION_FAILED", "分页数量不合法。", name)
			}
			object[name] = n
		} else {
			object[name] = items[0]
		}
	}
	b, err := json.Marshal(object)
	if err != nil {
		return zero, err
	}
	return contract.Decode[T](schema, b)
}
func productVersion(r *http.Request) (int64, error) {
	values := r.Header.Values("If-Match")
	if len(values) == 0 {
		return 0, contract.NewBusinessError(428, "PRECONDITION_REQUIRED", "请提供当前产品的 If-Match。")
	}
	if len(values) != 1 {
		return 0, contract.NewBusinessError(422, "INVALID_ETAG", "需要单个强 ETag。", "If-Match")
	}
	text := values[0]
	if len(text) < 3 || text[0] != '"' || text[len(text)-1] != '"' {
		return 0, contract.NewBusinessError(422, "INVALID_ETAG", "需要单个强 ETag。", "If-Match")
	}
	number := text[1 : len(text)-1]
	version, err := strconv.ParseInt(number, 10, 64)
	if err != nil || version < 1 || version > 9007199254740991 || strconv.FormatInt(version, 10) != number {
		return 0, contract.NewBusinessError(422, "INVALID_ETAG", "版本不合法。", "If-Match")
	}
	return version, nil
}
func writeProduct(w http.ResponseWriter, status int, out contract.ProductEnvelope) {
	w.Header().Set("ETag", `"`+strconv.FormatInt(out.Version, 10)+`"`)
	WriteJSON(w, status, out)
}

func RegisterContent(mux *http.ServeMux, m AuthMiddleware, service *content.Service) {
	mux.Handle("GET /api/admin/products", m.Browser(func(w http.ResponseWriter, r *http.Request, s auth.Session) {
		q, err := DecodeQuery[contract.AdminProductQuery](r, "admin.schema.json#/$defs/AdminProductQuery")
		if err != nil {
			WriteError(w, err)
			return
		}
		out, err := service.Search(r.Context(), s.Actor, q)
		if err != nil {
			WriteError(w, err)
			return
		}
		WriteJSON(w, 200, out)
	}))
	mux.Handle("POST /api/admin/products", m.Browser(func(w http.ResponseWriter, r *http.Request, s auth.Session) {
		input, err := DecodeJSON[contract.ProductCreate](w, r, "product.schema.json#/$defs/ProductCreate")
		if err != nil {
			WriteError(w, err)
			return
		}
		out, err := service.Create(r.Context(), s.Actor, input)
		if err != nil {
			WriteError(w, err)
			return
		}
		w.Header().Set("Location", "/api/admin/products/"+out.Product.ID)
		writeProduct(w, 201, out)
	}))
	mux.Handle("GET /api/admin/products/{id}", m.Browser(func(w http.ResponseWriter, r *http.Request, s auth.Session) {
		out, err := service.Get(r.Context(), s.Actor, r.PathValue("id"))
		if err != nil {
			WriteError(w, err)
			return
		}
		writeProduct(w, 200, out)
	}))
	mux.Handle("PUT /api/admin/products/{id}", m.Browser(func(w http.ResponseWriter, r *http.Request, s auth.Session) {
		version, err := productVersion(r)
		if err != nil {
			WriteError(w, err)
			return
		}
		input, err := DecodeJSON[contract.ProductEdit](w, r, "product.schema.json#/$defs/ProductEdit")
		if err != nil {
			WriteError(w, err)
			return
		}
		out, err := service.Replace(r.Context(), s.Actor, r.PathValue("id"), version, input)
		if err != nil {
			WriteError(w, err)
			return
		}
		writeProduct(w, 200, out)
	}))
	mux.Handle("DELETE /api/admin/products/{id}", m.Browser(func(w http.ResponseWriter, r *http.Request, s auth.Session) {
		version, err := productVersion(r)
		if err != nil {
			WriteError(w, err)
			return
		}
		out, err := service.Delete(r.Context(), s.Actor, r.PathValue("id"), version)
		if err != nil {
			WriteError(w, err)
			return
		}
		WriteJSON(w, 200, out)
	}))
	mux.Handle("POST /api/admin/products/{id}/guide-review", m.Browser(func(w http.ResponseWriter, r *http.Request, s auth.Session) {
		version, err := productVersion(r)
		if err != nil {
			WriteError(w, err)
			return
		}
		input, err := DecodeJSON[contract.GuideReviewInput](w, r, "product.schema.json#/$defs/GuideReviewInput")
		if err != nil {
			WriteError(w, err)
			return
		}
		out, err := service.Review(r.Context(), s.Actor, r.PathValue("id"), version, input)
		if err != nil {
			WriteError(w, err)
			return
		}
		writeProduct(w, 200, out)
	}))
}
