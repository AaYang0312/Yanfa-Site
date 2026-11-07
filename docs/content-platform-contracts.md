# 内容平台共享契约（schema_version 1）

共享数据和协议的唯一契约是 `schemas/*.schema.json`（JSON Schema draft2020-12）；`schemas/embed.go` 将相同字节嵌入 Go。`https://yuanfa.example/schemas/` 只作 schema 标识，运行时不访问该域名。

## 生成、验证与工具链

```sh
export GO_BIN=/path/to/go1.27.3/bin/go GOTOOLCHAIN=local
npm ci
npm run contracts:generate
npm run site:config
npm run test:contracts
"$GO_BIN" test ./backend/internal/contract/... -count=1
```

`contracts:generate` 从所有 `$defs` 生成 `src/generated/contracts.ts`、`backend/internal/contract/types_generated.go` 和前端使用的 `src/generated/schema-documents.json`。TypeScript 使用 json-schema-to-typescript；Go 字段由同一份定义生成，nullable 标量使用指针。生成前请显式将 `GO_BIN` 指向固定的 Go 工具链，生成器使用其同目录的 gofmt。生成类型不能替代运行校验（条件分支、未知键、引用及跨字段限制仍需验证）。禁止手改生成的 DTO 或维护另一份手写 TypeScript schema。

Go：`contract.ValidateJSON("guide.schema.json#/$defs/GuideRequirements", data)`；`contract.Decode[contract.GuideRequirements](schema, data)` 返回归一化后的 DTO。先做 schema 校验再解码，缺字段、null、false、0 互不混淆。格式错误/尾随第二个 JSON 值为 400 `INVALID_JSON`；字段错误为 422 `VALIDATION_FAILED`，`BusinessError.Detail.Fields` 为点分字段路径。`NewBusinessError`/`BusinessError.Response()` 为 HTTP 和 MCP 提供同一错误结构，内部错误不要写入公开 message。

所有 wire 整数的源 schema 范围统一限制在 `[-9007199254740991, 9007199254740991]` 内，并保留各字段更严格的非负、正数或数量上限；版本/revision/sort_order 不允许在浏览器中舍入。JS 显式拒绝 NaN/Infinity；正常整数 JSON 形式1.0/1e0仍接受。Go 在源校验前将浮点数转换为实际消费的float64表示，再校验同一源约束；正数转换下溢为0必须422并标出字段，不能成为未知或静默零值。最小可表示正数5e-324、普通小数和允许的null保持原义。

`contract.JSONSchema(name)` 将同一源定义打包为仅使用本地 $defs 的独立 JSON Schema，供 MCP SDK/模型参数注册使用；11工具的类型名统一带 MCP 前缀。官方 SDK 的 schema 解析器不必联网加载相对文件，业务处理仍调用 Decode 执行共享扩展语义。schema 的事实/来源摘要是服务器生成并回传的审核绑定值；浏览器/MCP 回送所见摘要，不自行推断审核状态。

前端/后台：从 `src/generated/validate-contract.mjs` 导入 `validateContract(schema, value)` 或 `decodeContract(schema, json)`；成功返回归一化后的值，失败抛出带 status/error 的 ContractError。调用者应使用返回值；材料/基材按 schema 的 x-trim 去首尾空白，其余未知值不补猜。Validator 可被 Astro/Vite 打包，不依赖文件系统或 Node HTTP。

共享扩展关键字也属于契约：x-trim、x-ordered-range、x-history-max-length、x-cover-membership、x-unique-by、x-safe-relative-path、x-catalog-references、x-active-membership、x-product-version、x-release-consistency、x-selection-missing-fields、x-max-pixels。Go/JS 先按 x-trim 以 Unicode White_Space 归一化材料/基材，再执行 draft2020-12 和跨字段约束；只使用裸 JSON Schema 引擎会遗漏这些语义。数组必须显式为数组，不接受 null；[] 是规定的空值，不能在缺字段时默默补入。JSON Schema 中的 default 是说明，不会修改请求。

