# WorkBuddy 私有管理 MCP 接入

本地验收记录（2026-11-09）：用户已在实际 WorkBuddy 客户端完成测试，反馈连接和使用正常。此记录确认当前本地环境的接入结果；部署地址或凭证变化后按下文重新配置。

管理端点为同源 `/mcp`，由固定的官方 `github.com/modelcontextprotocol/go-sdk v1.8.0` 提供 stateless Streamable HTTP。生产环境通过 HTTPS 访问；loopback HTTP 仅用于本地开发。初始化、协议协商、POST 响应及 GET/DELETE 行为由 SDK 决定，不提供自制 JSON-RPC 或旧 SSE 桥接协议。

服务使用 stateless 传输，无 `Mcp-Session-Id` 依赖。已鉴权 GET/DELETE 由 SDK 返回 405，缺凭证则先返回 401。目标 WorkBuddy 的协议协商和兼容性须在实际客户端确认。

管理员在后台设置（`/admin/settings/`）打开 WorkBuddy 接入，填写连接名称、失效时间，选择“只读查询”或“编辑产品”等权限后创建连接。凭证原值仅显示一次；关闭前按目标客户端支持的安全方式保存，遗失则撤销并重新创建。服务只保存哈希，每次 HTTP 请求和工具调用都核对有效期及撤销状态。客户端已初始化、TCP 连接仍在或此前调用成功均不延长授权。旧 `/admin/workbuddy/` 地址跳转到设置页。

配置结构：

```json
{
  "mcpServers": {
    "yuanfa-catalog": {
      "type": "streamableHttp",
      "url": "https://YOUR_SITE_DOMAIN/mcp",
      "headers": {
        "Authorization": "Bearer ${YUANFA_MCP_TOKEN}"
      }
    }
  }
}
```

`${YUANFA_MCP_TOKEN}` 是绑定位置示意，不保证目标 WorkBuddy 自动读取系统环境变量。请在实际客户端确认配置入口、安全凭证存储方式、WorkBuddy 版本、操作系统、账号形态及协商版本；本地官方 SDK 客户端不能替代客户端兼容性验证。不要把实际 Token 放进聊天、命令参数、仓库、截图或诊断日志。

没有 Origin 的原生客户端用 Bearer 鉴权；带 Origin 时必须精确等于服务器 `SITE_URL`，包括 scheme、主机及端口。拒绝 `null`、重复和其他来源。Cookie 不能代替 `/mcp` 的 Bearer。后台确认、审核和凭证管理接口也不接受 Bearer，即使同时带管理员 Cookie。

## 工具与共享契约

`tools/list` 返回下列 11 个工具的自包含 JSON Schema；定义直接来自 `schemas/mcp-tools.schema.json`，可编辑产品、媒体、候选和意图字段复用共享定义。未知字段、错误类型、非法 null、客户端路径及伪造服务器审核字段都会被拒绝。管理工具读取工作区，不读取公开目录作为草稿替代品。

| 工具 | 权限 | 输入与结果 |
| --- | --- | --- |
| `search_products` | `products:read` | `query/category/visibility/cursor/limit`；工作区摘要、每件产品版本、全局 revision、线上 release |
| `get_product` | `products:read` | `product_id`；完整产品、`version`、`workspace_revision` |
| `create_product` | `products:write` | 全部 ProductCreate 可编辑字段；创建目标下架机器草稿，返回可读的产品编辑确认链接 |
| `update_product` | `products:write` | `product_id/expected_version/edit`；整体替换可编辑字段，保存机器草稿并返回产品编辑确认链接 |
| `delete_draft` | `products:write` | `product_id/expected_version`；只删除从未公开的草稿，保留 slug 归属 |
| `list_media` | `media:read` | `after/cursor/limit`；私有素材元数据及受登录保护的预览 URL，无磁盘路径 |
| `open_upload` | `media:read` | 可选 `product_id`；产品编辑页（无 ID 时为新增页）的 `upload_url` 与原始 `after` 标记，只返回链接 |
| `create_candidate` | `candidates:write` | 旧版兼容：构建候选，不确认、不激活，不自动调用 |
| `get_candidate` | `products:read` | 旧版兼容：查询真实构建状态；结构化字段保持兼容 |
| `request_publication` | `publications:write` | 旧版兼容：创建待确认意图，不确认、不激活，不自动调用 |
| `get_publication` | `products:read` | 旧版兼容：查询意图状态、完成时间及成功 release |

