# 2026-11-09 产品资料增量迁移

本目录交付的是离线清理后的迁移材料。`material-manifest.json` 不包含客户本机路径、工作簿或压缩包；`material-assets/` 仅含审核允许的 56 张 JPEG，文件名为原始字节 SHA-256。原始 61 张图片中另外 5 张只有隔离摘要和原因，不随包发布。所有图片无 EXIF/XMP 元数据。素材不放在 public；通过管理媒体 API 上传后，由成功目录快照授权公开。

## 数量与永久地址映射

- 原 40 款工具：只读保留，不发送 PUT，不改变任何文字、图片、商城链接或导购事实。5 款争议字段仍保持当前值。
- 新资料 28 项：25 次新增 + 3 次沿用原记录。最终列出 68 明细（50 工具 / 5 配件 / 13 枪钉），保留 67 张原图并关联 56 张新图。
- 4 个旧概括记录在全部成员已存在后执行浏览器业务删除，后台普通列表和公开目录均剩 68 条明细。后端仍保留已删除记录的历史和永久 slug 所属关系；旧地址作为分组入口，不计入列表、分类计数、导购目录或 sitemap。
- N 系列按照片中的 N9→N21 排序，封面为源“型号图7”的 N9；J4 的 422J 中标图排在中标 419J 之后；其余按主图、材质分组和规格次序排列。J10 完整保留 14 张。

| 表格行 | 明细 | 永久 slug | 动作 |
| --- | --- | --- | --- |
| 2–41 | 原 40 款工具 | 原 slug | 不写入 |
| 42–46 | YF1206 / YF1208 / YF1210 / YF1206Q / YF1210HL | yf1206 / yf1208 / yf1210 / yf1206q / yf1210hl | 新增工具 |
| 47–49 | F30锂电 / 422J锂电 / 1022J锂电 | f30-lithium / 422j-lithium / 1022j-lithium | 新增工具 |
| 50–51 | YF32 / YF9000 | yf32 / yf9000 | 新增工具 |
| 52–56 | 快速接头 / 蜘蛛腿工作台 / PU气管 / 通用修理包 / 原装撞针 | quick-coupler / spider-workbench / pu-air-hose / repair-kit / original-driver-blade | 新增配件，kind=entry |
| 57–59 | 特种瓦斯钉 / 高碳钢N系列码钉 / 卷钉 | nail-special-gas / nail-high-carbon-n / nail-coil | 新增耗材，无图 |
| 60–61 | K4系列特种码钉 / N系列码钉 | nail-special-k4 / nail-n | 新增耗材 |
| 62 | F系列直钉 | nail-brad | 复用 p_nail-brad |
| 63–65 | J4系列码钉 / J10系列码钉 / 10F系列码钉 | nail-j4 / nail-j10 / nail-10f | 新增耗材 |
| 66 | 蚊钉 | nail-pin | 复用 p_nail-pin |
| 67 | K系列码钉 | nail-k | 新增耗材 |
| 68 | 钢排钉 | nail-steel | 复用 p_nail-steel |
| 69 | FST无头钢排钉 | nail-fst | 新增耗材，仅主图 |

| 业务删除的原 ID / slug | 保留的历史分组入口 |
| --- | --- |
| p_nail-staple / nail-staple | 7 类码钉明细 |
| p_lithium / lithium | 3 款锂电工具 |
| p_air-screwdriver / air-screwdriver | 5 款风批 |
| p_parts / parts | 5 项配件 |

每条原材料完整分行应用到 applications/features/notes；来源为空仍为空。E 列不推断兼容产品关联。新工具缺图时使用中性“暂无实物图”。所有新增 guide_profile 安全条件保持未知，不写人工审核标记。

## 调用管理 API

需先部署支持管理员新增上架产品、20 张图片和缺图占位的新版后端。不要对已有库运行 import-seed。运行前完成数据备份；此脚本不会创建数据库备份或执行部署。

使用已登录管理员会话，将完整 Cookie 请求头放入进程环境变量 `MATERIAL_ADMIN_COOKIE`，不要写到仓库、命令参数或日志。程序读取 `/api/admin/session` 获取 CSRF，写请求设置精确 Origin、CSRF、Idempotency-Key，PUT 另带最新强 If-Match。

