package httpapi

import (
	"net/http"

	"yuanfa-official/backend/internal/auth"
	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/publication"
)

func RegisterPreview(mux *http.ServeMux, m AuthMiddleware, s *publication.Service) {
	cookie := func(w http.ResponseWriter, id string) {
		age := 7200
		if id == "" {
			age = -1
		}
		http.SetCookie(w, &http.Cookie{Name: auth.PreviewCookie, Value: id, Path: "/", MaxAge: age, Secure: m.SecureCookies, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	}
	mux.Handle("POST /api/admin/preview-session", m.Browser(func(w http.ResponseWriter, r *http.Request, session auth.Session) {
		input, err := DecodeJSON[contract.PreviewInput](w, r, "admin.schema.json#/$defs/PreviewInput")
		if err != nil {
			WriteError(w, err)
			return
		}
		out, err := s.Preview(r.Context(), session.Actor, input.CandidateID)
		if err != nil {
			WriteError(w, err)
			return
		}
		cookie(w, input.CandidateID)
		WriteJSON(w, 200, out)
	}))
	mux.Handle("DELETE /api/admin/preview-session", m.Browser(func(w http.ResponseWriter, r *http.Request, session auth.Session) {
		out, err := s.Preview(r.Context(), session.Actor, "")
		if err != nil {
			WriteError(w, err)
			return
		}
		cookie(w, "")
		WriteJSON(w, 200, out)
	}))
}
