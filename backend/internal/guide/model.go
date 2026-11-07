package guide

import (
	"context"
	"net/http"

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
	return openai.NewChatModel(ctx, &openai.ChatModelConfig{BaseURL: baseURL, Model: name, APIKey: key, HTTPClient: &http.Client{Timeout: inferenceTimeout}, ResponseFormat: &openai.ChatCompletionResponseFormat{Type: openai.ChatCompletionResponseFormatTypeJSONObject}})
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
	// Consult deliberately requests Generate, so proposal tokens never become a
	// public stream. Keep this interface path bounded and counted as well.
	msg, err := m.Generate(ctx, messages, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}
