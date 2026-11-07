//go:build integration

package guide

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// Private scenario contract for createGuideHarness. Only this build reads the
// control directory. Proposals still pass through the real Eino agent, public
// tools, strict shared decoder, Evaluate, Select and production stream encoder.
type integrationScenario struct {
	ModelProposal     json.RawMessage `json:"model_proposal"`
	ModelError        string          `json:"model_error"`
	SearchQuery       json.RawMessage `json:"search_query"`
	GetProductID      string          `json:"get_product_id"`
	PauseBeforeResult bool            `json:"pause_before_result"`
	StreamFault       string          `json:"stream_fault"`
}

func scenario() (integrationScenario, error) {
	var s integrationScenario
	dir := os.Getenv("YUANFA_INTEGRATION_DIR")
	if dir == "" {
		return s, nil
	}
	b, err := os.ReadFile(filepath.Join(dir, "scenario.json"))
	if err != nil {
		return s, err
	}
	err = json.Unmarshal(b, &s)
	return s, err
}
func requestModel(base model.ToolCallingChatModel) (model.ToolCallingChatModel, error) {
	s, err := scenario()
	if err != nil {
		return nil, err
	}
	if len(s.ModelProposal) == 0 && s.ModelError == "" {
		return base, nil
	}
	return &integrationModel{scenario: s}, nil
}

type integrationModel struct{ scenario integrationScenario }

func (m *integrationModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}
func (m *integrationModel) Generate(ctx context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m.scenario.ModelError == "upstream" {
		return nil, errors.New("integration provider failure containing private detail")
	}
	if m.scenario.ModelError == "block" {
		if err := integrationWait(ctx, "model"); err != nil {
			return nil, err
		}
	}
	tools := 0
	for _, msg := range input {
		if msg.Role == schema.Tool {
			tools++
		}
	}
	if tools == 0 || m.scenario.ModelError == "loop" {
		query := m.scenario.SearchQuery
		if len(query) == 0 {
			query = json.RawMessage(`{}`)
		}
		name := "search_products"
		if m.scenario.ModelError == "forbidden-tool" {
			name = "create_candidate"
		}
		calls := []schema.ToolCall{{ID: "search_" + strconv.Itoa(tools), Function: schema.FunctionCall{Name: name, Arguments: string(query)}}}
		if m.scenario.GetProductID != "" {
			args, _ := json.Marshal(map[string]string{"product_id": m.scenario.GetProductID})
			calls = append(calls, schema.ToolCall{ID: "get_" + strconv.Itoa(tools), Function: schema.FunctionCall{Name: "get_product", Arguments: string(args)}})
		}
		return schema.AssistantMessage("", calls), nil
	}
	if m.scenario.ModelError == "invalid" {
		return schema.AssistantMessage(`{"requirements":`, nil), nil
	}
	return schema.AssistantMessage(string(m.scenario.ModelProposal), nil), nil
}
func (m *integrationModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

var statsMu sync.Mutex
var stats struct {
	AgentRuns        int64  `json:"agent_runs"`
	ModelCalls       int64  `json:"model_calls"`
	ToolCalls        int64  `json:"tool_calls"`
	SelectCalls      int64  `json:"select_calls"`
	ActiveModelCalls int64  `json:"active_model_calls"`
	ReleaseID        string `json:"release_id"`
}

func record(kind, id string, delta int64) {
	dir := os.Getenv("YUANFA_INTEGRATION_DIR")
	if dir == "" {
		return
	}
	statsMu.Lock()
	defer statsMu.Unlock()
	stats.ReleaseID = id
	switch kind {
	case "agent_runs":
		stats.AgentRuns += delta
	case "model_calls":
		stats.ModelCalls += delta
	case "tool_calls":
		stats.ToolCalls += delta
	case "select_calls":
		stats.SelectCalls += delta
	case "active_model_calls":
		stats.ActiveModelCalls += delta
	}
	b, err := json.Marshal(stats)
	if err != nil {
		return
	}
	temp := filepath.Join(dir, "stats.tmp")
	if os.WriteFile(temp, b, 0600) == nil {
		_ = os.Rename(temp, filepath.Join(dir, "stats.json"))
	}
}
func integrationWait(ctx context.Context, name string) error {
	dir := os.Getenv("YUANFA_INTEGRATION_DIR")
	b, _ := json.Marshal(map[string]any{"barrier": name, "pid": os.Getpid()})
	if err := os.WriteFile(filepath.Join(dir, "reached.json"), b, 0600); err != nil {
		return err
	}
	timer := time.NewTicker(10 * time.Millisecond)
	defer timer.Stop()
	for {
		if _, err := os.Stat(filepath.Join(dir, "continue")); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
}
func pauseResult(ctx context.Context, _ string) error {
	s, err := scenario()
	if err != nil {
		return err
	}
	if s.PauseBeforeResult {
		return integrationWait(ctx, "before-result")
	}
	return nil
}
func toolBoundary(ctx context.Context) error {
	s, err := scenario()
	if err != nil {
		return err
	}
	if s.ModelError == "tool-block" {
		return integrationWait(ctx, "tool")
	}
	return nil
}
func streamBoundary(ctx context.Context, index int) (bool, error) {
	s, err := scenario()
	if err != nil {
		return false, err
	}
	switch s.StreamFault {
	case "error":
		if index == 5 {
			return false, errors.New("integration private stream failure")
		}
	case "disconnect", "missing-finish":
		if index == 5 {
			return false, nil
		}
	case "missing-done":
		if index == 6 {
			return false, nil
		}
	case "pause":
		if index == 5 {
			if err = integrationWait(ctx, "stream"); err != nil {
				return false, err
			}
		}
	}
	return true, nil
}