Go/JS 的归一化及扩展约束遍历会逐层访问 `$ref` 目标，并在目标文档中解析其相对引用；引用节点自身的 siblings 和 `allOf` 仍按原文档上下文执行。`PublishableSnapshot` 的一次共享校验同时执行 `CatalogSnapshot` 的路由/成员引用约束和本地发布组合约束，调用者无需先补一次基础快照校验。MCP 输入/输出包装同样执行目标的数值归一化、封面归属及产品版本一致性，错误继续使用原 422 与点分字段路径。

工具链固定 Node 22.23.4 / npm 10.9.9 / Go 1.27.3；Go 必须 `GOTOOLCHAIN=local`。官方 Go linux-amd64 包 SHA256 为 `65efbc80fdc1fa2cd2631cad5b481777a68e8725f34e89f50fc6ac714e379b40`，安装包及镜像中的副本均须核对摘要，禁止静默换版本。间接依赖固定 Sonic v1.15.5、loader v0.5.3、base64x v0.1.6、cpuid/v2 v2.2.9、golang-asm v0.15.1、x/arch v0.11.0；不使用 replace/fork、强制fallback或禁校验来替代兼容性。精确 npm 版本见 package.json/package-lock.json，Go 版本及全图校验值见 go.mod/go.sum。

命令入口：`contracts:generate` 生成共享类型与 schema 文档，`test:contracts` 校验契约，`site:config` 生成站点配置；`seed:export`（`node --experimental-strip-types scripts/export-initial-catalog.mjs`）导出一次性初始化目录；`build:admin` / `dev:admin`（`astro build/dev --root admin`）构建或预览独立后台。独立后台使用根目录锁定依赖，base=/admin、输出 admin/dist；React 只用于官网导购岛。

## 私有产品、发布和审核

`product.schema.json`：ProductDocument、ProductCreate、ProductEdit、GuideProfile/Edit、GuideReview/Input、PurchaseOption/Edit、ProductEnvelope、PublishedProduct。Create 包含 slug 且目标 unlisted；Edit 不接收 slug/id/version/updated_at 或审核结果。输入为完整可编辑对象，数组整体替换。来源是 `{id,kind,reference,sha256,public_label,public_url}`；product_text.reference 为 JSON Pointer，media/document.reference 为媒体/归档来源 ID。公开 URL 为 HTTPS 或 null。

草稿允许空名称/型号/简介/用途/图片、null 封面和未完成购买地址；用 ProductCreate/Edit/Document 校验。发布用 PublishedProduct（或下述 PublishableSnapshot）：listed 名称非空；listed tool 的型号、简介、图片、封面非空，且用途数组至少一项含非 Unicode White_Space 字符；两种发布投影均引用唯一的 PublishedToolApplications 约束，空字符串/全空白用途不能绕过发布。封面属于 images，图片 media_id 不重复。购买渠道发布时平台/店名/HTTPS 地址须完整，listing_with_variants 必须 sku_hint。路径、媒体是否可解码、引用是否归属及唯一性还需在业务层绑定当前存储事实校验，不能单凭 schema 宣告发布可用。

新购买渠道 id=null，由服务器赋稳定 ID；已有 id 必须属于该产品，删除不复用。`ValidatePurchaseURL(raw, allowedHosts)` 使用 URL 解析器验证 HTTPS、无 userinfo、精确 hostname 白名单；部署白名单只放客户核实的商品主机，不放短链/重定向器。不能从 site.ts 店铺入口猜逐型号映射。

审核字段固定为 scene_ids、materials、substrates、excluded_materials、excluded_substrates、power_source、nail_type、fastener_length_mm、load_bearing、long_nose、blower、limitations、consultation_notes、required_requirements。`required_requirements` 只接受九个访客字段名，必须由人审核；不是表达式或模型生成规则。每次审核保存 field/fact_sha256/source_sha256s/reviewed_by/reviewed_at；管理员针对当前产品版本审核。编辑事实或被引用的文本/图片后，业务层使相应审核失效；MCP 无审核权限。空 required_requirements 不能绕过已知硬条件、pneumatic 气源或动力资料缺口。