仅含 `products:read` 的最小权限 Token 可初始化官方 SDK 客户端、读取候选及其原始审阅链接，并查询同一发布意图的待确认和成功结果，包括原始 ID、审阅 envelope 与实际生效 release。它不具备创建/更新/删除产品、创建候选或创建发布意图的权限；这些工具返回 `isError: true` 和结构化 `FORBIDDEN`。签发只读查询凭证不需要附加任何写 scope。候选、意图及浏览器确认继续复用同一业务服务，不使用提权 Actor 或管理 HTTP 自调用。

写入成功只代表草稿已保存。旧版本更新返回 MCP `isError: true`，`structuredContent.error.code=VERSION_CONFLICT`，并含 `current_version`；对应浏览器 PUT 返回 HTTP 412。保留本地编辑，重新读取后由用户合并，不自动重试旧内容。字段错误同样返回 `isError` 和结构化 `code/message/fields`；内部错误统一脱敏，不返回 SQL、密钥或私有路径。认证和 Origin 失败在 HTTP 边界返回 401/403，协议错误由 SDK 处理。

创建、更新及读取产品继续通过 `structuredContent` 返回原有完整 `ProductEnvelope`，包括客户端并发控制所需字段。人读的 `content` 改为中文说明和 `/admin/products/edit/?id=…` 链接，不再重复输出完整 JSON、来源哈希或发布摘要。旧客户端应从 `structuredContent` 读取数据，不要把人读文字当 JSON 解析。链接相对于连接的官网域名；客户端展示时可用该已配置域名补全，不能换成其他站点。

## 文字修改完整流程

1. `search_products` 查找，`get_product` 取得当前版本和完整文档。
2. 向 `update_product` 只提交完整 `edit` 与原 `expected_version`；保留未修改的可编辑资料，不向 edit 填入 ID、版本或审核等服务器字段。新增产品使用 `create_product`。
3. 展示工具返回的产品编辑链接，并说明“草稿已保存，尚未更新官网”。不要自动调用 `create_candidate` 或 `request_publication`。
4. 用户打开该链接，登录后检查同一简化编辑页的文字、图片及上下架状态，点击“保存并更新官网”。只有这个浏览器操作把当前产品写入已确认内容，系统随后自动构建和更新。
5. 浏览器显示本次修改对应的更新结果。只有实际切换成功且包含该次修改，才表示官网已更新；失败保留最后成功官网，使用页面重试。MCP 保存成功、目标为上架、候选就绪都不能当成上线成功。

机器草稿不进入已确认台账，也不会被其他产品的保存或重试顺带发布。它可以晚于该产品最后一次浏览器确认版本；此时公开目录仍使用已确认版本。MCP 不提供确认、更新重试或连接创建/撤销工具，不把 Bearer 身份转换成浏览器身份。旧候选/发布意图仅用于协议兼容，不是日常编辑步骤，更不会自动激活。

## 图片修改完整流程

调用 `open_upload`，把返回的链接交给用户在浏览器打开。有 `product_id` 时进入 `/admin/products/edit/?id=…`，无 ID 时进入 `/admin/products/new/`；登录后在产品图片区选择本机文件上传，不再进入独立素材库。`after` 仍原样保留在结构化结果，供客户端查询新素材；人读文字只显示操作说明和产品页链接。MCP 不读取 `C:\\...`、`/Users/...`、`file:` 或远端文件路径，也不承诺替用户自动打开浏览器。

保留此次返回的 `after`。上传后 `list_media` 使用该固定 `after`，后续分页增加服务器返回的 `cursor`，不要用每页响应的新 `after` 替换当前遍历下界。根据缩略图、说明及上传时间让用户辨认素材，从结果取得 ID；无需用户手工复制 media_id。把已选素材加入完整 `images`，需要时替换 `cover_media_id`，带当前版本保存，再交付产品编辑链接，由用户检查并点击“保存并更新官网”。上传未关联图片仍是私有素材，不自动上线。日常维护也可直接在产品编辑页上传、排序、设置封面或移除图片。

## 公开导购与未知条件

公开导购只读取当前成功发布的目录，与管理工作区草稿隔离。关键词搜索覆盖名称、型号、简介、用途、特点和注意事项，无需独立的逐事实审核。导购把这些公开文字作为明确标注的产品介绍，较长内容提供摘要并提示查看详情；没有声明选型条件时，不强制展开技术问卷。

