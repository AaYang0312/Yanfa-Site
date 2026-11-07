// Package httpapi adapts shared business errors and authentication to HTTP.
package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"yuanfa-official/backend/internal/auth"
	"yuanfa-official/backend/internal/contract"
)

const MaxAdminJSON = 256 * 1024

// WriteError exposes only BusinessError details. Internal causes are never sent
// to clients, since they may contain credentials, SQL or private file paths.
func WriteError(w http.ResponseWriter, err error) {
	var business *contract.BusinessError
	if !errors.As(err, &business) {
		business = contract.NewBusinessError(500, "INTERNAL_ERROR", "服务暂时不可用。")
	}
	WriteJSON(w, business.HTTPStatus, business.Response())
}
func WriteJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// DecodeJSON enforces the shared management JSON boundary before schema decode.
func DecodeJSON[T any](w http.ResponseWriter, r *http.Request, schema string) (T, error) {
	var zero T
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return zero, contract.NewBusinessError(400, "INVALID_JSON", "需要 application/json 请求。")
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxAdminJSON)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			return zero, contract.NewBusinessError(413, "PAYLOAD_TOO_LARGE", "JSON 请求超过 256 KiB。")
		}
		return zero, contract.NewBusinessError(400, "INVALID_JSON", "无法读取 JSON 请求。")
	}
	return contract.Decode[T](schema, data)
}

type AuthMiddleware struct {
	Auth          *auth.Service
	Origin        string
	SecureCookies bool
}

// CheckOrigin uses byte-for-byte origin matching; suffixes, alternate ports,
// missing/null origins and duplicate headers do not authorize browser writes.
func (m AuthMiddleware) CheckOrigin(r *http.Request) error {
	values := r.Header.Values("Origin")
	if len(values) != 1 || values[0] != m.Origin {
		return contract.NewBusinessError(403, "ORIGIN_FORBIDDEN", "请求来源不被允许。")
	}
	return nil
}
func (m AuthMiddleware) Browser(next func(http.ResponseWriter, *http.Request, auth.Session)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Header.Get("Authorization") != "" {
			WriteError(w, contract.NewBusinessError(403, "FORBIDDEN", "管理接口需要浏览器会话。"))
			return
		}
		cookie, err := r.Cookie(auth.SessionCookie)
		if err != nil {
			WriteError(w, contract.NewBusinessError(401, "UNAUTHENTICATED", "请先登录。"))
			return
		}
		session, err := m.Auth.AuthenticateSession(r.Context(), cookie.Value)
		if err != nil {
			WriteError(w, err)
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" {
			if err = m.CheckOrigin(r); err != nil {
				WriteError(w, err)
				return
			}
			if err = session.CheckCSRF(r.Header.Get("X-CSRF-Token")); err != nil {
				WriteError(w, err)
				return
			}
		}
		next(w, r, session)
	})
}

// Bearer is for the MCP transport only. Reuse on every SDK HTTP request and
// Authorize each tool invocation if its Actor is retained by SDK context.
func (m AuthMiddleware) Bearer(next func(http.ResponseWriter, *http.Request, auth.Actor)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if len(r.Header.Values("Origin")) > 0 {
			if err := m.CheckOrigin(r); err != nil {
				WriteError(w, err)
				return
			}
		}
		parts := strings.Fields(r.Header.Get("Authorization"))
		if len(r.Header.Values("Authorization")) != 1 || len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			w.Header().Set("WWW-Authenticate", `Bearer realm="yuanfa"`)
			WriteError(w, contract.NewBusinessError(401, "UNAUTHENTICATED", "需要有效 Bearer 凭证。"))
			return
		}
		actor, err := m.Auth.AuthenticateToken(r.Context(), parts[1])
		if err != nil {
			WriteError(w, err)
			return
		}
		next(w, r, actor)
	})
}

// RegisterAuth registers implemented identity routes only. state supplies the
// workspace revision and active release from their actual authorities.
func RegisterAuth(mux *http.ServeMux, m AuthMiddleware, state func(*http.Request) (int64, *string, error)) {
	response := func(r *http.Request, s auth.Session) (contract.SessionResponse, error) {
		revision, active, err := state(r)
		return contract.SessionResponse{AdminID: s.Actor.AdminID(), Username: s.Actor.Username(), CSRFToken: s.CSRFToken, ExpiresAt: s.ExpiresAt, WorkspaceRevision: revision, ActiveReleaseID: active}, err
	}
	mux.HandleFunc("POST /api/admin/login", func(w http.ResponseWriter, r *http.Request) {
		if err := m.CheckOrigin(r); err != nil {
			WriteError(w, err)
			return
		}
		input, err := DecodeJSON[contract.LoginInput](w, r, "admin.schema.json#/$defs/LoginInput")
		if err != nil {
			WriteError(w, err)
			return
		}
		result, err := m.Auth.Login(r.Context(), input)
		if err != nil {
			WriteError(w, err)
			return
		}
		out, err := response(r, result.Session)
		if err != nil {
			_ = m.Auth.Logout(r.Context(), result.Session.Actor)
			WriteError(w, err)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: auth.SessionCookie, Value: result.Secret, Path: "/", MaxAge: int(auth.SessionLifetime.Seconds()), Secure: m.SecureCookies, HttpOnly: true, SameSite: http.SameSiteStrictMode})
		WriteJSON(w, 200, out)
	})
	mux.Handle("GET /api/admin/session", m.Browser(func(w http.ResponseWriter, r *http.Request, s auth.Session) {
		out, err := response(r, s)
		if err != nil {
			WriteError(w, err)
			return
		}
		WriteJSON(w, 200, out)
	}))
	mux.Handle("POST /api/admin/logout", m.Browser(func(w http.ResponseWriter, r *http.Request, s auth.Session) {
		if err := m.Auth.Logout(r.Context(), s.Actor); err != nil {
			WriteError(w, err)
			return
		}
		for _, name := range []string{auth.SessionCookie, auth.PreviewCookie} {
			http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, Secure: m.SecureCookies, HttpOnly: true, SameSite: http.SameSiteStrictMode})
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.Handle("GET /api/admin/mcp-tokens", m.Browser(func(w http.ResponseWriter, r *http.Request, s auth.Session) {
		out, err := m.Auth.ListTokens(r.Context(), s.Actor)
		if err != nil {
			WriteError(w, err)
			return
		}
		WriteJSON(w, 200, out)
	}))
	mux.Handle("POST /api/admin/mcp-tokens", m.Browser(func(w http.ResponseWriter, r *http.Request, s auth.Session) {
		input, err := DecodeJSON[contract.TokenInput](w, r, "admin.schema.json#/$defs/TokenInput")
		if err != nil {
			WriteError(w, err)
			return
		}
		out, err := m.Auth.IssueToken(r.Context(), s.Actor, input)
		if err != nil {
			WriteError(w, err)
			return
		}
		WriteJSON(w, 201, out)
	}))
	mux.Handle("DELETE /api/admin/mcp-tokens/{id}", m.Browser(func(w http.ResponseWriter, r *http.Request, s auth.Session) {
		if err := m.Auth.RevokeToken(r.Context(), s.Actor, r.PathValue("id")); err != nil {
			WriteError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
}
