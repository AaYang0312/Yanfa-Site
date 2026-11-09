package guide_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/guide"
	"yuanfa-official/backend/internal/httpapi"
)

const httpStreamMetadata = `{"intent":"clarify","requirements":{"scene_ids":[],"material":null,"substrate":null,"nail_type":null,"fastener_length_mm":null,"air_supply":null,"load_bearing":null,"long_nose":null,"blower":null},"candidate_product_ids":[],"selected_product_id":null,"selection_reference":null,"cleared_requirements":[],"reply":"`
const httpStreamFirst = "你好，我可以帮你选工具。"
const httpStreamLast = "你主要想做什么工作？"

// Only the provider is a double. The real Agent, HTTP handler, stream encoder,
// release manager and TCP client must deliver the first delta before tail opens.
type httpStreamingModel struct {
	tail          chan struct{}
	stopped       chan struct{}
	openOnce      sync.Once
	suffix        string
	streamCalls   atomic.Int64
	generateCalls atomic.Int64
}

func newHTTPStreamingModel() *httpStreamingModel {
	return &httpStreamingModel{tail: make(chan struct{}), stopped: make(chan struct{}), suffix: httpStreamLast + `"}`}
}

func (m *httpStreamingModel) finish() { m.openOnce.Do(func() { close(m.tail) }) }
func (m *httpStreamingModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}
func (m *httpStreamingModel) Generate(ctx context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	m.generateCalls.Add(1)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-m.tail:
		return schema.AssistantMessage(httpStreamMetadata+httpStreamFirst+m.suffix, nil), nil
	}
}
func (m *httpStreamingModel) Stream(ctx context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	m.streamCalls.Add(1)
	reader, writer := schema.Pipe[*schema.Message](3)
	go func() {
		defer close(m.stopped)
		defer writer.Close()
		if writer.Send(schema.AssistantMessage(httpStreamMetadata, nil), nil) || writer.Send(schema.AssistantMessage(httpStreamFirst, nil), nil) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-m.tail:
			writer.Send(schema.AssistantMessage(m.suffix, nil), nil)
		}
	}()
	return reader, nil
}

type httpStreamHarness struct {
	fixture *selectionFixture
	model   *httpStreamingModel
	server  *httptest.Server
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
}

func newHTTPStreamHarness(t *testing.T) *httpStreamHarness {
	t.Helper()
	f := selectionSetup(t)
	m := newHTTPStreamingModel()
	mux := http.NewServeMux()
	httpapi.RegisterGuide(mux, f.releases, f.service, httpapi.GuideOptions{Agent: guide.NewAgent(f.service, m)})
	h := &httpStreamHarness{fixture: f, model: m, done: make(chan struct{})}
	h.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(h.done)
		mux.ServeHTTP(w, r)
	}))
	h.ctx, h.cancel = context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(func() {
		h.cancel()
		m.finish()
		h.server.Close()
	})
	return h
}

func (h *httpStreamHarness) post(t *testing.T, accept string) *http.Response {
	t.Helper()
	body, err := json.Marshal(contract.ConsultRequest{ReleaseID: h.fixture.h.ID(), Message: "你好，帮我选工具"})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(h.ctx, http.MethodPost, h.server.URL+"/api/guide/messages", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", accept)
	response, err := h.server.Client().Do(req)
	if err != nil {
		t.Fatalf("HTTP response was not flushed while the provider tail was blocked: %v", err)
	}
	t.Cleanup(func() { response.Body.Close() })
	return response
}

type httpGuideEvent struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	MessageID string          `json:"messageId"`
	Delta     string          `json:"delta"`
	Data      json.RawMessage `json:"data"`
}

func nextHTTPGuideEvent(scanner *bufio.Scanner) (httpGuideEvent, error) {
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			return httpGuideEvent{}, errors.New("unexpected non-SSE response line: " + line)
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			return httpGuideEvent{Type: "[DONE]"}, nil
		}
		var event httpGuideEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return event, err
		}
		return event, nil
	}
	if err := scanner.Err(); err != nil {
		return httpGuideEvent{}, err
	}
	return httpGuideEvent{}, io.EOF
}

