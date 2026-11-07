# 内容平台运维合同

生产 HTTP 入口为一个 Go 进程。镜像包含可执行 Node.js 22.23.4、npm 10.9.9、受控 Astro 模板及锁定依赖，Node 仅在发布构建期间作为子进程启动。独立后台为静态产物，不运行 Node/Next.js 聊天服务。Eino、MCP 和导购由同一 Go 应用承载。

## 构建和不可变输入

`Dockerfile` 固定 Go1.27.3、Node22.23.4 官方镜像的 tag 和索引 digest，镜像来源及 linux/amd64 子 manifest 记录在 `deploy/image-lock.json`。构建使用提交内的 go.mod/go.sum、package-lock.json；发布期间不执行 Git 拉取或 npm install。OS 安全包由固定 Debian 基础镜像的发行源安装，最终镜像必须记录实际 digest；基础镜像升级、OS 包更新或依赖升级后重新验证。

在仓库根目录执行，`GO_BIN` 须指向 Go1.27.3 的可执行文件：

```sh
export GOTOOLCHAIN=local
mkdir -p .work/bin
npm ci --no-audit --no-fund
npm run site:config
npm run build:admin
node scripts/package-runtime.mjs --root "$PWD" --out .work/runtime
"$GO_BIN" build -o .work/bin/yuanfa ./backend/cmd/yuanfa
"$GO_BIN" build -o .work/bin/yuanfa-backup ./backend/cmd/yuanfa-backup
```

`package-runtime.mjs` 是唯一运行模板打包入口，复用 `copyTemplate` 白名单。输出目录必须不存在。产物为 `template/`（真实、可搬移的 node_modules，含锁文件、schema、构建与审计脚本）、`admin/`、`site-config.json`、`licenses/`、`runtime.json`。不携带私有产品库、构建工作目录、仓库 Git、旧图片源或开发机依赖绝对链接。Go 二进制另由 Go 构建阶段放入 `/app`。运行路径固定：

| 配置 | 镜像路径 |
| --- | --- |
| TEMPLATE_DIR | /app/template |
| ADMIN_STATIC_DIR | /app/admin |
| SITE_CONFIG_PATH | /app/site-config.json |
| DATA_DIR | /data |
| HTTP_ADDR | 0.0.0.0:8080 |

依赖许可文本与适用 NOTICE 在 `licenses/dependencies/`，inventory 记录锁文件摘要、版本、上游及文本摘要。需要重新收集时，在已按锁安装依赖的根目录运行 `GO_BIN=<固定Go路径> node deploy/collect-licenses.mjs`。打包保留完整 `licenses/` 目录，包括 `licenses/aisle-MIT.txt`；Aisle 的上游提交、版权和移植文件映射见 [Aisle 来源说明](aisle-provenance.md)。

## 1Panel、网络和配置

由获授权运维在 1Panel 创建专用编排，使用 `deploy/compose.yml`。实际 OpenResty 使用 host 网络；Go 保持独立的 Compose 默认 bridge，只把容器8080发布到宿主 `127.0.0.1:${YUANFA_HTTP_PORT:-18080}`，默认面板反向代理目标为 `http://127.0.0.1:18080`。不需要外部共享网络；不将 Go 改成 host 网络或发布到公网地址。不要手改 1Panel 生成文件，不创建额外 `default_server`。

上线前配置：

