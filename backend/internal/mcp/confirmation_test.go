package mcp_test

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"yuanfa-official/backend/internal/contract"
)

func TestMachineWritesHandOffToProductEditorWithoutPublication(t *testing.T) {
	f := setup(t)
	token := f.token(t, "products:read", "products:write", "candidates:write", "publications:write")
	s, _, err := f.connect(token.Secret)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	input := draft("editor-handoff")
	input.Name = "产品确认测试"
	write := func(name string, in any) contract.ProductEnvelope {
		t.Helper()
		result, err := s.CallTool(f.ctx, &sdk.CallToolParams{Name: name, Arguments: in})
		if err != nil || result.IsError {
			t.Fatalf("%s failed: %+v %v", name, result, err)
		}
		encoded, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		out, err := contract.Decode[contract.ProductEnvelope]("product.schema.json#/$defs/ProductEnvelope", encoded)
		if err != nil {
			t.Fatalf("structured envelope compatibility: %v", err)
		}
		var text strings.Builder
		for _, c := range result.Content {
			if c, ok := c.(*sdk.TextContent); ok {
				text.WriteString(c.Text)
			}
		}
		handoff := text.String()
		if !strings.Contains(handoff, "/admin/products/edit/?id="+url.QueryEscape(out.Product.ID)) || !strings.Contains(handoff, "保存并更新官网") || !strings.Contains(handoff, "草稿") {
			t.Fatalf("missing readable browser confirmation: %s", handoff)
		}
		for _, internal := range []string{"workspace_revision", "sha256", "create_candidate", "request_publication", `"product":`} {
			if strings.Contains(handoff, internal) {
				t.Fatalf("technical handoff leaked %s: %s", internal, handoff)
			}
		}
		return out
	}
	created := write("create_product", input)
	edit := editing(input)
	edit.Summary = "仅保存到机器草稿"
	updated := write("update_product", contract.MCPUpdateProductInput{ProductID: created.Product.ID, ExpectedVersion: created.Version, Edit: edit})
	if updated.Version != created.Version+1 || updated.Product.Summary != edit.Summary {
		t.Fatal("machine update did not preserve versioned draft")
	}
	for _, table := range []string{"candidates", "publication_requests", "product_confirmations"} {
		var count int
		if err := f.a.Store.DB().QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("machine write unexpectedly created %s", table)
		}
	}
}

func TestReadOnlyConnectionRefusesEveryWriteTool(t *testing.T) {
	f := setup(t)
	created, err := f.a.Content.Create(f.ctx, f.browser.Session.Actor, draft("readonly-handoff"))
	if err != nil {
		t.Fatal(err)
	}
	token := f.token(t, "products:read")
	s, _, err := f.connect(token.Secret)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, name := range []string{"create_product", "update_product", "delete_draft", "create_candidate", "request_publication"} {
		business(t, s, name, map[string]any{}, "FORBIDDEN")
	}
	current := call[contract.ProductEnvelope](t, s, "get_product", contract.MCPGetProductInput{ProductID: created.Product.ID})
	if current.Version != created.Version || current.WorkspaceRevision != created.WorkspaceRevision {
		t.Fatal("read-only tool call changed the workspace")
	}
	for _, name := range []string{"confirm_publication", "guide_review", "issue_token", "revoke_token", "confirm_product", "retry_site_update"} {
		if _, err := s.CallTool(f.ctx, &sdk.CallToolParams{Name: name, Arguments: map[string]any{}}); err == nil {
			t.Fatalf("browser-only action %s exposed over MCP", name)
		}
	}
}

func TestUploadHandoffUsesProductEditor(t *testing.T) {
	f := setup(t)
	token := f.token(t, "media:read")
	s, _, err := f.connect(token.Secret)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, tc := range []struct {
		id   *string
		want string
	}{
		{nil, "/admin/products/new/"},
		{new("p_existing"), "/admin/products/edit/?id=p_existing"},
	} {
		out := call[contract.OpenUploadResult](t, s, "open_upload", contract.MCPOpenUploadInput{ProductID: tc.id})
		if out.UploadURL != tc.want || out.After == "" {
			t.Fatalf("upload must use the product's image editor and retain the media cursor: %+v", out)
		}
	}
}
