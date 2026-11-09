package guide_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/guide"
)

// Exercise the real SDK and agent against a provider boundary, including a
// function result and preserved reasoning on the second completion request.
func TestModelAdapterCatalogToolRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name      string
		jsonMode  bool
		reasoning string
	}{
		{"glm-5.3-flash", false, "low"},
		{"existing-compatible-model", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := selectionSetup(t)
			requirements := contract.UnknownRequirements()
			var calls atomic.Int64
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var body struct {
					Model           string            `json:"model"`
					ResponseFormat  json.RawMessage   `json:"response_format"`
					ReasoningEffort string            `json:"reasoning_effort"`
					Tools           []json.RawMessage `json:"tools"`
					Messages        []struct {
						Role             string `json:"role"`
						ReasoningContent string `json:"reasoning_content"`
					} `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer local-test-key" || body.Model != tc.name || len(body.Tools) != 2 {
					t.Error("incorrect completion endpoint, authentication, model, or public tool set")
				}
				hasJSONMode := len(body.ResponseFormat) > 0 && string(body.ResponseFormat) != "null"
				if hasJSONMode != tc.jsonMode || body.ReasoningEffort != tc.reasoning {
					t.Errorf("provider parameters: json_mode=%v reasoning=%q; want %v %q", hasJSONMode, body.ReasoningEffort, tc.jsonMode, tc.reasoning)
					w.WriteHeader(400)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				hasTool, hasReasoning := false, false
				for _, message := range body.Messages {
					hasTool = hasTool || message.Role == "tool"
					hasReasoning = hasReasoning || message.Role == "assistant" && message.ReasoningContent == "provider-test-context"
				}
				message := map[string]any{"role": "assistant", "content": "", "reasoning_content": "provider-test-context", "tool_calls": []any{map[string]any{"id": "read-product", "type": "function", "function": map[string]any{"name": "search_products", "arguments": `{"q":"SELECTION"}`}}}}
				finish := "tool_calls"
				if hasTool {
					if !hasReasoning {
						t.Error("provider reasoning context was lost across the tool round trip")
					}
					message = map[string]any{"role": "assistant", "content": proposal(t, requirements, []string{"p_selection"}, nil)}
					finish = "stop"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"id": "provider-test", "model": tc.name, "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finish}}})
			}))
			defer provider.Close()
			chatModel, err := guide.NewModel(context.Background(), provider.URL, tc.name, "local-test-key")
			if err != nil {
				t.Fatal(err)
			}
			out, err := guide.NewAgent(f.service, chatModel).Consult(context.Background(), f.h, contract.ConsultRequest{ReleaseID: f.h.ID(), Message: "介绍一下SELECTION", Requirements: &requirements})
			if err != nil || out.State != "recommendations" || len(out.Recommendations) != 1 || calls.Load() != 2 {
				t.Fatalf("catalog tool round trip failed: state=%s recommendations=%d calls=%d err=%v", out.State, len(out.Recommendations), calls.Load(), err)
			}
		})
	}
}
