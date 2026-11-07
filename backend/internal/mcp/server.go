// Package mcp exposes workspace management through the official MCP SDK.
// Transport negotiation belongs to the SDK; all mutations call the same services
// as the browser API. There are deliberately no confirmation or review tools.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"yuanfa-official/backend/internal/auth"
	"yuanfa-official/backend/internal/content"
	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/media"
	"yuanfa-official/backend/internal/publication"
	"yuanfa-official/schemas"
)

type toolSpec struct {
	Name  string `json:"name"`
	Input struct {
		Ref string `json:"$ref"`
	} `json:"inputSchema"`
	Output struct {
		Ref string `json:"$ref"`
	} `json:"outputSchema"`
	Scope contract.TokenScope `json:"scope"`
}

type registration struct {
	description string
	call        func(context.Context, auth.Actor, string, json.RawMessage) (any, error)
}

func typed[I, O any](description string, call func(context.Context, auth.Actor, I) (O, error)) registration {
	return registration{description: description, call: func(ctx context.Context, a auth.Actor, schema string, raw json.RawMessage) (any, error) {
		in, err := contract.Decode[I](schema, raw)
		if err != nil {
			return nil, err
		}
		return call(ctx, a, in)
	}}
}

// New returns a fully registered server or an error loading the frozen schemas.
// The low-level SDK handler keeps validation/business failures structured. Its
// shared source-schema decoder rejects unknown fields, explicit null and forged
// server fields before typed decoding; SDK reflection schemas are not a second
// contract. Each invocation authenticates its own HTTP headers again.
func New(identity *auth.Service, products *content.Service, images *media.Service, publishing *publication.Service) (*sdk.Server, error) {
	handlers := map[string]registration{
		"search_products": typed("搜索管理工作区的草稿和产品，返回版本；不是公开目录。", func(ctx context.Context, a auth.Actor, in contract.MCPSearchProductsInput) (contract.AdminProductList, error) {
			return products.Search(ctx, a, contract.AdminProductQuery{Q: in.Query, Category: in.Category, Visibility: in.Visibility, Cursor: in.Cursor, Limit: in.Limit})
		}),
		"get_product": typed("读取完整工作区产品及当前 version、workspace_revision。", func(ctx context.Context, a auth.Actor, in contract.MCPGetProductInput) (contract.ProductEnvelope, error) {
			return products.Get(ctx, a, in.ProductID)
		}),
		"create_product": typed("创建目标下架草稿；保存不代表官网生效。", products.Create),
		"update_product": typed("用 expected_version 整体替换可编辑文档；冲突时重新读取并人工合并，不能自动重试旧内容。", func(ctx context.Context, a auth.Actor, in contract.MCPUpdateProductInput) (contract.ProductEnvelope, error) {
			return products.Replace(ctx, a, in.ProductID, in.ExpectedVersion, in.Edit)
		}),
		"delete_draft": typed("仅删除从未公开的草稿，保留 slug 归属；需要当前版本。", func(ctx context.Context, a auth.Actor, in contract.MCPDeleteDraftInput) (contract.DeletionResult, error) {
			return products.Delete(ctx, a, in.ProductID, in.ExpectedVersion)
		}),
		"list_media": typed("以固定 after 和分页 cursor 查询私有素材元数据；预览需要浏览器登录。", images.List),
		"open_upload": typed("返回需登录的后台上传链接及 after 标记；请用户打开链接上传，然后 list_media 查询并辨认新图。不读取客户端路径。", func(ctx context.Context, a auth.Actor, in contract.MCPOpenUploadInput) (contract.OpenUploadResult, error) {
			// Query only one record: After is captured from the same media transaction,
			// independently of the page length. No filesystem path or upload privilege.
			listed, err := images.List(ctx, a, contract.MediaQuery{Limit: new(int64(1))})
			if err != nil {
				return contract.OpenUploadResult{}, err
			}
			q := url.Values{"after": {listed.After}}
			if in.ProductID != nil {
				q.Set("product_id", *in.ProductID)
			}
			return contract.OpenUploadResult{UploadURL: "/admin/media/?" + q.Encode(), After: listed.After}, nil
		}),
		"create_candidate": typed("异步冻结整个已保存工作区；返回 queued/building，随后 get_candidate 查询。相同请求重用 request_key。", func(ctx context.Context, a auth.Actor, in contract.MCPCreateCandidateInput) (contract.Candidate, error) {
			return publishing.CreateCandidate(ctx, a, in.ExpectedWorkspaceRevision, in.RequestKey)
		}),
		"get_candidate": typed("查询候选真实构建状态、完整差异、过期标记及原始审阅/预览链接。", func(ctx context.Context, a auth.Actor, in contract.MCPGetCandidateInput) (contract.Candidate, error) {
			return publishing.GetCandidate(ctx, a, in.CandidateID)
		}),
		"request_publication": typed("创建绑定摘要和预期版本的发布或回退意图，绝不直接上线。请用户打开原始 review_url 登录审阅确认。", func(ctx context.Context, a auth.Actor, in contract.MCPRequestPublicationInput) (contract.PublicationRequest, error) {
			return publishing.Request(ctx, a, contract.PublicationInput{Kind: in.Kind, CandidateID: in.CandidateID, ReleaseID: in.ReleaseID, SnapshotSHA256: in.SnapshotSHA256, ExpectedActiveReleaseID: in.ExpectedActiveReleaseID, ExpectedWorkspaceRevision: in.ExpectedWorkspaceRevision}, in.RequestKey)
		}),
		"get_publication": typed("查询待确认、执行或终态；仅 succeeded 且有 result_release_id 才报告官网已生效。", func(ctx context.Context, a auth.Actor, in contract.MCPGetPublicationInput) (contract.PublicationRequest, error) {
			return publishing.GetRequest(ctx, a, in.RequestID)
		}),
	}
	source, err := schemas.Files.ReadFile("mcp-tools.schema.json")
	if err != nil {
		return nil, err
	}
	var manifest struct {
		Tools []toolSpec `json:"x-tools"`
	}
	if err = json.Unmarshal(source, &manifest); err != nil {
		return nil, err
	}
	server := sdk.NewServer(&sdk.Implementation{Name: "yuanfa-management", Version: "1"}, &sdk.ServerOptions{Capabilities: &sdk.ServerCapabilities{}, Instructions: "管理工作区保存不等于官网生效。发布确认、选型事实审核和凭证管理只能在浏览器后台完成。图片须由用户打开 open_upload 链接上传。"})
	for _, spec := range manifest.Tools {
		handler, ok := handlers[spec.Name]
		if !ok {
			return nil, fmt.Errorf("unimplemented management tool %q", spec.Name)
		}
		delete(handlers, spec.Name)
		inputName := "mcp-tools.schema.json" + spec.Input.Ref
		outputName := "mcp-tools.schema.json" + spec.Output.Ref
		inputSchema, err := contract.JSONSchema(inputName)
		if err != nil {
			return nil, err
		}
		outputSchema, err := contract.JSONSchema(outputName)
		if err != nil {
			return nil, err
		}
		server.AddTool(&sdk.Tool{Name: spec.Name, Description: handler.description, InputSchema: inputSchema, OutputSchema: outputSchema}, func(ctx context.Context, request *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			actor, err := authenticate(ctx, identity, request)
			if err != nil {
				return failure(err), nil
			}
			if err = identity.Authorize(ctx, actor, spec.Scope); err != nil {
				return failure(err), nil
			}
			raw := request.Params.Arguments
			if len(raw) == 0 {
				raw = json.RawMessage(`{}`)
			}
			result, err := handler.call(ctx, actor, inputName, raw)
			if err != nil {
				return failure(err), nil
			}
			encoded, err := json.Marshal(result)
			if err != nil {
				return failure(err), nil
			}
			if err = contract.ValidateJSON(outputName, encoded); err != nil {
				// A broken server result is never exposed as a client validation error.
				return failure(errors.New("invalid management tool output")), nil
			}
			return &sdk.CallToolResult{StructuredContent: json.RawMessage(encoded), Content: []sdk.Content{&sdk.TextContent{Text: string(encoded)}}}, nil
		})
	}
	if len(handlers) != 0 {
		return nil, errors.New("management tool missing from shared schema")
	}
	return server, nil
}

func authenticate(ctx context.Context, identity *auth.Service, request *sdk.CallToolRequest) (auth.Actor, error) {
	if request.Extra == nil {
		return auth.Actor{}, contract.NewBusinessError(401, "UNAUTHENTICATED", "需要有效 Bearer 凭证。")
	}
	values := request.Extra.Header.Values("Authorization")
	if len(values) != 1 {
		return auth.Actor{}, contract.NewBusinessError(401, "UNAUTHENTICATED", "需要有效 Bearer 凭证。")
	}
	parts := strings.Fields(values[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return auth.Actor{}, contract.NewBusinessError(401, "UNAUTHENTICATED", "需要有效 Bearer 凭证。")
	}
	return identity.AuthenticateToken(ctx, parts[1])
}

func failure(err error) *sdk.CallToolResult {
	var business *contract.BusinessError
	if !errors.As(err, &business) {
		business = contract.NewBusinessError(500, "INTERNAL_ERROR", "服务暂时不可用。")
	}
	response := business.Response()
	encoded, _ := json.Marshal(response)
	return &sdk.CallToolResult{IsError: true, StructuredContent: response, Content: []sdk.Content{&sdk.TextContent{Text: string(encoded)}}}
}
