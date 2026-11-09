package httpapi

import (
	"net/http"
	"strconv"

	"yuanfa-official/backend/internal/auth"
	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/publication"
)

// RegisterPublication contains transport checks only. MCP calls the same service
// methods with its authenticated Actor; it never receives a confirm tool.
func RegisterPublication(mux *http.ServeMux, m AuthMiddleware, s *publication.Service) {
	mux.Handle("GET /api/admin/site-updates", m.Browser(func(w http.ResponseWriter, r *http.Request, session auth.Session) {
		out, err := s.SiteUpdates(r.Context(), session.Actor)
		if err != nil {
			WriteError(w, err)
			return
		}
		WriteJSON(w, 200, out)
	}))
	mux.Handle("POST /api/admin/site-updates/retry", m.Browser(func(w http.ResponseWriter, r *http.Request, session auth.Session) {
		if err := s.RetrySiteUpdate(r.Context(), session.Actor); err != nil {
			WriteError(w, err)
			return
		}
		WriteJSON(w, 202, map[string]string{"status": "pending"})
	}))
	mux.Handle("POST /api/admin/candidates", m.Browser(func(w http.ResponseWriter, r *http.Request, session auth.Session) {
		input, err := DecodeJSON[contract.CandidateInput](w, r, "admin.schema.json#/$defs/CandidateInput")
		if err != nil {
			WriteError(w, err)
			return
		}
		out, err := s.CreateCandidate(r.Context(), session.Actor, input.ExpectedWorkspaceRevision, r.Header.Get("Idempotency-Key"))
		if err != nil {
			WriteError(w, err)
			return
		}
		WriteJSON(w, 202, out)
	}))
	mux.Handle("GET /api/admin/candidates/{id}", m.Browser(func(w http.ResponseWriter, r *http.Request, session auth.Session) {
		out, err := s.GetCandidate(r.Context(), session.Actor, r.PathValue("id"))
		if err != nil {
			WriteError(w, err)
			return
		}
		WriteJSON(w, 200, out)
	}))
	mux.Handle("POST /api/admin/publication-requests", m.Browser(func(w http.ResponseWriter, r *http.Request, session auth.Session) {
		input, err := DecodeJSON[contract.PublicationInput](w, r, "admin.schema.json#/$defs/PublicationInput")
		if err != nil {
			WriteError(w, err)
			return
		}
		out, err := s.Request(r.Context(), session.Actor, input, r.Header.Get("Idempotency-Key"))
		if err != nil {
			WriteError(w, err)
			return
		}
		WriteJSON(w, 201, out)
	}))
	mux.Handle("GET /api/admin/publication-requests/{id}", m.Browser(func(w http.ResponseWriter, r *http.Request, session auth.Session) {
		out, err := s.GetRequest(r.Context(), session.Actor, r.PathValue("id"))
		if err != nil {
			WriteError(w, err)
			return
		}
		WriteJSON(w, 200, out)
	}))
	mux.Handle("POST /api/admin/publication-requests/{id}/confirm", m.Browser(func(w http.ResponseWriter, r *http.Request, session auth.Session) {
		input, err := DecodeJSON[contract.ConfirmationInput](w, r, "admin.schema.json#/$defs/ConfirmationInput")
		if err != nil {
			WriteError(w, err)
			return
		}
		out, err := s.Confirm(r.Context(), session.Actor, r.PathValue("id"), input, r.Header.Get("Idempotency-Key"))
		if err != nil {
			WriteError(w, err)
			return
		}
		WriteJSON(w, 202, out)
	}))
	mux.Handle("POST /api/admin/publication-requests/{id}/cancel", m.Browser(func(w http.ResponseWriter, r *http.Request, session auth.Session) {
		out, err := s.Cancel(r.Context(), session.Actor, r.PathValue("id"))
		if err != nil {
			WriteError(w, err)
			return
		}
		WriteJSON(w, 200, out)
	}))
	mux.Handle("GET /api/admin/publications", m.Browser(func(w http.ResponseWriter, r *http.Request, session auth.Session) {
		values := r.URL.Query()
		for key, v := range values {
			if (key != "cursor" && key != "limit") || len(v) != 1 {
				WriteError(w, contract.NewBusinessError(422, "VALIDATION_FAILED", "分页参数不合法。", key))
				return
			}
		}
		limit := 0
		var err error
		if value := values.Get("limit"); value != "" {
			limit, err = strconv.Atoi(value)
			if err != nil || limit < 1 {
				WriteError(w, contract.NewBusinessError(422, "VALIDATION_FAILED", "分页数量不合法。", "limit"))
				return
			}
		}
		out, err := s.List(r.Context(), session.Actor, values.Get("cursor"), limit)
		if err != nil {
			WriteError(w, err)
			return
		}
		WriteJSON(w, 200, out)
	}))
}
