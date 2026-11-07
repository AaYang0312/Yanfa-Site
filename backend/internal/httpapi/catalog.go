package httpapi

import (
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"strconv"

	"yuanfa-official/backend/internal/catalog"
	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/release"
)

// catalogQuery converts HTTP scalars before the shared strict schema validates
// them. Direct business consumers use the same CatalogQuery schema in Search.
func catalogQuery(r *http.Request, full, requiredRelease bool) (contract.CatalogQuery, error) {
	var zero contract.CatalogQuery
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return zero, contract.NewBusinessError(400, "INVALID_QUERY", "查询格式不合法。")
	}
	object := map[string]any{}
	for name, items := range values {
		invalid := func() (contract.CatalogQuery, error) {
			return zero, contract.NewBusinessError(422, "VALIDATION_FAILED", "查询字段不合法。", name)
		}
		if len(items) != 1 || !full && name != "release_id" {
			return invalid()
		}
		switch name {
		case "limit":
			v, e := strconv.ParseInt(items[0], 10, 64)
			if e != nil {
				return invalid()
			}
			object[name] = v
		case "fastener_length_mm":
			v, e := strconv.ParseFloat(items[0], 64)
			if e != nil || math.IsNaN(v) || math.IsInf(v, 0) {
				return invalid()
			}
			object[name] = v
		case "include_unknown":
			if items[0] != "true" && items[0] != "false" {
				return invalid()
			}
			object[name] = items[0] == "true"
		default:
			object[name] = items[0]
		}
	}
	b, err := json.Marshal(object)
	if err != nil {
		return zero, err
	}
	q, err := contract.Decode[contract.CatalogQuery]("public-catalog.schema.json#/$defs/CatalogQuery", b)
	if err != nil {
		return zero, err
	}
	if requiredRelease && q.ReleaseID == nil {
		return zero, contract.NewBusinessError(422, "VALIDATION_FAILED", "请提供当前发布版本。", "release_id")
	}
	return q, nil
}
func expectedRelease(q contract.CatalogQuery) string {
	if q.ReleaseID != nil {
		return *q.ReleaseID
	}
	return ""
}

// catalogJSON performs serialization outside the publication gate, commits only
// headers under BeginResponse, then sends the body without holding the gate.
func catalogJSON(w http.ResponseWriter, r *http.Request, releases *release.Manager, h *release.Handle, value any) {
	b, err := json.Marshal(value)
	if err != nil {
		WriteError(w, err)
		return
	}
	if err = releases.BeginResponse(h.ID(), func() error {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		return nil
	}); err != nil {
		WriteError(w, err)
		return
	}
	if r.Method != "HEAD" {
		_, _ = w.Write(b)
	}
}

// RegisterCatalog always captures active authority directly. Neither admin nor
// preview cookies are examined, and no workspace/preview service is supplied.
func RegisterCatalog(mux *http.ServeMux, releases *release.Manager, service *catalog.Service) {
	mux.HandleFunc("GET /api/catalog/release", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			WriteError(w, contract.NewBusinessError(422, "VALIDATION_FAILED", "版本入口不接受查询条件。"))
			return
		}
		h, err := releases.Capture("")
		if err != nil {
			WriteError(w, err)
			return
		}
		catalogJSON(w, r, releases, h, contract.ReleaseResponse{SchemaVersion: contract.SchemaVersion, ReleaseID: h.ID()})
	})
	mux.HandleFunc("GET /api/catalog/products", func(w http.ResponseWriter, r *http.Request) {
		q, err := catalogQuery(r, true, false)
		if err != nil {
			WriteError(w, err)
			return
		}
		h, err := releases.Capture(expectedRelease(q))
		if err != nil {
			WriteError(w, err)
			return
		}
		out, err := service.Search(r.Context(), h, q)
		if err != nil {
			WriteError(w, err)
			return
		}
		catalogJSON(w, r, releases, h, out)
	})
	mux.HandleFunc("GET /api/catalog/products/{id}", func(w http.ResponseWriter, r *http.Request) {
		q, err := catalogQuery(r, false, false)
		if err != nil {
			WriteError(w, err)
			return
		}
		h, err := releases.Capture(expectedRelease(q))
		if err != nil {
			WriteError(w, err)
			return
		}
		p, err := service.Get(r.Context(), h, r.PathValue("id"))
		if err != nil {
			WriteError(w, err)
			return
		}
		catalogJSON(w, r, releases, h, contract.ProductDetail{ReleaseID: h.ID(), Product: p})
	})
	mux.HandleFunc("GET /api/catalog/export", func(w http.ResponseWriter, r *http.Request) {
		q, err := catalogQuery(r, false, false)
		if err != nil {
			WriteError(w, err)
			return
		}
		h, err := releases.Capture(expectedRelease(q))
		if err != nil {
			WriteError(w, err)
			return
		}
		out, err := service.Export(r.Context(), h)
		if err != nil {
			WriteError(w, err)
			return
		}
		catalogJSON(w, r, releases, h, out)
	})
	mux.HandleFunc("GET /api/catalog/products/{id}/purchase-options", func(w http.ResponseWriter, r *http.Request) {
		q, err := catalogQuery(r, false, true)
		if err != nil {
			WriteError(w, err)
			return
		}
		h, err := releases.Capture(expectedRelease(q))
		if err != nil {
			WriteError(w, err)
			return
		}
		out, err := service.PurchaseOptions(r.Context(), h, r.PathValue("id"))
		if err != nil {
			WriteError(w, err)
			return
		}
		catalogJSON(w, r, releases, h, out)
	})
}
