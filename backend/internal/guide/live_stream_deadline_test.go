package guide_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/guide"
)

type liveDeadlineEvent struct {
	kind, payload string
	at, deadline  time.Time
}

// This writer models the ResponseController contract: updating a write deadline
// also wakes an already-blocked FlushError, including when the deadline expires.
// All observations are synchronized so cancellation/Close tests can run with -race.
type liveDeadlineWriter struct {
	header       http.Header
	mu           sync.Mutex
	deadline     time.Time
	events       []liveDeadlineEvent
	changed      chan struct{}
	blockFlush   bool
	flushEntered chan struct{}
	pastEntered  chan struct{}
	releasePast  chan struct{}
	pastOnce     sync.Once
}

func newLiveDeadlineWriter() *liveDeadlineWriter {
	return &liveDeadlineWriter{header: make(http.Header), changed: make(chan struct{}), flushEntered: make(chan struct{}, 1)}
}
func (w *liveDeadlineWriter) Header() http.Header { return w.header }
func (w *liveDeadlineWriter) WriteHeader(int)     {}
func (w *liveDeadlineWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.events = append(w.events, liveDeadlineEvent{kind: "write", payload: string(data), at: time.Now(), deadline: w.deadline})
	if !w.deadline.IsZero() && !time.Now().Before(w.deadline) {
		return 0, os.ErrDeadlineExceeded
	}
	return len(data), nil
}
func (w *liveDeadlineWriter) SetWriteDeadline(deadline time.Time) error {
	w.mu.Lock()
	at := time.Now()
	w.deadline = deadline
	w.events = append(w.events, liveDeadlineEvent{kind: "deadline", at: at, deadline: deadline})
	close(w.changed)
	w.changed = make(chan struct{})
	pastEntered, releasePast := w.pastEntered, w.releasePast
	w.mu.Unlock()
	if pastEntered != nil && !deadline.IsZero() && !deadline.After(at) {
		w.pastOnce.Do(func() { close(pastEntered) })
		<-releasePast
	}
	return nil
}
func (w *liveDeadlineWriter) FlushError() error {
	w.mu.Lock()
	w.events = append(w.events, liveDeadlineEvent{kind: "flush", at: time.Now(), deadline: w.deadline})
	block := w.blockFlush
	w.mu.Unlock()
	if !block {
		return nil
	}
	select {
	case w.flushEntered <- struct{}{}:
	default:
	}
	for {
		w.mu.Lock()
		deadline, changed := w.deadline, w.changed
		w.mu.Unlock()
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			return os.ErrDeadlineExceeded
		}
		var timer *time.Timer
		var expires <-chan time.Time
		if !deadline.IsZero() {
			timer = time.NewTimer(time.Until(deadline))
			expires = timer.C
		}
		select {
		case <-changed:
			if timer != nil {
				timer.Stop()
			}
		case <-expires:
			return os.ErrDeadlineExceeded
		}
	}
}
func (w *liveDeadlineWriter) snapshot() ([]liveDeadlineEvent, time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]liveDeadlineEvent(nil), w.events...), w.deadline
}
func (w *liveDeadlineWriter) blockNextFlush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.blockFlush = true
}
func deadlineStream(ctx context.Context, w *liveDeadlineWriter) *guide.LiveUIMessageStream {
	return guide.NewLiveUIMessageStream(ctx, w, func() error { w.WriteHeader(http.StatusOK); return nil })
}
func awaitDeadlineSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}
func assertLiveFrameDeadlines(t *testing.T, events []liveDeadlineEvent) []string {
	t.Helper()
	var armed, wrote, flushed bool
	var frames []string
	for _, event := range events {
		switch event.kind {
		case "deadline":
			if event.deadline.IsZero() {
				if wrote && !flushed {
					t.Fatal("write deadline cleared before flush completed")
				}
				armed, wrote, flushed = false, false, false
				continue
			}
			remaining := event.deadline.Sub(event.at)
			if remaining < 4*time.Second || remaining > 5*time.Second {
				t.Fatalf("frame needs a fresh five-second budget, got %s", remaining)
			}
			armed = true
		case "write":
			if !armed || wrote || event.deadline.IsZero() || !event.deadline.After(event.at) {
				t.Fatal("frame written without a fresh deadline")
			}
			wrote = true
			frames = append(frames, event.payload)
		case "flush":
			if !armed || !wrote || event.deadline.IsZero() || !event.deadline.After(event.at) {
				t.Fatal("flush executed without the frame deadline")
			}
			flushed = true
		}
	}
	if armed || wrote || flushed {
		t.Fatal("idle generation retained a socket deadline")
	}
	return frames
}

func TestLiveStreamDeadlineEveryFrame(t *testing.T) {
	w := newLiveDeadlineWriter()
	s := deadlineStream(context.Background(), w)
	defer s.Close()
	if err := s.Append("首段"); err != nil {
		t.Fatal(err)
	}
	if err := s.Append("后段"); err != nil {
		t.Fatal(err)
	}
	reply := "首段后段"
	result := contract.GuideResult{
		ReleaseID: "r_test", State: "clarifying", Reply: &reply,
		ConfirmedRequirements: contract.UnknownRequirements(), Conflicts: []string{},
		Recommendations: []contract.GuideRecommendation{}, MissingFields: []contract.MissingField{}, ContactHref: "tel:123456",
	}
	if err := s.Finish(result); err != nil {
		t.Fatal(err)
	}
	events, deadline := w.snapshot()
	frames := assertLiveFrameDeadlines(t, events)
	if len(frames) != 8 || !strings.Contains(frames[0], "\"type\":\"start\"") || frames[len(frames)-1] != "data: [DONE]\n\n" {
		t.Fatalf("unexpected frame sequence: %v", frames)
	}
	if !deadline.IsZero() {
		t.Fatal("completed frames did not clear their idle deadline")
	}
}

