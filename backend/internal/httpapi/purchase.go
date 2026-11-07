package httpapi

import (
	"errors"
	"html"
	"net/http"
	"net/url"
	"strconv"

	"yuanfa-official/backend/internal/catalog"
	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/release"
)

// RegisterPurchase accepts only stable IDs and release_id. The destination is
// resolved from the active server mapping, then the 303 is committed under the
// same short version boundary as JSON; no external page is fetched.
func RegisterPurchase(mux *http.ServeMux, releases *release.Manager, service *catalog.Service, contactHref string) {
	mux.HandleFunc("GET /go/products/{id}/{option_id}", func(w http.ResponseWriter, r *http.Request) {
		fail := func(err error) { purchaseError(w, r, releases, err, contactHref) }
		q, err := catalogQuery(r, false, true)
		if err != nil {
			fail(err)
			return
		}
		h, err := releases.Capture(expectedRelease(q))
		if err != nil {
			fail(err)
			return
		}
		target, err := service.Destination(r.Context(), h, r.PathValue("id"), r.PathValue("option_id"))
		if err != nil {
			fail(err)
			return
		}
		if err = releases.BeginResponse(h.ID(), func() error {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Location", target)
			w.WriteHeader(http.StatusSeeOther)
			return nil
		}); err != nil {
			fail(err)
		}
	})
}
func purchaseError(w http.ResponseWriter, r *http.Request, releases *release.Manager, err error, contactHref string) {
	status := 500
	message := "服务暂时不可用。"
	var business *contract.BusinessError
	if errors.As(err, &business) {
		status = business.HTTPStatus
		message = business.Detail.Message
	}
	title := "购买入口不可用"
	if status == 409 {
		title = "官网资料已更新"
		message = "商品或购买渠道已变化，请重新查看当前资料并选择。"
	} else if status == 404 {
		message = "该商品已下架或购买渠道已撤销，请查看当前产品或电话咨询。"
	}
	current := "/products/"
	if h, e := releases.Capture(""); e == nil {
		current = "/api/catalog/products/" + url.PathEscape(r.PathValue("id")) + "/purchase-options?release_id=" + url.QueryEscape(h.ID())
	}
	body := `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><meta name="robots" content="noindex"><title>` + html.EscapeString(title) + `</title></head><body><main><h1>` + html.EscapeString(title) + `</h1><p>` + html.EscapeString(message) + `</p><p><a href="` + html.EscapeString(current) + `">重新查看当前商品与渠道</a></p><p><a href="/products/">浏览当前产品</a></p><p><a href="` + html.EscapeString(contactHref) + `">电话咨询</a></p></main></body></html>`
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Robots-Tag", "noindex")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	if r.Method != "HEAD" {
		_, _ = w.Write([]byte(body))
	}
}
