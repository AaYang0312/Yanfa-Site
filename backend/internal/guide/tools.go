package guide

import (
	"context"
	"encoding/json"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
	"yuanfa-official/backend/internal/contract"
	"yuanfa-official/backend/internal/release"
)

type catalogTool struct {
	info *schema.ToolInfo
	run  func(context.Context, string) (any, error)
}

func (t *catalogTool) Info(context.Context) (*schema.ToolInfo, error) { return t.info, nil }
func (t *catalogTool) InvokableRun(ctx context.Context, input string, _ ...tool.Option) (string, error) {
	out, err := t.run(ctx, input)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(out)
	return string(b), err
}

func toolInfo(name, description, source string) (*schema.ToolInfo, error) {
	raw, err := contract.JSONSchema(source)
	if err != nil {
		return nil, err
	}
	var params jsonschema.Schema
	if err = json.Unmarshal(raw, &params); err != nil {
		return nil, err
	}
	return &schema.ToolInfo{Name: name, Desc: description, ParamsOneOf: schema.NewParamsOneOfByJSONSchema(&params)}, nil
}

func (a *Agent) readTools(h *release.Handle, counts *runCounts) ([]tool.BaseTool, error) {
	search, err := toolInfo("search_products", "搜索本次已发布公开目录。未知资料默认不满足显式筛选，可用 include_unknown 查询待核实项；空搜索页不证明无匹配。release_id 只能是本次版本。", "public-catalog.schema.json#/$defs/CatalogQuery")
	if err != nil {
		return nil, err
	}
	// This ID-only input reuses the existing strict ID DTO, not management
	// registration or permissions. No Actor or management service is reachable.
	get, err := toolInfo("get_product", "按稳定 product_id 读取本次版本上架商品及已审核公开资料。", "mcp-tools.schema.json#/$defs/MCPGetProductInput")
	if err != nil {
		return nil, err
	}
	enter := func(ctx context.Context) error {
		counts.tools.Add(1)
		record("tool_calls", h.ID(), 1)
		if err := ctx.Err(); err != nil {
			return err
		}
		return toolBoundary(ctx)
	}
	return []tool.BaseTool{
		&catalogTool{search, func(ctx context.Context, input string) (any, error) {
			if err := enter(ctx); err != nil {
				return nil, err
			}
			q, err := contract.Decode[contract.CatalogQuery]("public-catalog.schema.json#/$defs/CatalogQuery", []byte(input))
			if err != nil {
				return nil, err
			}
			if q.ReleaseID != nil && *q.ReleaseID != h.ID() {
				return nil, release.Changed()
			}
			id := h.ID()
			q.ReleaseID = &id
			return a.catalog.Search(ctx, h, q)
		}},
		&catalogTool{get, func(ctx context.Context, input string) (any, error) {
			if err := enter(ctx); err != nil {
				return nil, err
			}
			req, err := contract.Decode[contract.MCPGetProductInput]("mcp-tools.schema.json#/$defs/MCPGetProductInput", []byte(input))
			if err != nil {
				return nil, err
			}
			return a.catalog.Get(ctx, h, req.ProductID)
		}},
	}, nil
}