```powershell
# 默认只读：列出待新增/复用/业务删除及冲突，不上传图片也不保存。
node scripts/migrate-materials.mjs --base-url http://127.0.0.1:18081

# 管理员核对 dry-run 结果后执行。
node scripts/migrate-materials.mjs --base-url http://127.0.0.1:18081 --apply
```

该接口也可以作为模块导入：`browserAdminAPI(origin, cookie)` 配合 `runMigration({manifest, api, apply, readAsset})`。`readAsset` 返回 manifest 同目录下 `asset.file` 的原始 Buffer。

新增直接 POST listed 完整资料；相同请求键恢复网络中断后原创建结果，不重新占用 slug。已存在的 slug 不会被强行接管。现有记录先按审核基线比较所有可编辑字段，任一用户修改导致整条跳过；上传后再读一次，并以该版本 PUT。自动更新的 product_text 摘要不构成用户内容冲突，完整最新 guide_profile 和商城字段仍原样保留。新建后被修改或下架的条目，重跑不会覆盖或重新上架。媒体先分页读取并按 SHA 查重，媒体 API 再次校验/去重。任何 409/412 冲突保留当前内容，不盲目重试；退出码 2 表示存在需检查的冲突。其他网络/服务错误会停止，使用原脚本重跑即可继续。无持久凭据或批量清空。

仅 manifest.hubs 映射的 4 个原概括记录可 DELETE。删除前重新读取各成员状态、核对原记录所有基线字段和 slug，再次读取原记录，用最新 If-Match 和确定性 Idempotency-Key 调用 DELETE。用户修改过的概括记录保持原样并报告冲突。重跑遇到这 4 个原 ID 的 GET/DELETE 404 视为已完成；其他条目的 404 仍报告冲突。若此前执行过旧版 unlist 迁移，visibility 已偏离基线，脚本会保留并报告，不擅自删除。

已发布原条目由 previously_published_routes 提供分组路径；对于从未发布即业务删除的初始导入，src/data/material-families.ts 固定映射仅在所有成员已上市、且快照无同名产品/历史/系列路由占用时保留该分组路径。不创建新的可编辑概括产品，不改派已知 slug 所属关系。

返回的 saved/created/updated 只表示管理接口保存成功。负责人还需检查 `/api/admin/site-updates`，等待本批版本全部成功，并核对当前公开目录 68 明细；不要将脚本退出成功当作官网发布成功。发生用户编辑冲突时保持其当前值，最终数量可能暂未达到 68，报告会列出被跳过的条目。

## 复现清理包与离线构建

```powershell
node scripts/prepare-materials.mjs --source <private-material-review-directory> --out scripts
node scripts/material-preview.mjs --snapshot <reviewed-live-catalog.json> --out .work/material-preview-fresh
node --experimental-strip-types --test tests/material-migration.test.mjs tests/product-display.test.mjs
$env:MATERIAL_SITE_DIR='.work/material-preview-fresh/site'
node --test tests/product-display-browser.test.mjs
```

清理脚本交叉核对 workbook/comparison 的 68 行，依据固定允许映射复制 56 张图片并验证原始摘要，禁止采用暂缓图片。JSON 和脚本输出使用 LF；图片逐字节复制，不经文本换行转换。既有 seed/provenance/source archive 均不修改。已安装原 lockfile 依赖即可构建；无需重新导出 seed。

离线 preview 只创建独立快照和隔离静态构建，不调用 API；先验证基线并断言原 40 条逐字段未变。预览 ID 仅用于测试，绝不能将 preview snapshot 当作 live 数据导入。正常生产构建继续由后台成功快照驱动。浏览器测试使用临时本机静态测试端口并在结束时自动关闭，不连接业务实例。

## 改动文件对应

- 展示与缺图：src/components/{Header,ProductDetail,ProductImage,ProductPlaceholder}.astro；src/pages/index.astro；src/pages/products/{index,[slug]}.astro。
- 同源统计、搜索、分组与 sitemap：src/data/{catalog,material-families}.ts；src/pages/sitemap.xml.ts。
- 导购入口：src/components/guide/{chat-widget.tsx,guide.css}。
- 迁移/复现：scripts/{migrate-materials,prepare-materials,material-preview}.mjs；scripts/material-manifest.json；scripts/material-assets/；本文档。
- 验证：tests/material-migration.test.mjs；tests/product-display.test.mjs；tests/product-display-browser.test.mjs。