- `SITE_URL`：唯一权威 HTTPS origin；后台 Cookie、Origin/CSRF 与它一致。首发前公开站点 503，后台可初始化。
- `YUANFA_DATA_DIR`：专属绝对宿主目录，预先由授权管理员创建并交给 UID/GID 1000；不与静态站点目录混用。compose 禁止自动创建宿主路径。
- 备份输出目录同样由 UID1000 持有且权限0700；脚本在停服前检查这一条件，root 调度须先由授权运维准备空目录，不能让一次性非root容器写入 root 专属目录。
- `YUANFA_HTTP_PORT`：宿主回环 HTTP 上游端口，默认18080；独立实例必须覆盖，容器内仍监听8080。它不是对外 TLS 端口，不得把该 HTTP 地址填成 `SITE_URL`。
- `TRUSTED_PROXIES`：必填实际请求中 Go 观察到的 OpenResty 单个 peer，IPv4 使用 `/32`，IPv6 使用 `/128`。loopback 发布经 Docker 转发后可能表现为 bridge gateway，不能盲填 `127.0.0.1`。先 inspect 本实例网络以定位候选，再以该实例实际连接的 peer 核验；禁止直接信任 inspect 得到的整个 subnet、任意 Docker 网段、公网或 TEST-NET 样例。尚无请求证据时保留为未验证，不能声称转发链可信。
- `PURCHASE_ALLOWED_HOSTS`：客户核实的精确商城 hostname，逗号分隔，不含通配符、协议或路径。
- `MODEL_PROVIDER=openai`、`MODEL_BASE_URL`、`MODEL_MODEL`、`MODEL_API_KEY`：客户授权模型配置，仅经受保护的环境文件或 1Panel 变量注入 Go，不写源码、镜像层、命令行或日志。
- `YUANFA_IMAGE`：构建和审查时使用有版本的 tag；投产前记录仓库推送/拉取获得的实际 `repository:version@sha256:...` 并以该引用部署。不要以本地未构建 tag 代替镜像 digest 证据。

授权环境中的构建/启动示例，仅引用由运维预备的受保护环境文件（权限0600），不创建、打印或复制现有秘密；模型 Key 等只放在该文件或受保护的面板变量中。先用同一项目、文件和环境执行 `config --quiet`，不要把可能含秘密的完整 Compose config 写入日志：

```sh
docker compose -p yuanfa-production --env-file /absolute/protected/yuanfa-production.env -f deploy/compose.yml config --quiet
docker compose -p yuanfa-production --env-file /absolute/protected/yuanfa-production.env -f deploy/compose.yml build
docker compose -p yuanfa-production --env-file /absolute/protected/yuanfa-production.env -f deploy/compose.yml up -d
```

执行账户须具备 Docker/Compose 管理权限。配置解析成功不代表域名、TLS、模型或客户端已正确配置；应按实际部署逐项验证。

### 独立实例

下表及命令仅为配置示例：`.invalid` 是保留域名，不是真实 origin；TLS 端口也只是示例，须由授权运维配置真实域名、证书和独立监听。两个实例各有受保护环境文件，注入各自经实际请求核验的 `TRUSTED_PROXIES`、固定镜像和所需模型配置；不复用数据库、会话、数据目录或 TLS 入口。

| 实例 | Compose 项目（`-p`）/服务 | `YUANFA_DATA_DIR` | `YUANFA_HTTP_PORT` / 两处 HTTP 上游 | `SITE_URL` / 独立 TLS origin |
| --- | --- | --- | --- | --- |
| A | `yuanfa-a` / `yuanfa` | `/srv/yuanfa-a/data` | `18081` / `http://127.0.0.1:18081` | `https://a.example.invalid:18481` |
| B | `yuanfa-b` / `yuanfa` | `/srv/yuanfa-b/data` | `18082` / `http://127.0.0.1:18082` | `https://b.example.invalid:18482` |

```sh
YUANFA_DATA_DIR=/srv/yuanfa-a/data YUANFA_HTTP_PORT=18081 SITE_URL=https://a.example.invalid:18481 \
  docker compose -p yuanfa-a --env-file /absolute/protected/yuanfa-a.env -f deploy/compose.yml up -d
YUANFA_DATA_DIR=/srv/yuanfa-b/data YUANFA_HTTP_PORT=18082 SITE_URL=https://b.example.invalid:18482 \
  docker compose -p yuanfa-b --env-file /absolute/protected/yuanfa-b.env -f deploy/compose.yml up -d
```