func (h *httpStreamHarness) firstDelta(t *testing.T) (*bufio.Scanner, []httpGuideEvent) {
	t.Helper()
	response := h.post(t, "text/event-stream")
	mediaType, params, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if response.StatusCode != http.StatusOK || err != nil || mediaType != "text/event-stream" || !strings.EqualFold(params["charset"], "utf-8") {
		t.Fatalf("expected UTF-8 SSE response, got %d %q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	if response.Header.Get("X-Accel-Buffering") != "no" || response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("x-vercel-ai-ui-message-stream") != "v1" {
		t.Fatalf("missing streaming/proxy headers: %v", response.Header)
	}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var events []httpGuideEvent
	for {
		event, err := nextHTTPGuideEvent(scanner)
		if err != nil {
			t.Fatalf("initial text was not flushed before provider completion: %v", err)
		}
		events = append(events, event)
		switch event.Type {
		case "start", "text-start":
		case "text-delta":
			if event.Delta == "" || !strings.HasPrefix(httpStreamFirst, event.Delta) {
				t.Fatalf("expected only reply text before the tail: %+v", event)
			}
			if h.model.streamCalls.Load() != 1 || h.model.generateCalls.Load() != 0 {
				t.Fatal("SSE must consume native Stream rather than buffering Generate")
			}
			select {
			case <-h.model.stopped:
				t.Fatal("provider completed before the client received its first delta")
			default:
			}
			return scanner, events
		default:
			t.Fatalf("unexpected terminal or metadata event before the first delta: %+v", event)
		}
	}
}

func drainHTTPGuideEvents(scanner *bufio.Scanner, events []httpGuideEvent) ([]httpGuideEvent, error) {
	for {
		event, err := nextHTTPGuideEvent(scanner)
		if errors.Is(err, io.EOF) {
			return events, nil
		}
		if err != nil {
			return events, err
		}
		events = append(events, event)
	}
}

func assertNoHTTPSuccess(t *testing.T, events []httpGuideEvent) {
	t.Helper()
	for _, event := range events {
		if event.Type == "data-guide-result" || event.Type == "finish" || event.Type == "[DONE]" {
			t.Fatalf("incomplete stream committed a successful terminal event: %+v", event)
		}
	}
}

func TestHTTPGuideNativeStreamFlushesBeforeTailAndFinishesOnce(t *testing.T) {
	h := newHTTPStreamHarness(t)
	scanner, events := h.firstDelta(t)
	h.model.finish() // The real network reader, not a timer, releases the tail.
	events, err := drainHTTPGuideEvents(scanner, events)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	var text strings.Builder
	var textID string
	var resultData json.RawMessage
	for _, event := range events {
		counts[event.Type]++
		switch event.Type {
		case "start":
			if event.MessageID == "" {
				t.Fatal("missing message ID")
			}
		case "text-start":
			textID = event.ID
		case "text-delta", "text-end":
			if textID == "" || event.ID != textID {
				t.Fatalf("text event IDs changed: %+v", event)
			}
			text.WriteString(event.Delta)
		case "data-guide-result":
			resultData = event.Data
		case "finish", "[DONE]":
		default:
			t.Fatalf("unexpected success-stream event: %+v", event)
		}
	}
	for _, kind := range []string{"start", "text-start", "text-end", "data-guide-result", "finish", "[DONE]"} {
		if counts[kind] != 1 {
			t.Fatalf("expected exactly one %s, counts=%v", kind, counts)
		}
	}
	if counts["text-delta"] < 2 || text.String() != httpStreamFirst+httpStreamLast {
		t.Fatalf("reply was buffered, duplicated or truncated: %q, counts=%v", text.String(), counts)
	}
	for i, kind := range []string{"text-end", "data-guide-result", "finish", "[DONE]"} {
		if events[len(events)-4+i].Type != kind {
			t.Fatalf("terminal events out of order: %+v", events)
		}
	}
	out, err := contract.Decode[contract.GuideResult]("guide.schema.json#/$defs/GuideResult", resultData)
	if err != nil || out.ReleaseID != h.fixture.h.ID() || out.State != "clarifying" || out.Reply == nil || *out.Reply != text.String() || out.Selection != nil || len(out.Recommendations) != 0 {
		t.Fatalf("invalid final result: %+v %v", out, err)
	}
}

func TestHTTPGuideCancelledStreamHasNoSuccessfulFinalResult(t *testing.T) {
	h := newHTTPStreamHarness(t)
	scanner, events := h.firstDelta(t)
	h.cancel() // Tail stays blocked: only HTTP cancellation can stop the model.
	events, err := drainHTTPGuideEvents(scanner, events)
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected cancellation error: %v", err)
	}
	assertNoHTTPSuccess(t, events)
	for name, done := range map[string]<-chan struct{}{"provider": h.model.stopped, "handler": h.done} {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("HTTP cancellation did not drain %s while tail was blocked", name)
		}
	}
}

