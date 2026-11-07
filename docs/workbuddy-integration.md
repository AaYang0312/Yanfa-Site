# WorkBuddy 私有管理 MCP 接入

管理端点为同源 `/mcp`，由固定的官方 `github.com/modelcontextprotocol/go-sdk v1.8.0` 提供 stateless Streamable HTTP。生产环境通过 HTTPS 访问；loopback HTTP 仅用于本地开发。初始化、协议协商、POST 响应及 GET/DELETE 行为由 SDK 决定，不提供自制 JSON-RPC 或旧 SSE 桥接协议。

服务使用 stateless 传输，无 `Mcp-Session-Id` 依赖。已鉴权 GET/DELETE 由 SDK 返回 405，缺凭证则先返回 401。目标 WorkBuddy 的协议协商和兼容性须在实际客户端确认。

管理员登录 `/admin/workbuddy/`，选择名称、未来有效期和所需权限后签发 Token。原值仅在签发时显示一次；关闭前按目标客户端支持的安全方式保存，遗失则撤销并重新签发。服务只保存哈希，每次 HTTP 请求和工具调用都核对有效期及撤销状态。客户端已初始化、TCP 连接仍在或此前调用成功均不延长授权。

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
| `create_product` | `products:write` | 全部 ProductCreate 可编辑字段；创建目标下架草稿，稳定 ID 由服务器生成 |
| `update_product` | `products:write` | `product_id/expected_version/edit`；整体替换可编辑数组和字段，返回新版本 |
| `delete_draft` | `products:write` | `product_id/expected_version`；只删除从未公开的草稿，保留 slug 归属 |
| `list_media` | `media:read` | `after/cursor/limit`；私有素材元数据及受登录保护的预览 URL，无磁盘路径 |
| `open_upload` | `media:read` | 可选 `product_id`；需登录的 `upload_url` 与原始 `after` 标记，只返回链接 |
| `create_candidate` | `candidates:write` | `expected_workspace_revision/request_key`；异步冻结整个已保存工作区 |
| `get_candidate` | `products:read` | `candidate_id`；真实构建状态、冻结摘要、差异、过期标记及业务服务原始审阅链接 |
| `request_publication` | `publications:write` | 发布/回退目标、摘要、预期线上 ID/工作区版本、`request_key`；待浏览器确认意图 |
| `get_publication` | `products:read` | `request_id`；真实意图状态、完成时间及成功 release |

仅含 `products:read` 的最小权限 Token 可初始化官方 SDK 客户端、读取候选及其原始审阅链接，并查询同一发布意图的待确认和成功结果，包括原始 ID、审阅 envelope 与实际生效 release。它不具备创建/更新/删除产品、创建候选或创建发布意图的权限；这些工具返回 `isError: true` 和结构化 `FORBIDDEN`。签发只读查询凭证不需要附加任何写 scope。候选、意图及浏览器确认继续复用同一业务服务，不使用提权 Actor 或管理 HTTP 自调用。

写入成功只代表草稿已保存。旧版本更新返回 MCP `isError: true`，`structuredContent.error.code=VERSION_CONFLICT`，并含 `current_version`；对应浏览器 PUT 返回 HTTP 412。保留本地编辑，重新读取后由用户合并，不自动重试旧内容。字段错误同样返回 `isError` 和结构化 `code/message/fields`；内部错误统一脱敏，不返回 SQL、密钥或私有路径。认证和 Origin 失败在 HTTP 边界返回 401/403，协议错误由 SDK 处理。

## 文字修改完整流程

1. `search_products` 查找，`get_product` 取得当前版本和完整文档。
2. 保留 slug、稳定 ID、审核等服务器字段，向 `update_product` 只提交完整 `edit` 与原 `expected_version`；结果供下一次操作使用。
3. `create_candidate` 使用最新工作区 revision 和本次唯一 `request_key`。不要等待该调用完成构建；轮询 `get_candidate`，queued/building 不能报告成功。
4. ready 后展示完整差异、警告和原始 `review_url`。候选冻结整个工作区，包含其他已保存产品的修改。`workspace_changed` 或冲突要求重新生成并审阅。
5. `request_publication` 精确携带候选 ID、SHA、冻结的 expected active/revision，发布时 `release_id=null`。同一次不确定请求重用相同 key 和内容；同 key 不同内容返回幂等冲突。
6. 用户打开服务原样返回的 `review_url`，登录后回到该意图，人工审阅并勾选确认。MCP 没有 confirm、选型事实审核和 Token 管理工具；创建意图不改变官网。
7. `get_publication` 查询执行状态。仅 `succeeded` 且有 `result_release_id/completed_at/site_url` 才报告生效版本；failed/stale/expired/cancelled 均不能当成功。

回退使用同一个 `request_publication`，`kind=rollback`、`candidate_id=null`、`release_id` 指定兼容 schema 的历史成功版本，仍须浏览器确认。回退不修改工作区。

## 图片修改完整流程

调用 `open_upload`，把返回的链接交给用户在浏览器打开。该页面实际登录后可选择本机文件上传；MCP 不读取 `C:\\...`、`/Users/...`、`file:` 或远端文件路径，也不承诺替用户自动打开浏览器。

保留此次返回的 `after`。上传后 `list_media` 使用该固定 `after`，后续分页增加服务器返回的 `cursor`，不要用每页响应的新 `after` 替换当前遍历下界。根据缩略图、说明及上传时间让用户辨认素材，从结果取得 ID；无需用户手工复制 media_id。把已选素材加入完整 `images`，需要时替换 `cover_media_id`，带当前版本保存，再执行同一候选、浏览器确认和结果查询流程。上传未关联图片仍是私有素材，不自动上线。

## 本地官方 SDK 烟测

仅对专用隔离数据运行，下述命令会真实修改一个已导入的上架产品并发布文字、图片两个完整候选。仓库须已安装锁定依赖并生成 site config、构建后台；服务按 migrate → TTY `init-admin` → `import-seed` → `serve` 启动。使用独立 DATA_DIR 和端口，不共享运行中的数据库。`GO_BIN` 指向固定的 Go1.27.3 可执行文件，Go 设置 `GOTOOLCHAIN=local`，TMPDIR/GOTMPDIR 使用私有绝对路径；浏览器子进程独用短 TMPDIR。部署和应用配置见 [运维说明](content-platform-operations.md)。

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