`catalog-snapshot.schema.json` 的 CatalogSnapshot 顶层固定 schema_version、release_id、workspace_revision、categories、scenes、nail_types、legacy_series、products、previously_published_routes。SnapshotProduct 保留 ProductDocument 全字段；其 images 扩展为 media_id/label/description/sha256/format/bytes/width/height/path，path 是构建输入相对路径。没有顶层私有媒体目录；不输出系统磁盘路径。PublishableSnapshot 进一步校验每个目标 listed 产品的发布约束。它仍不能代替实际图片解码和资源存在性检查。

分类/场景/钉型使用既有枚举；字典数量来自固定枚举，产品数不固定。系列和产品共用 slug 命名空间、非空 model 大小写不敏感唯一；series.product_ids 与 product.legacy_series_ids 双向一致。slug 永久保留、历史公开资格、渠道 ID 归属/不复用由内容事务与 reserved_routes 维护，不由浏览器判断。

Manifest 固定 schema_version/candidate_id/workspace_revision/code_version/snapshot_sha256/routes/resources，资源含相对 path/SHA256/bytes。ActiveRelease 固定 schema_version/release_id/snapshot_sha256/operation_id/activated_at/successful_release_ids/previously_published_routes，当前 release 必须在成功列表。历史路由含 product_id/slug；成功版本 manifest 形成历史资源索引。candidate、manifest、active 的 SHA 与文件存在性由发布/恢复层核对；active 是唯一公开版本权威。

## 公开目录与确定性判定边界

`public-catalog.schema.json` 提供 PublicProduct、ReviewedGuideProfile、PublicSource/ReviewedSource、PublicPurchaseOption、CatalogQuery、ProductList/Detail、PublicExport、PurchaseOptionsResult、ReleaseResponse、MatchDecision。列表、详情和导出复用 PublicProduct；包含 schema/release/product/version、展示字段、公开图片、审核后的事实、unknown_fields、匹配依据/缺口和服务端生成的 handoff_url。公开来源只有 field（逐字段来源时）、public_label/public_url，不含私有 reference/hash、审核人或账号；未审核字段以 null/[] 和 unknown_fields 表示，不能同时泄露未审核值。

CatalogQuery 支持 q/category/scene/nail_type/material/power_source/fastener_length_mm/cursor/limit/release_id/include_unknown。limit 默认20/max100；默认值由处理器设置。cursor 绑定 release+规范化查询+最后 sort_order/id，旧 release 或换查询返回409。列表/详情/导出内的嵌套产品 release 必须一致。私有快照不通过 export 直出，任何 Cookie 均不能扩大公开范围。

公开搜索和导购共用 `matching.Evaluate(req contract.GuideRequirements, profile contract.ReviewedGuideProfile) contract.MatchDecision`。硬冲突优先；unknown 不等于匹配；材料只 trim 后匹配审核名称，不猜同义词；长度闭区间。pneumatic 要求 air_supply：null 缺口、false 冲突、true 只通过气源；动力未知仍有资料缺口。false 功能偏好表示不要求，不表示禁止。missing_fields 按九访客字段顺序、再 guide_profile 字段字典序去重；公开 MissingField 限定这两个集合。审核 required_requirements 之外的已知硬条件也不能跳过。

公开购买选项固定 purchase_option_id/platform/store_name/match_scope/sku_hint/destination_host/handoff_url；没有目标商城 URL。`/go/products/{id}/{option_id}?release_id=...` 从同一 active 查归属、启用/上架、地址/白名单；成功303 no-store，旧版409说明页、下架或撤销404，不换型号/店铺。

## 导购请求、提议与结果

GuideRequirements 恰九个必填字段：scene_ids、material、substrate、nail_type、fastener_length_mm、air_supply、load_bearing、long_nose、blower。未知标量 null，scene_ids=[]；去重场景最多10；材料/基材 trim 后1–100字符；长度有限正数（mm），不接收数字字符串；布尔不以 false 代替未知。