服务键保持 `yuanfa`，由不同 `-p` 分隔容器及默认 bridge 身份。初始化的一次性 `compose run --rm --no-deps`、后续 stop/start 和运维操作必须使用同一项目、环境文件及端口变量。通过 `YUANFA_HTTP_PORT` 改变基础 Compose 的唯一端口映射，不用追加 `ports` 的 override；以实际 `compose config` 确认仅有对应的 `127.0.0.1:18081:8080` 或 `127.0.0.1:18082:8080`，没有遗留18080映射。检查完整配置时避免将含秘密的内容写入日志。

分别在面板支持的配置入口配置各实例的 TLS server：主反代与该实例 SSE location 必须同时指向表中对应的 HTTP 上游，在主反代设置和 SSE 片段中各自替换端口，不能共同使用默认18080。两个位置均设 `proxy_set_header Host $http_host;`、`X-Forwarded-Proto $scheme`、`X-Forwarded-For $proxy_add_x_forwarded_for`，从而保留请求 authority 的非默认 TLS 端口；`$host` 会丢掉该端口。实际请求的 HTTPS authority 必须与本实例 `SITE_URL` 一致；内部 HTTP 上游不是 TLS origin。

容器以 UID/GID 1000、只读根文件系统、无额外 capabilities 运行，应用仅写 `/data`。`restart: unless-stopped`；SIGTERM 退出等待构建子进程、HTTP 与数据库关闭后释放锁，compose 预留330秒。`GET /health/live` 检查进程；`GET /health/ready` 分别显示数据库、产物和模型状态。首发前 ready 503 合法；有完整 active 后模型未配置仍可浏览官网。模型配置状态不代表真实调用已通过。

通过面板支持的配置入口加入 `deploy/1panel-openresty.conf` 的 SSE 精确 location，并在常规代理上使用同一实例的回环上游和上述转发头，保留 Go 的 Cache-Control/Set-Cookie。`/api/guide/messages` 关闭 response/request buffering、proxy cache、gzip/上游压缩、自动重试；如面板启用 Brotli，同样在面板支持的入口关闭该路由压缩。HTTP/1.1，5秒连接及60秒读写/发送期限，断连传 Go context。常规代理也关闭共享 `proxy_cache`；常规 HTML 重新验证；哈希资源 immutable；带会话/预览的 HTML、后台和 API 不共享缓存。禁止为 `/data`、私有媒体、catalog.json、构建日志或备份设置静态公开目录。

生产配置应经实际 TLS/OpenResty 的真实浏览器检查 SSE 分块、中文/多行、finish/DONE、取消释放和 JSON 错误；还应通过 inspect/top 检查非root、镜像 digest、挂载及构建结束后仅 Go 常驻。无 Docker/1Panel 权限时这些项目仍需在部署环境验证，不能以本地产物替代。

## 初始化、发布及恢复边界

在 serve 停止时，用同一镜像的一次性命令依次执行 `yuanfa migrate`、TTY 交互的 `yuanfa init-admin --username admin`、`yuanfa import-seed --seed /seed/initial-catalog.json --media-root /seed-media`。导入介质必须包含种子 JSON、相邻 `provenance/archive-records.json` 及其中引用的文件，以及完整媒体根目录，并作为独立只读挂载提供；它们不进入日常运行模板。密码不进入 argv/env/日志。首次发布通过后台创建候选、审阅、确认；日常产品维护和发布也只走后台/MCP 的既有权限合同。

`state/active.json.operation_id` 是激活身份的唯一权威，不能用时间或 release ID 猜测。启动按该指针修复切换后的 DB 回执；切换前中断不自动发布。备份完整保留 `candidates.validation_json` 中的 `baseline_operation_id` 和 `reconciled_operation_id`，不裁剪私有元数据，没有另需备份的基线 sidecar。历史成功版本、曾公开路由与资源保留；不得自动清理历史媒体。

## 唯一备份方案：每日短暂停服

部署脚本接口固定：