func TestLiveStreamDeadlineFailFrame(t *testing.T) {
	w := newLiveDeadlineWriter()
	s := deadlineStream(context.Background(), w)
	defer s.Close()
	if err := s.Append("部分回复"); err != nil {
		t.Fatal(err)
	}
	s.Fail()
	events, _ := w.snapshot()
	frames := assertLiveFrameDeadlines(t, events)
	body := strings.Join(frames, "")
	if len(frames) != 4 || strings.Count(body, "\"type\":\"error\"") != 1 || strings.Contains(body, "[DONE]") {
		t.Fatalf("unexpected failure stream: %s", body)
	}
	s.Fail()
	after, _ := w.snapshot()
	if len(after) != len(events) {
		t.Fatal("Fail wrote more than one failure frame")
	}
}

func TestLiveStreamDeadlineCancellationUnblocksFlush(t *testing.T) {
	for _, operation := range []string{"append", "fail"} {
		t.Run(operation, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			w := newLiveDeadlineWriter()
			s := deadlineStream(ctx, w)
			defer s.Close()
			if err := s.Append("开始"); err != nil {
				t.Fatal(err)
			}
			w.blockNextFlush()
			done := make(chan error, 1)
			go func() {
				if operation == "append" {
					done <- s.Append("下一段")
				} else {
					s.Fail()
					done <- nil
				}
			}()
			awaitDeadlineSignal(t, w.flushEntered, "blocked flush")
			cancel()
			select {
			case err := <-done:
				if operation == "append" && !errors.Is(err, os.ErrDeadlineExceeded) {
					t.Fatalf("blocked append must report deadline failure, got %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("context cancellation did not interrupt flush")
			}
			events, deadline := w.snapshot()
			if deadline.IsZero() || deadline.After(time.Now()) {
				t.Fatal("idle cleanup overwrote cancellation's expired deadline")
			}
			var forced bool
			for _, event := range events {
				if event.kind == "deadline" && !event.deadline.IsZero() && !event.deadline.After(event.at) {
					forced = true
				}
			}
			if !forced {
				t.Fatal("cancellation never forced an immediate socket deadline")
			}
			s.Close()
			_, deadline = w.snapshot()
			if !deadline.IsZero() {
				t.Fatal("Close did not reset deadline for the next response")
			}
		})
	}
}

func TestLiveStreamDeadlineClampedToContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	end, _ := ctx.Deadline()
	w := newLiveDeadlineWriter()
	w.blockNextFlush()
	s := deadlineStream(ctx, w)
	defer s.Close()
	done := make(chan error, 1)
	go func() { done <- s.Append("受限回复") }()
	awaitDeadlineSignal(t, w.flushEntered, "context-limited flush")
	events, _ := w.snapshot()
	var armed bool
	for _, event := range events {
		if event.kind == "deadline" && !event.deadline.IsZero() {
			armed = true
			if event.deadline.After(end) {
				t.Fatal("frame deadline extends beyond the request context")
			}
		}
	}
	if !armed {
		t.Fatal("no write deadline was installed")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expired context left flush successful")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("context deadline did not unblock flush")
	}
}

func TestLiveStreamDeadlineCloseCleanup(t *testing.T) {
	t.Run("stops pending cancellation and is idempotent", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		w := newLiveDeadlineWriter()
		s := deadlineStream(ctx, w)
		if err := s.Append("完成"); err != nil {
			t.Fatal(err)
		}
		s.Close()
		before, deadline := w.snapshot()
		if !deadline.IsZero() {
			t.Fatal("Close retained a write deadline")
		}
		cancel()
		s.Close()
		// Give a wrongly retained AfterFunc a chance to corrupt the reused writer.
		time.Sleep(20 * time.Millisecond)
		after, deadline := w.snapshot()
		if len(after) != len(before) || !deadline.IsZero() {
			t.Fatal("a cancellation callback touched the writer after Close")
		}
	})
	t.Run("joins an already running cancellation callback", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		w := newLiveDeadlineWriter()
		w.pastEntered, w.releasePast = make(chan struct{}), make(chan struct{})
		var releaseOnce sync.Once
		release := func() { releaseOnce.Do(func() { close(w.releasePast) }) }
		defer release()
		s := deadlineStream(ctx, w)
		cancel()
		awaitDeadlineSignal(t, w.pastEntered, "running cancellation callback")
		closing, closed := make(chan struct{}), make(chan struct{})
		go func() { close(closing); s.Close(); close(closed) }()
		awaitDeadlineSignal(t, closing, "Close invocation")
		select {
		case <-closed:
			t.Fatal("Close returned before the callback completed")
		case <-time.After(20 * time.Millisecond):
		}
		release()
		awaitDeadlineSignal(t, closed, "Close joining callback")
		events, deadline := w.snapshot()
		if !deadline.IsZero() || len(events) < 2 || !events[len(events)-1].deadline.IsZero() {
			t.Fatal("callback was not joined before the final deadline reset")
		}
		s.Close()
		after, _ := w.snapshot()
		if len(after) != len(events) {
			t.Fatal("repeated Close changed deadline state")
		}
	})
}
