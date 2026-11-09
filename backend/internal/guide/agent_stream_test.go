package guide_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/guide"
)

const streamTestTimeout = 5 * time.Second

// Only the provider is faked: the real Eino agent, read tools, catalog and
// deterministic proposal checks run against selectionSetup's temporary release.
type streamScript struct {
	head, tail    string
	needsTool     bool
	tailGate      chan struct{}
	stopped       chan struct{}
	generateCalls atomic.Int64
	nativeCalls   atomic.Int64
	bindings      atomic.Int64
	finalCalls    atomic.Int64
	sawToolResult atomic.Bool
}

type pipeConsultModel struct {
	script *streamScript
	tools  []*schema.ToolInfo
}

func newPipeConsultModel(head, tail string, needsTool bool) *pipeConsultModel {
	return &pipeConsultModel{script: &streamScript{
		head: head, tail: tail, needsTool: needsTool,
		tailGate: make(chan struct{}), stopped: make(chan struct{}),
	}}
}

func streamToolsValid(tools []*schema.ToolInfo) error {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	if len(names) != 2 || !slices.Contains(names, "search_products") || !slices.Contains(names, "get_product") {
		return errors.New("streaming agent did not bind the two public read tools")
	}
	return nil
}

func (m *pipeConsultModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	if err := streamToolsValid(tools); err != nil {
		return nil, err
	}
	return &pipeConsultModel{script: m.script, tools: slices.Clone(tools)}, nil
}

func (m *pipeConsultModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	m.script.generateCalls.Add(1)
	return nil, errors.New("ConsultStream must use native Stream, never Generate")
}

func (m *pipeConsultModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	m.script.nativeCalls.Add(1)
	// Eino v0.9.22 supplies model.WithTools at call time. Validate what the
	// provider actually receives, while also supporting instance binding.
	options := model.GetCommonOptions(&model.Options{Tools: m.tools}, opts...)
	if err := streamToolsValid(options.Tools); err != nil {
		return nil, err
	}
	m.script.bindings.Add(1)
	hasTool := false
	for _, msg := range input {
		if msg.Role == schema.Tool {
			hasTool = true
			if strings.Contains(msg.Content, "p_selection") {
				m.script.sawToolResult.Store(true)
			}
		}
	}
	if m.script.needsTool && !hasTool {
		return schema.StreamReaderFromArray([]*schema.Message{{
			Role: schema.Assistant, ReasoningContent: "PRIVATE_TOOL_REASONING",
			ToolCalls: []schema.ToolCall{{
				Index: new(0), ID: "private-stream-read",
				Function: schema.FunctionCall{Name: "search_products", Arguments: "{}"},
			}},
		}}), nil
	}
	if m.script.finalCalls.Add(1) != 1 {
		return nil, errors.New("unexpected extra final inference")
	}
	// Capacity covers every scripted send, including the cancellation error,
	// so a failed assertion cannot strand the provider in writer.Send.
	reader, writer := schema.Pipe[*schema.Message](3)
	go func() {
		defer close(m.script.stopped)
		defer writer.Close()
		if writer.Send(&schema.Message{
			Role: schema.Assistant, ReasoningContent: `{"reply":"PRIVATE_FINAL_REASONING"}`,
		}, nil) {
			return
		}
		if writer.Send(schema.AssistantMessage(m.script.head, nil), nil) {
			return
		}
		select {
		case <-ctx.Done():
			writer.Send(nil, ctx.Err())
			return
		case <-m.script.tailGate:
			writer.Send(schema.AssistantMessage(m.script.tail, nil), nil)
		}
	}()
	return reader, nil
}