func TestHTTPGuideMalformedTailHasNoSuccessfulFinalResult(t *testing.T) {
	h := newHTTPStreamHarness(t)
	h.model.suffix = `\q"}` // Invalid JSON escape after a visible, valid reply prefix.
	scanner, events := h.firstDelta(t)
	h.model.finish()
	events, err := drainHTTPGuideEvents(scanner, events)
	if err != nil {
		t.Fatal(err)
	}
	assertNoHTTPSuccess(t, events)
	errorsSeen := 0
	for _, event := range events {
		if event.Type == "error" {
			errorsSeen++
		}
	}
	if errorsSeen != 1 {
		t.Fatalf("malformed tail must emit one SSE error, got %+v", events)
	}
}

func TestHTTPGuideReleaseSwitchDuringStreamCannotCommitOldResult(t *testing.T) {
	h := newHTTPStreamHarness(t)
	scanner, events := h.firstDelta(t)
	next := h.fixture.load(t, "http_stream_new_release")
	prepared, err := h.fixture.releases.Prepare(next, "http_stream_switch")
	if err != nil {
		t.Fatal(err)
	}
	activated := make(chan error, 1)
	go func() {
		h.fixture.gate.Lock()
		_, err := h.fixture.releases.ActivateLocked(h.ctx, prepared)
		h.fixture.gate.Unlock()
		activated <- err
	}()
	select {
	case err := <-activated:
		if err != nil {
			t.Fatal(err)
		}
	case <-h.ctx.Done():
		t.Fatal("stream held the release lock while waiting for the provider tail")
	}
	h.model.finish()
	events, err = drainHTTPGuideEvents(scanner, events)
	if err != nil {
		t.Fatal(err)
	}
	assertNoHTTPSuccess(t, events)
	errorsSeen := 0
	for _, event := range events {
		if event.Type == "error" {
			errorsSeen++
		}
	}
	if errorsSeen != 1 {
		t.Fatalf("release switch must terminate with an SSE error: %+v", events)
	}
}

func TestHTTPGuideJSONAcceptRemainsACompleteResponse(t *testing.T) {
	for _, accept := range []string{"application/json", "text/event-stream, application/json"} {
		t.Run(accept, func(t *testing.T) {
			h := newHTTPStreamHarness(t)
			h.model.finish()
			response := h.post(t, accept)
			mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
			if err != nil || response.StatusCode != http.StatusOK || mediaType != "application/json" {
				t.Fatalf("JSON negotiation changed: %d %q", response.StatusCode, response.Header.Get("Content-Type"))
			}
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			out, err := contract.Decode[contract.GuideResult]("guide.schema.json#/$defs/GuideResult", body)
			if err != nil || out.State != "clarifying" || out.Reply == nil || *out.Reply != httpStreamFirst+httpStreamLast || out.ReleaseID != h.fixture.h.ID() {
				t.Fatalf("incomplete JSON result: %+v %v", out, err)
			}
			if h.model.streamCalls.Load() != 0 || h.model.generateCalls.Load() != 1 {
				t.Fatal("JSON Accept must retain the non-streaming consultation path")
			}
		})
	}
}
