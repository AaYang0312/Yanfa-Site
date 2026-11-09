package main

import (
	"encoding/json"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"testing"
)

func TestStructuredResultDoesNotParseConversationalText(t *testing.T) {
	result := &sdk.CallToolResult{StructuredContent: map[string]any{"version": 7}, Content: []sdk.Content{&sdk.TextContent{Text: "产品草稿已保存，尚未更新官网。/admin/products/edit/?id=p_fixture"}}}
	var payload struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(structuredJSON(result), &payload); err != nil || payload.Version != 7 {
		t.Fatal("machine payload was not decoded independently")
	}
	if link := productEditURL(result.Content, "p_fixture"); link != "/admin/products/edit/?id=p_fixture" {
		t.Fatal("wrong product handoff")
	}
}
func TestStructuredResultRequiresMachinePayload(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("missing structured content was accepted")
		}
	}()
	structuredJSON(&sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: `{"version":7}`}}})
}