```sh
bash deploy/backup-offline.sh \
  --compose /absolute/deploy/compose.yml \
  --data /absolute/persistent-data \
  --output /absolute/backups/new-unique-backup \
  --health-url https://authoritative-origin.example
```

脚本需要获授权的 Docker/compose、python3、curl、coreutils 和 Bash，使用同一已部署 compose 配置及环境。它在停服前核对 compose 解析出的可写 `/data` bind source 与 `--data` 相同，记录原运行状态并安装恢复 trap。先 `stop -t 330 yuanfa` 并确认已退出，再用同一服务镜像一次性运行 `/app/yuanfa-backup create`，持源锁完成全部导出、复制和校验，随后独立只读 verify。成功/失败/INT/TERM 都恢复原先运行的服务，检查 live/ready；原先已停的服务保持停止。停服失败不备份；复服或健康检查失败也返回非零，保留诊断。SIGKILL、宿主掉电无法执行 shell trap，运维须依原状态记录处理，不强开锁。

脚本内部服务键固定为 `yuanfa`，调用时须在受保护的调度环境注入与部署相同的全部 Compose 变量，并设置 `COMPOSE_PROJECT_NAME` 为启动时 `-p` 的值（例如 `yuanfa-production`），使其 stop/run/start 命中正确实例；脚本不会自动加载 CLI 启动时的 `--env-file`。数据目录、`YUANFA_HTTP_PORT`、`SITE_URL`、镜像及精确 peer 信任值须与原实例一致，`--health-url` 使用该实例的真实 HTTPS origin。不要把秘密写进命令行或日志。

执行账户还须具备输出目录的宿主访问和实际枚举权限；Docker/compose 管理权限不替代宿主文件权限。无法枚举时，脚本在创建控制日志、调用 Docker 或停服前返回非零，并保留 find 原始诊断。

源和输出必须互不包含，输出必须新建或空目录，不能覆盖上一份。诊断写 `BACKUP_CONTROL_DIR`（默认备份父目录的 `.work/`，0700/0600），失败包保留 `failure.json`，无成功 manifest。禁止在线复制 catalog.sqlite/WAL，禁止释放锁再复制，也不调用在线维护 API。凭证不进入备份调度日志，compose 完整配置仅在管道内解析。

隔离本地环境可使用同一脚本的显式本地模式：`--local-server <yuanfa绝对路径> --local-pid-file <本次PID文件> --binary <yuanfa-backup绝对路径>`，替代 `--compose`，其余参数相同，继承该隔离实例的 DATA_DIR/SITE_URL/HTTP_ADDR。先核对 PID executable 与进程 DATA_DIR，再停止/重启真实 Go。此模式可检查失败复服行为，不能替代容器环境验证。恢复后的 PID 写回控制文件，由调用者负责最终关闭。

只有原服务成功恢复后才将已封存备份送到获授权异地目标，传输后再 verify；每日执行，异地至少保留7份。异地目标和执行权限由获授权运维配置，脚本不假设远端地址，不自动删除源或历史包。

## CLI 和完整保留集合

```sh
yuanfa-backup create --data /stopped-data --output /new-backup
yuanfa-backup verify --backup /sealed-backup
yuanfa-backup restore --backup /sealed-backup --data /empty-target
```

create/restore 从首次源/目标访问到最终完整校验持续持 `flock(LOCK_EX|LOCK_NB)`。`.yuanfa-data.lock` 永不删除、替换或重命名，冲突立即输出 `DATA_DIR_LOCKED`、退出73，不等待、不偷锁。verify 只读封存包并以 immutable SQLite 连接校验，不访问运行实例。

备份私有布局为 `manifest.json`、`data/db/catalog.sqlite`、`data/media/`、`data/artifacts/`、`data/state/active.json` 及保留的 `data/work/` 输入/日志/已存在 staging 产物。独立数据库由 modernc SQLite backup API 分页导出，包含已提交 WAL，再将导出副本转为无 WAL 的独立数据库；不按表导出，不丢会话、凭证哈希、候选私有字段或历史操作。全部保留媒体行（包括草稿专属、未关联素材）均要求原文件存在并核对字节数/SHA。所有保留候选冻结 JSON 和已有 artifacts、active、成功历史和哈希资源都在恢复集；工作目录中的模板、缓存和 node_modules 链接是可再生构建数据，不跟随复制成共享依赖。每个候选冻结 JSON 以 DB 原字节保留。

