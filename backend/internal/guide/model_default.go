//go:build !integration

package guide

import (
	"context"
	"github.com/cloudwego/eino/components/model"
)

func requestModel(base model.ToolCallingChatModel) (model.ToolCallingChatModel, error) {
	return base, nil
}
func record(string, string, int64)                      {}
func pauseResult(context.Context, string) error         { return nil }
func toolBoundary(context.Context) error                { return nil }
func streamBoundary(context.Context, int) (bool, error) { return true, nil }