// The envelope's complete metadata (including the explicitly empty clearances)
// precedes reply. Its opening Chinese text is sent before the blocked tail.
func streamEnvelope(t *testing.T, requirements contract.GuideRequirements, ids []string, intent, first, rest string) (string, string) {
	t.Helper()
	metadata := struct {
		Intent              string                               `json:"intent"`
		Requirements        contract.GuideRequirements           `json:"requirements"`
		CandidateProductIDs []string                             `json:"candidate_product_ids"`
		SelectedProductID   *string                              `json:"selected_product_id"`
		SelectionReference  *string                              `json:"selection_reference"`
		ClearedRequirements []contract.GuideRequirementClearance `json:"cleared_requirements"`
	}{intent, requirements, ids, nil, nil, []contract.GuideRequirementClearance{}}
	data, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	restJSON, err := json.Marshal(rest)
	if err != nil {
		t.Fatal(err)
	}
	head := string(data[:len(data)-1]) + `,"reply":"` + string(firstJSON[1:len(firstJSON)-1])
	tail := string(restJSON[1:len(restJSON)-1]) + `"}`
	if err := contract.ValidateJSON("guide.schema.json#/$defs/GuideModelProposal", []byte(head+tail)); err != nil {
		t.Fatalf("invalid provider fixture: %v", err)
	}
	return head, tail
}

type streamOutcome struct {
	result contract.GuideResult
	err    error
}

func startConsultStream(t *testing.T, f *selectionFixture, m *pipeConsultModel, req contract.ConsultRequest, onDelta func(string) error) (context.CancelFunc, <-chan streamOutcome) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("provider diagnostics: Generate=%d Stream=%d validToolBindings=%d final=%d toolResult=%v", m.script.generateCalls.Load(), m.script.nativeCalls.Load(), m.script.bindings.Load(), m.script.finalCalls.Load(), m.script.sawToolResult.Load())
		}
	})
	done := make(chan streamOutcome, 1)
	go func() {
		out, err := guide.NewAgent(f.service, m).ConsultStream(ctx, f.h, req, onDelta)
		done <- streamOutcome{out, err}
	}()
	return cancel, done
}

func awaitStreamPreview(t *testing.T, deltas <-chan string, done <-chan streamOutcome, want string) string {
	t.Helper()
	timer := time.NewTimer(streamTestTimeout)
	defer timer.Stop()
	got := ""
	for got != want {
		select {
		case delta := <-deltas:
			got += delta
			if !strings.HasPrefix(want, got) {
				t.Fatalf("preview leaked non-reply content: got %q, want prefix of %q", got, want)
			}
		case out := <-done:
			t.Fatalf("ConsultStream finished before the blocked provider tail: %+v, %v", out.result, out.err)
		case <-timer.C:
			t.Fatal("no Chinese reply preview while the provider tail is blocked")
		}
	}
	return got
}

func awaitStreamResult(t *testing.T, done <-chan streamOutcome) streamOutcome {
	t.Helper()
	select {
	case out := <-done:
		return out
	case <-time.After(streamTestTimeout):
		t.Fatal("ConsultStream did not return after completion/cancellation")
		return streamOutcome{}
	}
}

func remainingStreamText(deltas <-chan string, prefix string) string {
	for {
		select {
		case delta := <-deltas:
			prefix += delta
		default:
			return prefix
		}
	}
}

func assertNativeStream(t *testing.T, m *pipeConsultModel, calls int64) {
	t.Helper()
	if m.script.generateCalls.Load() != 0 || m.script.nativeCalls.Load() != calls || m.script.bindings.Load() != calls {
		t.Fatalf("provider path: Generate=%d Stream=%d validToolBindings=%d", m.script.generateCalls.Load(), m.script.nativeCalls.Load(), m.script.bindings.Load())
	}
	if m.script.needsTool && !m.script.sawToolResult.Load() {
		t.Fatal("consultation never consumed the real catalog tool result")
	}
	select {
	case <-m.script.stopped:
	case <-time.After(streamTestTimeout):
		t.Fatal("provider pipe did not stop")
	}
}

func assertStreamUncommitted(t *testing.T, out contract.GuideResult, prior contract.GuideRequirements) {
	t.Helper()
	if out.State != "unavailable" || out.Reply != nil || out.Selection != nil || len(out.Recommendations) != 0 {
		t.Fatalf("failed stream committed reply/cards: %+v", out)
	}
	if !reflect.DeepEqual(out.ConfirmedRequirements, prior) {
		t.Fatalf("failed stream committed proposed conditions: got %+v, prior %+v", out.ConfirmedRequirements, prior)
	}
}