普通说明、分类和名称相似不构成适配证据。材料、基材、钉型、尺寸、兼容性、承重等条件缺失或冲突时仍保持未知或冲突，不批量生成已审核事实。已有有效证据可继续参与确定性判断；介绍文字不会写进 `matched_facts`。展示产品介绍或购买入口也不等于确认适配。模型未配置时仍返回导购暂不可用；公开目录搜索和产品详情可正常使用。

## 本地验证

使用固定 Go 工具链运行实际服务边界测试：

```sh
"$GO_BIN" test ./backend/internal/matching/... ./backend/internal/catalog/... ./backend/internal/guide/... ./backend/internal/mcp/... -count=1
```

覆盖公开文字检索与介绍、未知安全条件、响应大小限制、只读拒绝全部写工具、已初始化连接的过期与撤销、并发版本冲突，以及 MCP 创建/更新返回编辑链接且不创建确认记录。简化浏览器流程的集成验收还需确认：机器草稿不会被其他保存发布，用户检查后保存才更新，失败可重试且保留最近成功官网。

### 旧版候选兼容烟测

仓库 `mcp-smoke` 当前覆盖旧版候选和发布意图链路，不代表简化编辑页已验收；旧页面退出日常导航后，该浏览器脚本需配合集成任务调整。仅在专用隔离数据、确认兼容入口可用时运行，下述命令会真实修改并发布产品。仓库须已安装锁定依赖并生成 site config、构建后台；服务按 migrate → TTY `init-admin` → `import-seed` → `serve` 启动。使用独立 DATA_DIR 和端口，不共享运行中的数据库。`GO_BIN` 指向固定的 Go1.27.3 可执行文件，Go 设置 `GOTOOLCHAIN=local`，TMPDIR/GOTMPDIR 使用私有绝对路径；浏览器子进程独用短 TMPDIR。部署和应用配置见 [运维说明](content-platform-operations.md)。

```sh
export GOTOOLCHAIN=local
mkdir -p .work/bin
npm ci --no-audit --no-fund
npm run site:config
npm run build:admin
"$GO_BIN" test ./backend/internal/mcp/... -count=1
"$GO_BIN" build -o .work/bin/yuanfa ./backend/cmd/yuanfa
.work/bin/yuanfa migrate
.work/bin/yuanfa init-admin --username admin
.work/bin/yuanfa import-seed --seed seed/initial-catalog.json --media-root src/assets/products
.work/bin/yuanfa serve
```

在同一仓库的另一终端运行；`SMOKE_ADMIN_PASSWORD_FILE` 指向操作者准备的0600私有密码文件，密码原值不放进 argv/env。`PLAYWRIGHT_MODULE` 指向已锁的 playwright-core，`BROWSER_PATH` 指向可执行 Chromium；`CHECK_OUTPUT` 为本次私有输出目录。

```sh
"$GO_BIN" run ./backend/cmd/mcp-smoke --base "$SITE_URL" --out "$CHECK_OUTPUT/mcp"
```

CLI 用受保护密码输入登录，Token 从同源签发响应直接进入内存；通过官方 SDK 执行 initialize、tools/list、全部工具和错误边界。默认完整流程另签发仅含 `products:read` 的 Token，以同一个独立 SDK 客户端贯穿文字和图片两条链：读取实际候选、待确认意图、成功结果，逐项核对完整对象、原始 ID、绑定摘要/预期版本及生效 release；每条链都尝试全部五个写工具并要求结构化 `FORBIDDEN`，随后重读以证实产品、候选、意图、工作区版本和线上版本未被拒绝操作改变。原生 Playwright 在独立无会话浏览器中从原始上传/审阅链接登录回跳，上传真实 PNG、提交绑定的审阅 envelope 并观察成功状态。CLI 同时核对 HTTP/MCP 的 412、缺/错 Token 在已有 SDK 客户端上的 401、真实短期限 Token 过期和撤销后的 401，以及 Bearer 直调浏览器确认/审核/凭证管理的 403。工具/候选/意图、脱敏状态和截图写入输出目录，凭证不写入；退出时撤销本次签发 Token 并关闭自己创建的浏览器。CLI 不拥有外部 serve 进程，由启动者停止。

本地烟测用于检查当前固定 SDK 与该实例的协议和业务边界。WorkBuddy 实际客户端、HTTPS/OpenResty、真实商城、客户选型事实和模型调用须分别在对应环境验证。
