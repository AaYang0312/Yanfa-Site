package guide

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// NewModel constructs the locked OpenAI-compatible adapter without contacting
// the provider. Missing customer settings leave consultation unconfigured.
// No retry/failover wrappers are installed; cancellation reaches net/http.
func NewModel(ctx context.Context, baseURL, name, key string) (model.ToolCallingChatModel, error) {
	if baseURL == "" || name == "" || key == "" {
		return nil, nil
	}
	config := &openai.ChatModelConfig{BaseURL: baseURL, Model: name, APIKey: key, HTTPClient: &http.Client{Timeout: inferenceTimeout}, ResponseFormat: &openai.ChatCompletionResponseFormat{Type: openai.ChatCompletionResponseFormatTypeJSONObject}}
	if strings.EqualFold(name, "glm-5.3-flash") {
		// GLM's constrained JSON mode interferes with function-call generation.
		// The agent still requires JSON and strictly validates its final proposal.
		config.ResponseFormat = nil
		// This model cannot disable thinking. Bound its reasoning effort for the
		// interactive guide's existing 20-second total inference deadline.
		config.ReasoningEffort = "low"
	}
	return openai.NewChatModel(ctx, config)
}

type countedModel struct {
	base    model.ToolCallingChatModel
	counts  *runCounts
	release string
}

func (m *countedModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	bound, err := m.base.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return &countedModel{base: bound, counts: m.counts, release: m.release}, nil
}
func (m *countedModel) Generate(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	m.counts.models.Add(1)
	record("model_calls", m.release, 1)
	record("active_model_calls", m.release, 1)
	defer record("active_model_calls", m.release, -1)
	return m.base.Generate(ctx, messages, opts...)
}
func (m *countedModel) Stream(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	m.counts.models.Add(1)
	record("model_calls", m.release, 1)
	record("active_model_calls", m.release, 1)
	input, err := m.base.Stream(ctx, messages, opts...)
	if err != nil {
		record("active_model_calls", m.release, -1)
		return nil, err
	}
	output, writer := schema.Pipe[*schema.Message](1)
	go func() {
		// Account for the completed provider before EOF reaches the agent.
		defer writer.Close()
		defer record("active_model_calls", m.release, -1)
		defer input.Close()
		for {
			chunk, err := input.Recv()
			if errors.Is(err, io.EOF) {
				return
			}
			if writer.Send(chunk, err) || err != nil {
				return
			}
		}
	}()
	return output, nil
}