func TestConsultStreamPreviewsReplyBeforeProviderFinishes(t *testing.T) {
	for _, intent := range []string{"clarify", "consult"} {
		t.Run(intent, func(t *testing.T) {
			f := selectionSetup(t)
			ids := []string{}
			calls := int64(1)
			wantState := "clarifying"
			if intent == "consult" {
				ids, calls, wantState = []string{"p_selection"}, 2, "recommendations"
			}
			first, rest := "这款", "可以先了解一下。\n你想固定\"木条\"还是板材？"
			head, tail := streamEnvelope(t, f.requirements, ids, intent, first, rest)
			m := newPipeConsultModel(head, tail, intent == "consult")
			req := contract.ConsultRequest{ReleaseID: f.h.ID(), Message: "帮我了解一下木工工具", Requirements: &f.requirements}
			deltas := make(chan string, 128)
			_, done := startConsultStream(t, f, m, req, func(delta string) error {
				deltas <- delta
				return nil
			})
			preview := awaitStreamPreview(t, deltas, done, first)
			select {
			case <-m.script.stopped:
				t.Fatal("provider finished before its tail was released")
			default:
			}
			close(m.script.tailGate)
			got := awaitStreamResult(t, done)
			if got.err != nil || got.result.State != wantState || len(got.result.Recommendations) != len(ids) {
				t.Fatalf("unexpected final contract: %+v, %v", got.result, got.err)
			}
			if text := remainingStreamText(deltas, preview); text != first+rest || guide.ResultText(got.result) != text {
				t.Fatalf("preview/final text mismatch or private content leak: preview=%q final=%q", text, guide.ResultText(got.result))
			}
			if intent == "consult" && got.result.Recommendations[0].ProductID != "p_selection" {
				t.Fatalf("wrong public candidate: %+v", got.result.Recommendations)
			}
			data, err := json.Marshal(got.result)
			if err != nil {
				t.Fatal(err)
			}
			if err := contract.ValidateJSON("guide.schema.json#/$defs/GuideResult", data); err != nil {
				t.Fatal(err)
			}
			// The existing non-streaming entry point must still resolve exactly
			// the same proposal into the same public JSON contract.
			jsonModel := &conversationalModel{consultModel: &consultModel{proposal: head + tail}, direct: intent == "clarify"}
			jsonResult, err := guide.NewAgent(f.service, jsonModel).Consult(context.Background(), f.h, req)
			if err != nil || !reflect.DeepEqual(got.result, jsonResult) {
				t.Fatalf("streaming changed Consult's final result: stream=%+v JSON=%+v err=%v", got.result, jsonResult, err)
			}
			assertNativeStream(t, m, calls)
		})
	}
}

func TestConsultStreamCancellationDiscardsPreviewAndProposedConditions(t *testing.T) {
	f := selectionSetup(t)
	prior := f.requirements
	prior.AirSupply = new(false)
	proposed := prior
	proposed.AirSupply = new(true)
	head, tail := streamEnvelope(t, proposed, []string{}, "clarify", "你好", "，准备做什么？")
	m := newPipeConsultModel(head, tail, false)
	req := contract.ConsultRequest{ReleaseID: f.h.ID(), Message: "现在有气源了", Requirements: &prior}
	deltas := make(chan string, 128)
	cancel, done := startConsultStream(t, f, m, req, func(delta string) error {
		deltas <- delta
		return nil
	})
	preview := awaitStreamPreview(t, deltas, done, "你好")
	cancel() // The provider gate stays closed; only cancellation can unblock it.
	got := awaitStreamResult(t, done)
	if !errors.Is(got.err, context.Canceled) {
		t.Fatalf("parent cancellation must return context.Canceled, got %v", got.err)
	}
	assertStreamUncommitted(t, got.result, prior)
	if text := remainingStreamText(deltas, preview); text != "你好" {
		t.Fatalf("emitted content after cancellation: %q", text)
	}
	assertNativeStream(t, m, 1)
}

