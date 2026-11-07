# Aisle 导购 UI 来源与消费边界

导购 UI 移植自 [Roboto Studio / turbo-start-aisle](https://github.com/robotostudio/turbo-start-aisle/tree/fd8e049bd14d4d4be857afed0b2f1f8efa015d3f)，固定上游提交 `fd8e049bd14d4d4be857afed0b2f1f8efa015d3f`。MIT 版权为 `Copyright (c) 2025 Roboto Studio`；[完整许可](../licenses/aisle-MIT.txt)逐字保留，上游许可 SHA256 为 `d5063d75af241249858e00754f66fbbf81e8df1872d11ada29175ee657011ace`。

上游路径均位于 `packages/ai-commerce/src/ui/`：

| 上游文件 | 上游 SHA256 | 本地移植及改动 |
| --- | --- | --- |
| chat-widget.tsx | af628d978b9adac7078b6a5bb93f15833360296cb250104a26886d3785752187 | `src/components/guide/chat-widget.tsx`：沿用 launcher、开关状态与常驻隐藏面板；增加入口焦点返回和 visualViewport 键盘适配。 |
| chat-panel.tsx | 93becb6b534ec1567f2c59d20f2e538d9e95cb11f6b13e1c05ffd10b29057d6a | `src/components/guide/chat-panel.tsx`：保留 useChat / DefaultChatTransport 及面板、消息、输入组合；接入公开 Go API、协议和版本提交栅栏。 |
| message-input.tsx | f089175ce03d262a166e8af613054395af4e7d1c53ea47b9afefbf77f4c4d483 | `src/components/guide/message-input.tsx`：沿用 trim、发送后清草稿、Enter / Shift+Enter；替换共享控件并增加 IME 与消息长度边界。 |
| message-list.tsx | 60a39e8a258796f1cc674ad3b25a28c064b35b42df98c892954fed37e9440343 | `src/components/guide/message-list.tsx`：沿用 user / assistant 消息分组、底部锚点和加载状态；只呈现纯文本完整历史。 |
| product.tsx | f21755e77395eb8ae5d00f7c1c866b5b8bcf58897c6f914727f18649d948461f | `src/components/guide/product.tsx`：保留图、标题、详情链接和显式商品动作布局；数据改取严格 GuideResult，购物车动作改为 JSON 选定与服务端商城入口。 |

此集成不包含上游的 Next 导航、Sanity 查询、Shopify 购物车、货币/库存/价格逻辑、截图与自动改筛选工具。`::document` 留作普通文本；只有结构化结果能创建卡片。样式限定于 `.yg`。应用端没有 Aisle Node 聊天服务或客户端工具执行入口。

## 公开接口与完整提交

- 咨询仍由真实 `@ai-sdk/react` useChat 和 `ai` DefaultChatTransport 驱动。`prepareSendMessagesRequest` 只提取当前纯文本、最多12条且总计16000字符的 user/assistant 历史、上次完整九字段 requirements、页面 release 和可选产品上下文；不转发 UIMessage 的 data/tool/system 部分。
- 显式“选这款”独立 POST `/api/guide/messages`，`Accept: application/json`，请求只有 `release_id`、`selected_product_id`、完整 `requirements`。返回与文字选定的 SSE selected 进入同一个 `GuideSession.commit` 和 `GuideResultView`。
- `src/scripts/guide/protocol.ts` 观察实际交给 SDK 的响应字节：start → 同 ID 文本块 → 唯一严格有效 data-guide-result → 成功 finish → `[DONE]` → 正常 EOF。错误、取消、重复 data、缺事件或异常 EOF 不提交。SDK 的 ready / onFinish 不能独立提交结果。
- JSON 和 SSE 都先通过当前共享 GuideResult schema，再 GET `/api/catalog/release` 复核。请求代次检查拒绝晚到的已取消提交。只有完整事务成功后才同时更新 result、九字段 requirements、纯文本完整回复与 pending 状态。
- no_match 必须有非空 `conflicts`，逐项纯文本显示并提供 `contact_href`；无选择或商城按钮。其他所有状态都必须显式 `conflicts: []`。缺字段、默认数组兼容、非法状态数组一律拒绝。
- selected 的服务端缺口、确认提示和商城入口同屏。未知资料显示“无法确认”；无映射保留详情和咨询。商城链接取服务端 `/go/...`，点击前再次复核 release，默认同标签导航，最终目标仍由 Go 跳转服务校验。

## 当前标签页恢复

sessionStorage key 为 `yuanfa.guide.v1`，version 为1。持久字段为 `messages`、`result`（最后完整 GuideResult 或 null）、`requirements`、`release_id`、`pending_turn`。pending 包括 `request_id`、`kind`（consult/select）、`text`、`selected_product_id`（咨询为 null）、`correcting: true`。

发送前同步持久 pending 并停用旧操作。失败、取消和离页保留 pending；关闭面板保留组件与输入草稿。访客可重新提交或明确撤销；撤销后也须当前 release 复核。旧 release / 非法本地结果清除可操作结果，分别保留可验证历史及条件；缺 conflicts 的旧记录不补字段。跨版本未完成选择只能通过访客再次点击“重新提交”，以当前页面 release 和原稳定产品 ID 重新走生产 Select。存储关闭或写入失败明确提示仅当前页面内存会话；清空操作取消本轮并删除本标签页记录。

## 验证入口与覆盖边界

`node --experimental-strip-types --test tests/guide-state/*.test.mjs` 覆盖协议分块、完整结束、严格结果、取消/切版提交、pending 恢复和存储失败。

普通隔离 Go 服务初始化并构建后台后执行：

```sh
node tests/browser/guide-client.mjs --base "$SITE_URL" --out "$CHECK_OUTPUT/guide-client"
```

`CHECK_OUTPUT` 为本次私有输出目录。脚本从0600的 `SMOKE_ADMIN_PASSWORD_FILE`读取密码，通过真实管理 API 录入并审核隔离规则、构建和确认发布页面。浏览器咨询拦截仅提供协议 fixture；显式选择调用真实 Go 服务，并检查流完成到 release 复核之间发生切版时的提交行为。使用仓库锁定的 Playwright、可执行 Chromium 和独立端口/data；浏览器的 TMPDIR 使用短目录，例如 `/proc/self/cwd/.work/browser-tmp`。

SSE fixture 与 `.test` 商城映射只覆盖本地消费者和规则边界；真实 Go SSE 编码、Eino/模型调用、OpenResty及商城目的页须分别在对应环境验证。headless Chromium 的缩小 visual viewport 与原生键盘焦点截图不能替代实体移动设备软键盘检查。