ConsultRequest 为 release_id/message，附 messages?/requirements?/context_product_id?。当前纯文本最多2000字符，历史仅 user/assistant，最多12条、总计16000字符；禁止system/tool和派生内容。只有首轮未传 requirements 才由业务调用 UnknownRequirements() 初始化；传了不完整对象则422。SelectionRequest 必须 release_id/selected_product_id/requirements，不接收 message/messages/context_product_id/match_level/missing_fields/URL；完整条件由最新成功结果回传，显式选择绕过模型。

内部 GuideModelProposal 固定 intent/requirements/candidate_product_ids/selected_product_id/selection_reference 五字段。consult 时后两项必须null；select 时二者非空，selection_reference 是当前消息中明确型号/稳定ID的原文引用。schema 校验形状与互斥；导购服务还必须验证引用实际存在于当前消息、对应同版目录唯一稳定ID且表达明确选择。仅询问/比较型号保持consult；“第二款”等含糊引用、context 或仅剩一候选均不能当作选定。歧义返回 question.field=null、text=“请点击要选择的商品卡片”、options=[]，不能伪造 requirements 缺口。

同次 Eino 提议先在已有条件上合并当前明确更正，保留未改字段和 null，再送同一校验；解析后直接调用导购服务的 Select，不追加模型调用。提议不接收匹配等级、缺口、购买渠道或 URL；它不是浏览器请求分支。

GuideResult 的 state 为 clarifying/recommendations/selected/no_match/unavailable；含完整 confirmed_requirements、必填 conflicts:string[]、question（无问题=null）、最多3个 recommendations、selection（仅selected非null）、missing_fields、contact_href。卡片固定 product_id/model/name/detail_url/image/match_level/matched_facts/missing_fields/confirmation_notes。selection 另有 purchase_options/purchase_status；无渠道仍 selected/200、purchase_status=unavailable。selected 顶层和卡片缺口必须完全一致；matched 无缺口，needs_confirmation 有缺口。no_match 的 conflicts 为1–100条确定性说明，question=null、selection=null、recommendations=[]，不提供购买按钮；其余四种状态必须显式 conflicts=[]。每条说明按既有 Unicode White_Space trim 后为1–1000字符，拒绝空白、缺字段、null、错类型和超限；允许重复原文，不截断、不补默认数组。

单商品 no_match 直接承接本次 matching.Evaluate 返回的自有 MatchDecision.Conflicts，保留原文、顺序及真实 MissingFields；其他状态初始化空数组。air_supply=false 的已审核 pneumatic 冲突使用现有供能说明；null 仍是缺口，true 只消除气源缺口，动力未知仍需确认。conflicts 不是第十个访客条件，也不加入请求、模型提议、推荐或 selection；请求伪造仍422。匹配和购买判定保持同一 Evaluate 与原渠道边界。

咨询只有同版完整公开 catalog.Service.Export 的 Products 为空，或逐项 Evaluate 全为 no_match，才能证明目录无候选；模型空列表、搜索空页、部分复核及资料不足均不能证明。空目录说明固定为“当前已发布目录暂无可供导购的上架商品，请联系咨询。”；全冲突按商品原顺序和决策原顺序聚合，完整字符串去重，展示前100条，仍完成全目录判定。存在无硬冲突但证据不足的商品时保留真实缺口及澄清/咨询；已有 matched 但提议无可用候选时返回 unavailable，不补造推荐或追加模型调用。

电话只来自 `src/data/site.ts`。`site:config` 使用 Node22 的 TypeScript stripping 读取现有 site/telHref，生成单一 `src/generated/site-config.json`（schema_version/name/phone/contact_href）。本地 SITE_CONFIG_PATH 指向该文件；运行镜像放 /app/site-config.json。禁止另一处电话号码运行常量。

## HTTP、MCP 与 Go 服务接口

`schemas/http-api.openapi.yaml`（OpenAPI3.1.0，info.version=1.0.0；JSON 是合法 YAML）列出 HTTP 路径及输入/输出、错误、权限和请求头。管理JSON上限256KiB，no-store；浏览器写要会话+CSRF+精确Origin。产品 GET 返回强ETag "version"，PUT/DELETE/guide-review 缺If-Match为428，旧版本412。审核 body.version 也要和当前文档一致。MCP Bearer 不授权浏览器管理API。会话12小时，Secure/HttpOnly/SameSite=Strict，仅loopback开发放宽Secure。

