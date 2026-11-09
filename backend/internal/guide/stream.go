package guide

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/matching"
)

// ResultText keeps deterministic failures authoritative and uses the validated
// conversational reply for introductions and clarification. Structured facts
// and product controls remain separate from natural language.
func ResultText(result contract.GuideResult) string {
	switch result.State {
	case "no_match":
		return strings.Join(result.Conflicts, "\n")
	case "unavailable":
		return "导购暂时不可用，请使用产品筛选或联系咨询。"
	}
	if result.Reply != nil {
		return *result.Reply
	}
	var lines []string
	if result.State == "selected" {
		lines = append(lines, "已选择："+result.Selection.Model+" "+result.Selection.Name)
		for _, f := range result.Selection.MatchedFacts {
			lines = append(lines, f.Text)
		}
		lines = append(lines, result.Selection.ConfirmationNotes...)
	}
	if len(result.MissingFields) > 0 && hasStatedRequirements(result.ConfirmedRequirements) {
		lines = append(lines, "目前无法确认全部适配条件，仍需核实："+joinMissing(result.MissingFields)+"。请补充条件或联系咨询。")
	}
	if result.Question != nil {
		lines = append(lines, result.Question.Text)
	}
	for _, r := range result.Recommendations {
		lines = append(lines, r.Model+" "+r.Name)
		for _, f := range r.MatchedFacts {
			lines = append(lines, f.Text)
		}
		lines = append(lines, r.ConfirmationNotes...)
	}
	if len(lines) == 0 {
		lines = append(lines, "请查看官网产品介绍，或联系咨询确认适配条件。")
	}
	return strings.Join(lines, "\n")
}
func joinMissing(fields []contract.MissingField) string {
	var b strings.Builder
	for i, f := range fields {
		if i > 0 {
			b.WriteString("、")
		}
		b.WriteString(matching.MissingLabel(f))
	}
	return b.String()
}

// PrepareUIMessageStream validates and serializes all events before headers are
// committed. The returned sender writes after BeginResponse's short read lock.
// Exactly one complete GuideResult is shared with the JSON response boundary.
func PrepareUIMessageStream(result contract.GuideResult) (func(context.Context, http.ResponseWriter) error, error) {
	data, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	if err = contract.ValidateJSON("guide.schema.json#/$defs/GuideResult", data); err != nil {
		return nil, err
	}
	id := uuid.NewString()
	textID := id + "-text"
	events := []any{
		map[string]any{"type": "start", "messageId": id},
		map[string]any{"type": "text-start", "id": textID},
		map[string]any{"type": "text-delta", "id": textID, "delta": ResultText(result)},
		map[string]any{"type": "text-end", "id": textID},
		map[string]any{"type": "data-guide-result", "data": json.RawMessage(data)},
		map[string]any{"type": "finish"},
	}
	frames := make([][]byte, 0, len(events)+1)
	for _, event := range events {
		b, e := json.Marshal(event)
		if e != nil {
			return nil, e
		}
		frames = append(frames, append(append([]byte("data: "), b...), '\n', '\n'))
	}
	frames = append(frames, []byte("data: [DONE]\n\n"))
	return func(ctx context.Context, w http.ResponseWriter) error {
		controller := http.NewResponseController(w)
		fail := func(err error) error {
			if ctx.Err() == nil {
				_, _ = fmt.Fprint(w, "data: {\"type\":\"error\",\"errorText\":\"回复未完成，请重新咨询。\"}\n\n")
				_ = controller.Flush()
			}
			return err
		}
		for i, frame := range frames {
			if err := ctx.Err(); err != nil {
				return err
			}
			send, err := streamBoundary(ctx, i)
			if err != nil {
				return fail(err)
			}
			if !send {
				return nil
			}
			if _, err = w.Write(frame); err != nil {
				return fail(err)
			}
			if err = controller.Flush(); err != nil {
				return fail(err)
			}
		}
		return nil
	}, nil
}

// WriteUIMessageStream writes a validated result to an already-started SSE
// response. HTTP callers use PrepareUIMessageStream before BeginResponse to keep
// invalid output on the JSON error side of the commit boundary.
func WriteUIMessageStream(ctx context.Context, w http.ResponseWriter, result contract.GuideResult) error {
	send, err := PrepareUIMessageStream(result)
	if err != nil {
		return err
	}
	return send(ctx, w)
}