verify 检查备份格式/schema、精确文件清单/SHA/长度、SQLite integrity/foreign keys、嵌入 migration 版本/名称/SHA、工作区与冻结媒体引用、候选/artifact/manifest/code 身份、active 的全部成功资源与操作归属。拒绝符号链接、路径越界、缺文件、摘要错误或不支持 schema。失败不删除媒体记录、不发布成功清单、不替换上一份。

## 在空目录恢复并再次发布

原实例可以在备份封存后正常运行；替代实例用另一空宿主目录/监听端口，不覆盖原 DATA_DIR。目标只允许稳定锁文件，其他任何文件都会拒绝。恢复开始写入前创建并同步 `.restore-incomplete`，完整复制/校验/同步成功才删除；失败保留标记和诊断，既有 serve 遇标记拒绝。不要为绕过错误手动删标记，应另建空目录重新恢复。

授权容器环境的一次性命令（同一已固定镜像）：

```sh
docker run --rm --user 1000:1000 --read-only \
  --entrypoint /app/yuanfa-backup \
  -v /absolute/sealed-backup:/backup:ro \
  -v /absolute/empty-target:/restore \
  "$YUANFA_IMAGE" restore --backup /backup --data /restore
```

随后将替代服务挂到恢复目录，使用独立网络 origin/端口和同版运行模板。核对所有产品、草稿专属图、未关联素材、候选、active 操作、旧成功 hash 与当前页面；经真实后台编辑、构建、审阅和确认再发布一次。验证不依赖旧实例目录；可将旧目录暂时移入同一私有控制父目录防止误读，锁 inode 随目录保留。

本地验证入口仅用于隔离数据。`PROJECT_ROOT` 为仓库绝对路径，`CHECK_OUTPUT` 为私有输出目录；`SMOKE_NUMBER` 按脚本要求选取，并避免与运行实例端口冲突：

```sh
"$GO_BIN" test ./backend/internal/backup/... -count=1
node tests/smoke/backup.mjs --go "$GO_BIN" --root "$PROJECT_ROOT" --task-number "$SMOKE_NUMBER" \
  --binary .work/bin/yuanfa-backup --server .work/bin/yuanfa --out "$CHECK_OUTPUT/backup"
```

烟测创建独立源实例和恢复实例，执行真实TTY初始化、后台HTTP构建与确认、完整离线备份、空目录恢复、重启和再次发布。额外 tagged CLI 仅用于私有 `backup-locked`、`backup-before-seal`、`restore-locked` 屏障，生产不读取控制配置；分别在 SQLite 导出前、完整校验后和目标恢复写入前检查排他锁。屏障期间 serve/migrate/init-admin/import-seed/create/restore 应退出73且文件不变；烟测还检查缺图、校验/schema失败、非空恢复和失败复服。真实容器、1Panel、TLS/SSE和客户导购仍须在相应环境独立验证。

## 正式切换和回退

完成域名/TLS、镜像、备份及恢复准备后，在维护窗口备份旧站、面板设置、镜像和数据，按 1Panel 设置唯一域名、TLS和服务名代理，实测后台、发布、导购和MCP。停止旧静态部署向同一公开目录写入；代码升级走镜像，内容发布走后台。

保留旧站可恢复副本和前一应用镜像 digest。数据回退使用经验证的独立恢复目录，不将新 schema 数据强行交给旧 binary。正式域名为唯一权威；若仍有 Cloudflare Pages 入口，应配置跳转或停止将其作为正式入口，不宣称自动同步。生产切换前须验证实际部署链路和异地恢复；本地烟测不能替代这些检查。