公开目录、后台产品与媒体的 OpenAPI 查询参数直接引用源 schema 的对应属性；x-trim 在校验前执行同一 Unicode White_Space 规范化。OpenAPI消费者可将实际提取的 parameter.schema.$ref 交给 validateContract 验证/归一化该参数；Go HTTP适配层解析参数类型后使用对应共享查询DTO。禁止另写材料pattern或独立数字边界。

候选 queued/building/ready/failed；PublicationState 为 awaiting_confirmation/queued/activating/succeeded/stale/expired/cancelled/failed。publish 使用 candidate_id 且 release_id=null；rollback 相反。意图绑定snapshot SHA、expected active/workspace，24小时有效；candidate/request/confirm 按身份+接口+Idempotency-Key存24h，相同请求复用，异内容409。后台响应/查询/Token/媒体/差异/预览/健康 DTO 位于 `admin.schema.json`，业务处理由相应服务执行。

`mcp-tools.schema.json` 的 x-tools 与 $defs 发布11工具的输入、输出和scope：search_products、get_product、create_product、update_product、delete_draft、list_media、open_upload、create_candidate、get_candidate、request_publication、get_publication。输出错误为 isError=true + structuredContent=ErrorResponse。官方SDK负责 stateless Streamable HTTP协商；每次检查token期限/撤销，有Origin则精确白名单。没有confirm、review、token管理工具。open_upload只给受登录保护的页面链接和after游标，不读取本地文件路径。

MCP search_products 的 MCPSearchProductsInput 为 query/category/visibility/cursor/limit；HTTP AdminProductQuery 使用 q。两种wire类型独立并拒绝对方的别名；MCP 适配层只把已校验的 query 映射到共用业务查询，不提供兼容wire别名。

主要 Go 服务接口：

```go
content.Service.Create(ctx context.Context, actor auth.Actor, edit contract.ProductCreate) (contract.ProductEnvelope, error)
content.Service.Replace(ctx context.Context, actor auth.Actor, id string, expected int64, edit contract.ProductEdit) (contract.ProductEnvelope, error)
content.Service.Review(ctx context.Context, actor auth.Actor, id string, expected int64, input contract.GuideReviewInput) (contract.ProductEnvelope, error)
publication.Service.CreateCandidate(ctx context.Context, actor auth.Actor, revision int64, key string) (contract.Candidate, error)
publication.Service.Request(ctx context.Context, actor auth.Actor, input contract.PublicationInput, key string) (contract.PublicationRequest, error)
publication.Service.Confirm(ctx context.Context, actor auth.Actor, id string, input contract.ConfirmationInput, key string) (contract.PublicationRequest, error)
release.Manager.Capture(expectedID string) (*release.Handle, error)
release.Manager.BeginResponse(expectedID string, start func() error) error
catalog.Service.Search(ctx context.Context, h *release.Handle, q contract.CatalogQuery) (contract.ProductList, error)
catalog.Service.Get(ctx context.Context, h *release.Handle, id string) (contract.PublicProduct, error)
catalog.Service.Export(ctx context.Context, h *release.Handle) (contract.PublicExport, error)
catalog.Service.PurchaseOptions(ctx context.Context, h *release.Handle, id string) (contract.PurchaseOptionsResult, error)
matching.Evaluate(req contract.GuideRequirements, profile contract.ReviewedGuideProfile) contract.MatchDecision
guide.Service.Consult(ctx context.Context, h *release.Handle, req contract.ConsultRequest) (contract.GuideResult, error)
guide.Service.Select(ctx context.Context, h *release.Handle, req contract.SelectionRequest) (contract.GuideResult, error)
guide.WriteUIMessageStream(ctx context.Context, w http.ResponseWriter, result contract.GuideResult) error
```

