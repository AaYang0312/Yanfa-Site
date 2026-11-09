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
		"create_product": typed("创建目标下架的机器草稿，返回产品编辑链接。请用户在浏览器检查后点击“保存并更新官网”；机器保存不确认、不发布，也不会加入其他产品的更新批次。", products.Create),
		"update_product": typed("用 expected_version 整体替换可编辑文档，仅保存机器草稿并返回产品编辑链接。请用户在浏览器检查后点击“保存并更新官网”。冲突时重新读取并人工合并，不能自动重试旧内容；不要自动创建候选或发布意图。", func(ctx context.Context, a auth.Actor, in contract.MCPUpdateProductInput) (contract.ProductEnvelope, error) {
			return products.Replace(ctx, a, in.ProductID, in.ExpectedVersion, in.Edit)
		}),
		"delete_draft": typed("仅删除从未公开的草稿，保留 slug 归属；需要当前版本。", func(ctx context.Context, a auth.Actor, in contract.MCPDeleteDraftInput) (contract.DeletionResult, error) {
			return products.Delete(ctx, a, in.ProductID, in.ExpectedVersion)
		}),
		"list_media": typed("以固定 after 和分页 cursor 查询私有素材元数据；预览需要浏览器登录。", images.List),
		"open_upload": typed("返回需登录的产品图片编辑链接及 after 标记；有 product_id 时打开该产品，否则打开新增页。请用户在图片区上传，可用 list_media 查询新图；保存并更新官网必须由用户确认。不读取客户端路径。", func(ctx context.Context, a auth.Actor, in contract.MCPOpenUploadInput) (contract.OpenUploadResult, error) {
			// Query only one record: After is captured from the same media transaction,
			// independently of the page length. No filesystem path or upload privilege.
			listed, err := images.List(ctx, a, contract.MediaQuery{Limit: new(int64(1))})
			if err != nil {
				return contract.OpenUploadResult{}, err
			}
			link := "/admin/products/new/"
			if in.ProductID != nil {
				link = "/admin/products/edit/?" + url.Values{"id": {*in.ProductID}}.Encode()
			}
			return contract.OpenUploadResult{UploadURL: link, After: listed.After}, nil
		}),
		"create_candidate": typed("旧版兼容工具，不用于日常产品编辑，不自动调用。异步冻结工作区，仅构建候选，不确认或激活官网；相同请求重用 request_key。普通修改应使用产品编辑链接由用户确认。", func(ctx context.Context, a auth.Actor, in contract.MCPCreateCandidateInput) (contract.Candidate, error) {
			return publishing.CreateCandidate(ctx, a, in.ExpectedWorkspaceRevision, in.RequestKey)
		}),
		"get_candidate": typed("旧版兼容查询，读取候选真实构建状态、差异和原始链接。候选就绪不表示官网生效；普通编辑请转到产品编辑页确认。", func(ctx context.Context, a auth.Actor, in contract.MCPGetCandidateInput) (contract.Candidate, error) {
			return publishing.GetCandidate(ctx, a, in.CandidateID)
		}),
		"request_publication": typed("旧版兼容工具，不自动调用。创建绑定摘要和预期版本的发布或回退意图，绝不确认或直接上线；普通产品修改使用产品编辑页的“保存并更新官网”。", func(ctx context.Context, a auth.Actor, in contract.MCPRequestPublicationInput) (contract.PublicationRequest, error) {
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
	server := sdk.NewServer(&sdk.Implementation{Name: "yuanfa-management", Version: "1"}, &sdk.ServerOptions{Capabilities: &sdk.ServerCapabilities{}, Instructions: "查询读取管理工作区；创建和更新只保存机器草稿，不更新官网或进入已确认批次。写入后把返回的产品编辑链接交给用户，登录检查后点击“保存并更新官网”。不要自动串联候选或发布意图工具；它们仅供旧版兼容，不能代替浏览器确认。普通维护不要求独立选型事实审核，缺失的适配证据仍未知。图片须由用户打开 open_upload 链接上传；确认和连接管理仅限浏览器。只读连接不能写入。"})
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
			return &sdk.CallToolResult{StructuredContent: json.RawMessage(encoded), Content: []sdk.Content{&sdk.TextContent{Text: resultText(spec.Name, result, encoded)}}}, nil
		})
	}
	if len(handlers) != 0 {
		return nil, errors.New("management tool missing from shared schema")
	}
	return server, nil
}

// Preserve the frozen structured schemas for machine clients while keeping the
// human handoff readable. In particular, do not repeat private source hashes or
// publication envelopes in the conversational text.
func resultText(name string, result any, encoded []byte) string {
	switch value := result.(type) {
	case contract.ProductEnvelope:
		link := "/admin/products/edit/?" + url.Values{"id": {value.Product.ID}}.Encode()
		if name == "create_product" || name == "update_product" {
			return "产品草稿已保存，尚未更新官网。请打开产品编辑页，登录并检查内容后点击“保存并更新官网”：" + link
		}
		return "已读取产品工作区资料，可能包含尚未确认的修改。查看产品编辑页：" + link
	case contract.Candidate:
		return "已读取旧版候选，状态：" + string(value.State) + "。候选不代表官网已更新。日常修改请在产品编辑页检查并点击“保存并更新官网”。"
	case contract.PublicationRequest:
		if value.State == "succeeded" && value.ResultReleaseID != nil {
			return "该发布操作已成功，官网已更新。"
		}
		return "旧版发布操作状态：" + string(value.State) + "，尚不能报告官网已更新。日常修改请在产品编辑页检查并点击“保存并更新官网”。"
	case contract.OpenUploadResult:
		return "请打开产品编辑页，在图片区上传并检查图片；需要更新官网时由您点击“保存并更新官网”：" + value.UploadURL
	default:
		return string(encoded)
	}
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
