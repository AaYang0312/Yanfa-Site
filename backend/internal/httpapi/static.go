package httpapi

import (
	"bytes"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"yuanfa-official/backend/internal/auth"
	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/publication"
	"yuanfa-official/backend/internal/release"
)

// RegisterStatic authorizes each path through a verified Handle. Neither the
// artifact parent, work directory, nor private DATA_DIR/media is a static root.
func RegisterStatic(mux *http.ServeMux, m AuthMiddleware, publications *publication.Service, releases *release.Manager, adminDir, contactHref, phone string) {
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			WriteError(w, contract.NewBusinessError(404, "NOT_FOUND", "页面不存在。"))
			return
		}
		for _, prefix := range []string{"/api", "/mcp", "/go", "/health"} {
			if r.URL.Path == prefix || strings.HasPrefix(r.URL.Path, prefix+"/") {
				WriteError(w, contract.NewBusinessError(404, "NOT_FOUND", "接口不存在。"))
				return
			}
		}
		if r.URL.Path == "/admin" || strings.HasPrefix(r.URL.Path, "/admin/") {
			serveAdmin(w, r, adminDir)
			return
		}
		name := release.Route(r.URL.Path)
		if name == "" {
			WriteError(w, contract.NewBusinessError(404, "NOT_FOUND", "页面不存在。"))
			return
		}
		var h *release.Handle
		var session auth.Session
		preview := false
		if cookie, err := r.Cookie(auth.PreviewCookie); err == nil && cookie.Value != "" {
			management, e := r.Cookie(auth.SessionCookie)
			if e != nil {
				WriteError(w, contract.NewBusinessError(401, "UNAUTHENTICATED", "预览需要有效管理会话。"))
				return
			}
			session, e = m.Auth.AuthenticateSession(r.Context(), management.Value)
			if e != nil {
				WriteError(w, e)
				return
			}
			h, e = publications.PreviewHandle(r.Context(), session.Actor, cookie.Value)
			if e != nil {
				WriteError(w, e)
				return
			}
			preview = true
		} else {
			var err error
			h, err = releases.Capture("")
			if err != nil {
				if r.URL.Path == "/" || strings.HasPrefix(r.URL.Path, "/products") {
					WriteError(w, err)
				} else {
					WriteError(w, contract.NewBusinessError(404, "NOT_FOUND", "资源不存在。"))
				}
				return
			}
		}
		isHTML := h.HasRoute(name)
		resourceHandle := h
		if !isHTML && !preview {
			if authorized, ok := releases.Resource(name); ok {
				resourceHandle = authorized
			}
		}
		f, err := resourceHandle.Open(name)
		if err != nil {
			slug := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/products/"), "/")
			if !preview && strings.HasPrefix(r.URL.Path, "/products/") && releases.Historical(slug) {
				body := []byte(`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="robots" content="noindex"><meta name="release-id" content="` + html.EscapeString(h.ID()) + `"><title>产品已下架</title></head><body><main><h1>该产品已下架</h1><p>当前官网版本不再展示此型号。请电话咨询：<a href="` + html.EscapeString(contactHref) + `">` + html.EscapeString(phone) + `</a></p><a href="/products/">查看当前产品</a></main></body></html>`)
				if err = releases.BeginResponse(h.ID(), func() error {
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					w.Header().Set("Cache-Control", "no-cache")
					w.Header().Set("X-Robots-Tag", "noindex")
					w.WriteHeader(200)
					return nil
				}); err != nil {
					WriteError(w, err)
					return
				}
				if r.Method != "HEAD" {
					_, _ = w.Write(body)
				}
				return
			}
			WriteError(w, contract.NewBusinessError(404, "NOT_FOUND", "页面或资源不存在。"))
			return
		}
		defer f.Close()
		stat, err := f.Stat()
		if err != nil || !stat.Mode().IsRegular() {
			WriteError(w, contract.NewBusinessError(503, "ARTIFACT_UNAVAILABLE", "发布资源不可用。"))
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if preview {
			w.Header().Set("Cache-Control", "private, no-store")
			w.Header().Set("X-Robots-Tag", "noindex")
			w.Header().Set("Vary", "Cookie")
			if isHTML {
				b, err := io.ReadAll(f)
				if err != nil {
					WriteError(w, err)
					return
				}
				csrf, _ := json.Marshal(session.CSRFToken)
				banner := `<style>[data-guide-root],[data-guide],astro-island[component-url*="guide"]{display:none!important}#yuanfa-preview{position:sticky;top:0;z-index:2147483647;padding:14px;background:#fff3bc;color:#211800;font:16px sans-serif}#yuanfa-preview button{margin-left:16px}</style><aside id="yuanfa-preview" role="status">预览模式，仅当前浏览器可见。此模式影响同一浏览器的其他官网标签页。<button id="yuanfa-preview-exit" type="button">退出预览</button><span id="yuanfa-preview-error"></span></aside><script>document.querySelector('#yuanfa-preview-exit').addEventListener('click',async()=>{const r=await fetch('/api/admin/preview-session',{method:'DELETE',headers:{'X-CSRF-Token':` + string(csrf) + `}});if(r.ok)location.assign('/');else document.querySelector('#yuanfa-preview-error').textContent='退出失败，请重新登录后退出预览';});</script>`
				marker := bytes.Index(b, []byte("<body"))
				if marker >= 0 {
					end := bytes.IndexByte(b[marker:], '>')
					if end >= 0 {
						marker += end + 1
					} else {
						marker = 0
					}
				} else {
					marker = 0
				}
				b = append(append(append(make([]byte, 0, len(b)+len(banner)), b[:marker]...), banner...), b[marker:]...)
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(200)
				if r.Method != "HEAD" {
					_, _ = w.Write(b)
				}
				return
			}
			// ServeContent may add Last-Modified but cannot turn private bytes public.
			http.ServeContent(w, r, name, stat.ModTime(), f)
			return
		}
		if isHTML {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			if _, err = r.Cookie(auth.SessionCookie); err == nil {
				w.Header().Set("Cache-Control", "private, no-cache")
				w.Header().Set("Vary", "Cookie")
			}
			if err = releases.BeginResponse(h.ID(), func() error { w.WriteHeader(200); return nil }); err != nil {
				WriteError(w, err)
				return
			}
			if r.Method != "HEAD" {
				_, _ = io.Copy(w, f)
			}
			return
		}
		if strings.HasPrefix(name, "_astro/") || strings.HasPrefix(name, "media/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		http.ServeContent(w, r, name, stat.ModTime(), f)
	})
}
func serveAdmin(w http.ResponseWriter, r *http.Request, directory string) {
	name := release.Route("/" + strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/admin"), "/"))
	if name == "" {
		WriteError(w, contract.NewBusinessError(404, "NOT_FOUND", "页面不存在。"))
		return
	}
	root, err := os.OpenRoot(filepath.Clean(directory))
	if err != nil {
		WriteError(w, contract.NewBusinessError(404, "NOT_FOUND", "后台页面尚未构建。"))
		return
	}
	defer root.Close()
	f, err := root.Open(name)
	if err != nil {
		WriteError(w, contract.NewBusinessError(404, "NOT_FOUND", "页面不存在。"))
		return
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil || !stat.Mode().IsRegular() {
		WriteError(w, contract.NewBusinessError(404, "NOT_FOUND", "页面不存在。"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, name, stat.ModTime(), f)
}
