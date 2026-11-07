package guide_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/guide"
)

func TestStreamCompleteResultAndEscaping(t *testing.T) {
	f := selectionSetup(t)
	out, err := f.service.Select(context.Background(), f.h, contract.SelectionRequest{ReleaseID: f.h.ID(), SelectedProductID: "p_selection", Requirements: f.requirements})
	if err != nil {
		t.Fatal(err)
	}
	out.Selection.Name = "中文\"引号\"\n第二行 {data}"
	w := httptest.NewRecorder()
	if err = guide.WriteUIMessageStream(context.Background(), w, out); err != nil {
		t.Fatal(err)
	}
	events := strings.Split(strings.TrimSpace(w.Body.String()), "\n\n")
	expected := []string{"start", "text-start", "text-delta", "text-end", "data-guide-result", "finish"}
	var textID string
	for i, kind := range expected {
		var e map[string]json.RawMessage
		if err = json.Unmarshal([]byte(strings.TrimPrefix(events[i], "data: ")), &e); err != nil {
			t.Fatal(err)
		}
		var got string
		_ = json.Unmarshal(e["type"], &got)
		if got != kind {
			t.Fatalf("event %d=%s", i, got)
		}
		if i == 1 {
			_ = json.Unmarshal(e["id"], &textID)
		}
		if i == 2 || i == 3 {
			var id string
			_ = json.Unmarshal(e["id"], &id)
			if id != textID || id == "" {
				t.Fatal("text IDs differ")
			}
		}
		if i == 2 {
			var text string
			_ = json.Unmarshal(e["delta"], &text)
			if !strings.Contains(text, out.Selection.Name) {
				t.Fatal("multiline Unicode text lost")
			}
		}
		if i == 4 {
			b, _ := json.Marshal(out)
			if string(e["data"]) != string(b) {
				t.Fatal("JSON/SSE result differs")
			}
		}
	}
	if len(events) != 7 || events[6] != "data: [DONE]" {
		t.Fatal("missing or duplicate finish/data/DONE")
	}
}

type brokenStream struct {
	header http.Header
	body   strings.Builder
	writes int
}

func (w *brokenStream) Header() http.Header { return w.header }
func (w *brokenStream) WriteHeader(int)     {}
func (w *brokenStream) Write(b []byte) (int, error) {
	w.writes++
	if w.writes == 3 {
		return 0, errors.New("private secret")
	}
	return w.body.Write(b)
}
func (w *brokenStream) Flush() {}
func TestStreamWriteFailureCannotFinish(t *testing.T) {
	f := selectionSetup(t)
	out, err := f.service.Select(context.Background(), f.h, contract.SelectionRequest{ReleaseID: f.h.ID(), SelectedProductID: "p_selection", Requirements: f.requirements})
	if err != nil {
		t.Fatal(err)
	}
	w := &brokenStream{header: http.Header{}}
	if guide.WriteUIMessageStream(context.Background(), w, out) == nil {
		t.Fatal("write failure swallowed")
	}
	raw := w.body.String()
	if !strings.Contains(raw, `"type":"error"`) || strings.Contains(raw, "private secret") || strings.Contains(raw, `"type":"finish"`) || strings.Contains(raw, "[DONE]") {
		t.Fatal("sanitized incomplete stream boundary")
	}
}