Service方法用指针接收器，Actor仅认证层构造；Handle不可变且不引用workspace；业务错误统一 *contract.BusinessError。进程内Gate用于工作区最终提交、激活和BeginResponse短读锁；DATA_DIR另有全生命周期排他flock，冲突DATA_DIR_LOCKED退出73。离线备份/恢复复用该锁，不提供在线维护API。

## AI SDK 消息流与客户端提交

锁定 @ai-sdk/react4.0.167/ai7.0.163。Go 对外先完成业务校验与发布版本复核，再发 `Content-Type: text/event-stream`、`x-vercel-ai-ui-message-stream: v1`、`Cache-Control: no-store`；不发Eino内部事件或模型原始token。

成功事件顺序：start(messageId) → text-start(id) → text-delta(id,delta，可多块) → text-end(id) → 唯一 data-guide-result(data=完整GuideResult) → finish → `data: [DONE]\n\n`。每个事件用JSON编码，UTF8/中文/换行正确转义；文本id一致，逐事件flush，无模拟打字延时。选择请求始终JSON；不可用JSON返回503 GuideResult，开流前409/422/429按错误JSON返回。开流后失败只能写sanitized error后终止，不发成功finish，不伪造503。

前端必须旁路观察原始流的data数量、finish、DONE、error、abort、EOF；唯一完整data+finish+DONE且无错后，再读/api/catalog/release并原子提交。useChat.status=ready 或 onFinish 不能单独作为提交依据；会话未完成/失败更正必须保留pending，禁用旧选择/购买。静态协议样例不能替代真实模型、Go导购、useChat页面或OpenResty链路的验证。

JSON 与 SSE 的 data-guide-result 严格消费同一必填 conflicts 数组。no_match 的说明由服务端决策提供，下游逐项以纯文本展示并保留咨询入口，清除旧选择/购买；未知证据显示真实缺口与无法确认。旧 sessionStorage 完整结果缺 conflicts 时拒绝恢复为当前结果；可分别校验保留纯文本历史和九字段需求，清除操作卡并提示重新咨询/选择，不补[]，不更改 yuanfa.guide.v1/version1。

部署至少配置 SITE_URL/DATA_DIR/HTTP_ADDR/TRUSTED_PROXIES/MODEL_PROVIDER/MODEL_BASE_URL/MODEL_MODEL/MODEL_API_KEY/PURCHASE_ALLOWED_HOSTS/TEMPLATE_DIR/ADMIN_STATIC_DIR/SITE_CONFIG_PATH。MODEL_PROVIDER=openai；未配置模型时咨询unavailable，独立目录和显式选择仍可运行。模型20s/8轮、IP10次每分钟、并发4；无自动重试/failover。真实客户模型、WorkBuddy、商城和OpenResty须在各自运行环境验证。

## 契约验证入口

Node 与 Go 的契约回归使用 `tests/contracts/consumer-cases.json`、`consumer-boundaries.test.mjs`、`openapi-parameters.test.mjs` 和 Go `consumer_test.go`，检查数值边界、typed DTO、完整发布快照及 OpenAPI 参数。CatalogSnapshot/PublishableSnapshot 共用的校验涵盖保留系列 slug 冲突、成员引用、封面、范围、相对媒体路径、发布用途和嵌套条件的 Unicode trim/null/false。GuideResult 校验涵盖五状态、conflicts 的100项/1000字符边界及请求伪造；容量合成数据只证明 wire 边界。

浏览器消费回归使用已安装的锁定 Playwright 和可执行 Chromium。`CHECK_OUTPUT` 指向本次私有输出目录：

```sh
npm run test:contracts
"$GO_BIN" test ./backend/internal/contract/... -count=1
BROWSER_PATH=/path/to/chromium node tests/contracts/browser-consumer.mjs "$CHECK_OUTPUT/contracts-browser"
```

浏览器入口输出观察JSON与截图，并在结束时关闭页面、浏览器和临时HTTP服务。未指定 BROWSER_PATH 时使用已安装的 Playwright Chromium。契约检查不代替实际供应商调用、完整业务服务或生产部署验证。

