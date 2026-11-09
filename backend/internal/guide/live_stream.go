package guide

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"yuanfa-official/backend/internal/contract"
)

// LiveUIMessageStream opens lazily: provider/input failures before the first
// reply retain their ordinary HTTP status. Text is provisional until Finish.
type LiveUIMessageStream struct {
	ctx        context.Context
	w          http.ResponseWriter
	begin      func() error
	started    bool
	failed     bool
	frame      int
	id         string
	text       string
	controller *http.ResponseController
	deadlineMu sync.Mutex
	cancel     context.CancelFunc
	stopCancel func() bool
	cancelDone chan struct{}
	closeOnce  sync.Once
}

func NewLiveUIMessageStream(ctx context.Context, w http.ResponseWriter, begin func() error) *LiveUIMessageStream {
	ctx, cancel := context.WithTimeout(ctx, inferenceTimeout)
	s := &LiveUIMessageStream{ctx: ctx, w: w, begin: begin, id: uuid.NewString(), controller: http.NewResponseController(w), cancel: cancel, cancelDone: make(chan struct{})}
	s.stopCancel = context.AfterFunc(ctx, func() {
		defer close(s.cancelDone)
		s.deadlineMu.Lock()
		defer s.deadlineMu.Unlock()
		_ = s.controller.SetWriteDeadline(time.Now())
	})
	return s
}

// Close joins the cancellation callback before the ResponseWriter can be reused.
func (s *LiveUIMessageStream) Close() {
	s.closeOnce.Do(func() {
		if !s.stopCancel() {
			<-s.cancelDone
		}
		s.cancel()
		_ = s.controller.SetWriteDeadline(time.Time{})
	})
}

func (s *LiveUIMessageStream) Started() bool { return s.started }

func (s *LiveUIMessageStream) start() error {
	if s.started {
		return nil
	}
	if err := s.ctx.Err(); err != nil {
		return err
	}
	if err := s.begin(); err != nil {
		return err
	}
	s.started = true
	if err := s.event(map[string]any{"type": "start", "messageId": s.id}); err != nil {
		return err
	}
	return s.event(map[string]any{"type": "text-start", "id": s.id + "-text"})
}

func (s *LiveUIMessageStream) Append(delta string) error {
	if delta == "" {
		return nil
	}
	if err := s.start(); err != nil {
		return err
	}
	if err := s.event(map[string]any{"type": "text-delta", "id": s.id + "-text", "delta": delta}); err != nil {
		return err
	}
	s.text += delta
	return nil
}

func (s *LiveUIMessageStream) Finish(result contract.GuideResult) error {
	data, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if err = contract.ValidateJSON("guide.schema.json#/$defs/GuideResult", data); err != nil {
		return err
	}
	text := ResultText(result)
	if !strings.HasPrefix(text, s.text) {
		return errors.New("final guide reply differs from preview")
	}
	if err = s.start(); err != nil {
		return err
	}
	if err = s.Append(text[len(s.text):]); err != nil {
		return err
	}
	for _, event := range []any{
		map[string]any{"type": "text-end", "id": s.id + "-text"},
		map[string]any{"type": "data-guide-result", "data": json.RawMessage(data)},
		map[string]any{"type": "finish"},
	} {
		if err = s.event(event); err != nil {
			return err
		}
	}
	return s.write([]byte("data: [DONE]\n\n"))
}

// Fail never exposes provider errors and never emits a successful terminator.
func (s *LiveUIMessageStream) Fail() {
	if !s.started || s.failed || s.ctx.Err() != nil {
		return
	}
	s.failed = true
	_ = s.sendFrame([]byte("data: {\"type\":\"error\",\"errorText\":\"回复未完成，请重新咨询。\"}\n\n"))
}

func (s *LiveUIMessageStream) event(event any) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return s.write(append(append([]byte("data: "), data...), '\n', '\n'))
}

func (s *LiveUIMessageStream) write(frame []byte) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	send, err := streamBoundary(s.ctx, s.frame)
	if err != nil {
		return err
	}
	if !send {
		return errors.New("guide stream interrupted")
	}
	s.frame++
	return s.sendFrame(frame)
}

func (s *LiveUIMessageStream) sendFrame(frame []byte) error {
	s.deadlineMu.Lock()
	if err := s.ctx.Err(); err != nil {
		s.deadlineMu.Unlock()
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	if end, ok := s.ctx.Deadline(); ok && end.Before(deadline) {
		deadline = end
	}
	err := s.controller.SetWriteDeadline(deadline)
	s.deadlineMu.Unlock()
	if err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	defer func() {
		s.deadlineMu.Lock()
		defer s.deadlineMu.Unlock()
		// Do not let the socket deadline expire during idle model generation.
		if s.ctx.Err() == nil {
			_ = s.controller.SetWriteDeadline(time.Time{})
		}
	}()
	if _, err = s.w.Write(frame); err != nil {
		return err
	}
	return s.controller.Flush()
}
