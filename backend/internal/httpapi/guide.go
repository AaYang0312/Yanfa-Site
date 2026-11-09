package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/guide"
	"yuanfa-official/backend/internal/release"
)

// GuideOptions binds the inference executor and deployment IP authority. IDs
// bypass model capacity; the optional argument keeps deterministic Service-only
// consumers independent of model configuration.
type GuideOptions struct {
	Agent    *guide.Agent
	Limits   *guide.Limits
	ClientIP func(*http.Request) string
}

// RegisterGuide uses a single captured public handle and commits JSON/SSE
// headers only through BeginResponse. Streamed text remains provisional until
// the final result passes the complete business and active-release checks.
func RegisterGuide(mux *http.ServeMux, releases *release.Manager, service *guide.Service, options ...GuideOptions) {
	var config GuideOptions
	if len(options) > 0 {
		config = options[0]
	}
	mux.HandleFunc("POST /api/guide/messages", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			WriteError(w, contract.NewBusinessError(422, "VALIDATION_FAILED", "导购入口不接受查询条件。"))
			return
		}
		req, err := DecodeJSON[contract.GuideRequest](w, r, "guide.schema.json#/$defs/GuideRequest")
		if err != nil {
			WriteError(w, err)
			return
		}
		h, err := releases.Capture(req.ReleaseID)
		if err != nil {
			WriteError(w, err)
			return
		}
		if config.Limits != nil {
			ip := r.RemoteAddr
			if config.ClientIP != nil {
				ip = config.ClientIP(r)
			}
			if err = config.Limits.Allow(ip); err != nil {
				WriteError(w, err)
				return
			}
		}
		var result contract.GuideResult
		status := http.StatusOK
		if req.SelectedProductID != nil {
			selection := contract.SelectionRequest{ReleaseID: req.ReleaseID, SelectedProductID: *req.SelectedProductID, Requirements: *req.Requirements}
			if config.Agent != nil {
				result, err = config.Agent.Select(r.Context(), h, selection)
			} else {
				result, err = service.Select(r.Context(), h, selection)
			}
		} else {
			consultation := contract.ConsultRequest{ReleaseID: req.ReleaseID, Message: *req.Message, Messages: req.Messages, Requirements: req.Requirements, ContextProductID: req.ContextProductID}
			if config.Agent == nil {
				result, err = service.Unavailable(r.Context(), h, consultation)
			} else {
				var releaseSlot func()
				if config.Limits != nil {
					var e error
					releaseSlot, e = config.Limits.Acquire()
					if e != nil {
						WriteError(w, e)
						return
					}
				}
				if acceptsGuideStream(r.Header.Get("Accept")) {
					if releaseSlot != nil {
						defer releaseSlot()
					}
					guideStream(w, r, releases, h, config.Agent, consultation)
					return
				}
				func() {
					if releaseSlot != nil {
						defer releaseSlot()
					}
					result, err = config.Agent.Consult(r.Context(), h, consultation)
				}()
			}
			if result.State == "unavailable" {
				status = http.StatusServiceUnavailable
			}
		}
		if r.Context().Err() != nil {
			return
		}
		if err != nil {
			WriteError(w, err)
			return
		}
		guideJSON(w, r, releases, h, status, result)
	})
}

func guideStream(w http.ResponseWriter, r *http.Request, releases *release.Manager, h *release.Handle, agent *guide.Agent, req contract.ConsultRequest) {
	stream := guide.NewLiveUIMessageStream(r.Context(), w, func() error {
		return releases.BeginResponse(h.ID(), func() error {
			w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
			w.Header().Set("x-vercel-ai-ui-message-stream", "v1")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Accel-Buffering", "no")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.WriteHeader(http.StatusOK)
			return nil
		})
	})
	defer stream.Close()
	result, err := agent.ConsultStream(r.Context(), h, req, func(delta string) error {
		if _, e := releases.Capture(h.ID()); e != nil {
			return e
		}
		return stream.Append(delta)
	})
	if r.Context().Err() != nil {
		return
	}
	// A publication switch during generation cannot commit old product cards.
	if _, e := releases.Capture(h.ID()); e != nil {
		err = e
	}
	if err != nil || result.State == "unavailable" {
		if stream.Started() {
			stream.Fail()
		} else if err != nil {
			WriteError(w, err)
		} else {
			guideJSON(w, r, releases, h, http.StatusServiceUnavailable, result)
		}
		return
	}
	if err = stream.Finish(result); err != nil {
		if stream.Started() {
			stream.Fail()
		} else {
			WriteError(w, err)
		}
	}
}

func acceptsGuideStream(accept string) bool {
	// Explicit JSON wins when a client advertises both supported representations.
	for _, part := range strings.Split(accept, ",") {
		if strings.TrimSpace(strings.SplitN(part, ";", 2)[0]) == "application/json" {
			return false
		}
	}
	for _, part := range strings.Split(accept, ",") {
		if strings.TrimSpace(part) == "text/event-stream" {
			return true
		}
	}
	return false
}

// Serialize before BeginResponse, commit headers under the short publication
// read lock, and write the body after unlocking. A switch during computation
// returns a real 409; a response already begun keeps its captured release ID.
func guideJSON(w http.ResponseWriter, r *http.Request, releases *release.Manager, h *release.Handle, status int, result contract.GuideResult) {
	b, err := json.Marshal(result)
	if err != nil {
		WriteError(w, err)
		return
	}
	if err = contract.ValidateJSON("guide.schema.json#/$defs/GuideResult", b); err != nil {
		WriteError(w, err)
		return
	}
	if err = r.Context().Err(); err != nil {
		return
	}
	if err = releases.BeginResponse(h.ID(), func() error {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(status)
		return nil
	}); err != nil {
		WriteError(w, err)
		return
	}
	_, _ = w.Write(b)
}
