# 元发工具官网与内容管理平台

基于 Astro、React 和 Go 的工具产品官网，包含产品目录、管理后台、发布与回退、WorkBuddy MCP 接入及辅助选购功能。

## 功能

- 产品目录：型号搜索、分类与场景筛选、产品详情和图片展示。
- 管理后台：产品编辑、图片管理、选型资料审核和购买渠道维护。
- 发布流程：保存草稿、生成候选版本、预览差异、确认发布和历史回退。
- 辅助选购：按用途和已有条件推荐工具，核对明确条件并展示商品限制。
- WorkBuddy：通过私有 MCP 端点维护草稿，发布仍由管理员在浏览器中确认。
- 运维：SQLite 与媒体数据备份、完整性校验及独立恢复。

管理后台入口为 `/admin/`，登录页为 `/admin/login/`；后台页面和 API 均由 Go 服务提供。

## 环境要求

- Node.js **22.23.4**、npm **10.9.9**。
- Go **1.27.3**。
- 生产容器使用 Docker Compose，镜像版本与摘要固定在 `Dockerfile` 和 `deploy/image-lock.json`。

## 构建静态站点

静态构建使用明确的目录快照和媒体输入：

```sh
npm ci
npm run site:config
node scripts/build-candidate.mjs \
  --template "$PWD" \
  --snapshot "$PWD/seed/initial-catalog.json" \
  --media-root "$PWD/src/assets/products" \
  --work "$PWD/.work/static-build" \
  --out "$PWD/.work/static-site" \
  --site-url http://127.0.0.1:4330
```

生成的 `.work/static-site/` 可交给静态 Web 服务。构建工作目录及输出目录必须尚不存在，重复构建时使用新的目录。需要后台、发布、MCP 和导购接口时，启动下面的完整 Go 服务。

## 本地启动完整系统

```sh
npm ci
npm run site:config
npm run build:admin
mkdir -p .work/bin

go build -o .work/bin/yuanfa ./backend/cmd/yuanfa
go build -o .work/bin/yuanfa-backup ./backend/cmd/yuanfa-backup

export DATA_DIR="$PWD/.work/data"
export HTTP_ADDR="127.0.0.1:8080"
export SITE_URL="http://127.0.0.1:8080"
export TEMPLATE_DIR="$PWD"
export ADMIN_STATIC_DIR="$PWD/admin/dist"
export SITE_CONFIG_PATH="$PWD/src/generated/site-config.json"

.work/bin/yuanfa migrate
.work/bin/yuanfa init-admin --username admin
.work/bin/yuanfa import-seed --seed seed/initial-catalog.json --media-root src/assets/products
.work/bin/yuanfa serve
```

`init-admin` 在终端交互设置管理员密码；仓库没有默认管理员密码。启动后访问 `http://127.0.0.1:8080/admin/`。首次公开官网前，先在后台生成候选版本、预览并确认发布；发布前公开站点未就绪属于正常状态。

`DATA_DIR` 保存运行数据库、私有媒体和发布版本，应放在受保护的独立目录中。已有数据库不重复导入初始目录。初始导入、备份和恢复等独占操作需按运维说明停服执行。

## 运行配置

| 配置 | 用途 |
| --- | --- |
| `SITE_URL` | 对外访问的唯一网站 origin，生产环境使用实际 HTTPS 域名 |
| `DATA_DIR` | 运行数据目录 |
| `HTTP_ADDR` | Go 服务监听地址 |
| `TEMPLATE_DIR` | 发布构建使用的模板目录 |
| `ADMIN_STATIC_DIR` | 已构建的管理后台目录 |
| `SITE_CONFIG_PATH` | 生成的网站配置文件 |
| `PURCHASE_ALLOWED_HOSTS` | 经核实的商城精确主机名，多个主机用逗号分隔 |
| `TRUSTED_PROXIES` | 实际可信反向代理的精确地址 |
| `MODEL_PROVIDER`、`MODEL_BASE_URL`、`MODEL_MODEL` | 兼容 OpenAI 协议的模型服务配置 |
| `MODEL_API_KEY` | 通过受保护的运行环境提供的模型密钥 |

模型未配置时，官网目录和管理功能仍可使用；自然语言咨询需要有效的模型服务。商城链接需要在后台维护真实的型号对应地址和多规格提示。

管理员密码、模型 Key、MCP Token、运行数据库、备份、客户原始材料及开发过程记录不属于仓库交付内容。不要将实际凭据写入源码、Git 提交或公开截图。

## 构建与检查

```sh
npm test
npm run test:contracts
go test ./backend/...
```

其他检查脚本位于 `tests/`。浏览器检查需要可执行的 Chromium；实际服务联调使用独立数据目录和测试账号。

## 部署与接口说明

- [容器部署、反向代理、备份与恢复](docs/content-platform-operations.md)
- [数据与 API 契约](docs/content-platform-contracts.md)
- [WorkBuddy MCP 接入](docs/workbuddy-integration.md)
- OpenAPI 定义：`schemas/http-api.openapi.yaml`
- 容器编排：`deploy/compose.yml`
- 反向代理配置：`deploy/1panel-openresty.conf`

## 许可证

项目许可证见 [LICENSE](LICENSE)。第三方代码与依赖许可见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) 和 `licenses/`。