func TestConsultStreamMalformedFinalDiscardsPreviewAndProposedConditions(t *testing.T) {
	for _, test := range []struct{ name, tail string }{
		{"trailing_garbage", `"} invalid suffix`},
		{"truncated_json", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := selectionSetup(t)
			prior := f.requirements
			prior.AirSupply = new(false)
			proposed := prior
			proposed.AirSupply = new(true)
			head, _ := streamEnvelope(t, proposed, []string{}, "clarify", "你好", "，准备做什么？")
			m := newPipeConsultModel(head, test.tail, false)
			req := contract.ConsultRequest{ReleaseID: f.h.ID(), Message: "现在有气源了", Requirements: &prior}
			deltas := make(chan string, 128)
			_, done := startConsultStream(t, f, m, req, func(delta string) error {
				deltas <- delta
				return nil
			})
			preview := awaitStreamPreview(t, deltas, done, "你好")
			close(m.script.tailGate)
			got := awaitStreamResult(t, done)
			if got.err != nil {
				t.Fatalf("internal malformed proposal must become unavailable, got %v", got.err)
			}
			assertStreamUncommitted(t, got.result, prior)
			if text := remainingStreamText(deltas, preview); text != "你好" {
				t.Fatalf("malformed suffix leaked into reply: %q", text)
			}
			assertNativeStream(t, m, 1)
		})
	}
}

func TestConsultStreamCallbackFailureCancelsProviderWithoutCommit(t *testing.T) {
	f := selectionSetup(t)
	head, tail := streamEnvelope(t, f.requirements, []string{}, "clarify", "你好", "，准备做什么？")
	m := newPipeConsultModel(head, tail, false)
	req := contract.ConsultRequest{ReleaseID: f.h.ID(), Message: "帮我选工具", Requirements: &f.requirements}
	var callbacks atomic.Int64
	_, done := startConsultStream(t, f, m, req, func(string) error {
		callbacks.Add(1)
		return errors.New("downstream stopped accepting deltas")
	})
	got := awaitStreamResult(t, done)
	if got.err != nil || callbacks.Load() != 1 {
		t.Fatalf("callback failure must abort once as unavailable: callbacks=%d err=%v", callbacks.Load(), got.err)
	}
	assertStreamUncommitted(t, got.result, f.requirements)
	assertNativeStream(t, m, 1)
}

func TestConsultStreamHardRejectedCandidateNeverPreviewsUnsafeReply(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		name := "all_rejected"
		if mixed {
			name = "partially_rejected"
		}
		t.Run(name, func(t *testing.T) {
			f := selectionSetup(t)
			ids := []string{"p_selection"}
			if mixed {
				ids = append(ids, "p_unknown")
			} else {
				f.snapshot.Products = f.snapshot.Products[:1]
				f.h = f.load(t, "stream_conflict")
				f.activate(t, f.h, "stream_conflict_operation")
			}
			f.requirements.AirSupply = new(false)
			head, tail := streamEnvelope(t, f.requirements, ids, "consult", "无需气源", "，SELECTION可以直接使用。")
			m := newPipeConsultModel(head, tail, true)
			close(m.script.tailGate)
			req := contract.ConsultRequest{ReleaseID: f.h.ID(), Message: "没有气源，帮我推荐", Requirements: &f.requirements}
			deltas := make(chan string, 128)
			_, done := startConsultStream(t, f, m, req, func(delta string) error {
				deltas <- delta
				return nil
			})
			got := awaitStreamResult(t, done)
			if got.err != nil || got.result.Reply != nil || remainingStreamText(deltas, "") != "" {
				t.Fatalf("rejected candidate emitted unsafe reply: %+v, %v", got.result, got.err)
			}
			if mixed {
				if len(got.result.Recommendations) != 1 || got.result.Recommendations[0].ProductID != "p_unknown" {
					t.Fatalf("hard filter did not preserve only the remaining candidate: %+v", got.result)
				}
			} else {
				assertSupplyConflict(t, got.result)
			}
			if strings.Contains(guide.ResultText(got.result), "无需气源") {
				t.Fatal("unsafe provider reply escaped via final fallback text")
			}
			assertNativeStream(t, m, 2)
		})
	}
}
