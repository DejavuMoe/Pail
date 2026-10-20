# Pail 验证状态

本文记录每个功能现在被验证到什么程度，不是工作日志。每一项分四层记录，彼此不能替代：

- **API**：`internal/app`（或 `internal/storage` 等）里有对应的导出函数。
- **界面**：`cmd/pail-desktop` 里有入口（命令、菜单、对话框或工具页）。界面按钮存在不代表它对着真实服务端点过。
- **协议测试**：针对 `internal/s3test`（内存 S3，经真实 SDK 驱动）或 `httptest` 的命名测试。
- **真实端到端**：针对真实服务端的测试，分 MinIO（`integration/app_*_test.go`）与 VersityGW（`integration/ext_*_test.go`，经 Cloudflare，只写唯一前缀并清理）。

标记：✅ 有证据，⚠️ 部分覆盖，❌ 没有，n/a 不适用。与 CS Browser Pro 的逐项对照见 [feature-matrix.md](feature-matrix.md)。

## 验证环境

| 环境 | 做了什么 |
| --- | --- |
| Windows（原生） | `gofmt`、`go vet ./...`、`go test ./...`、`go build ./...`；原生窗口亮、暗、中文截图经人工审阅；解压后的发布包 exe 冒烟通过 |
| Linux（WSL，含 WSLg） | `go vet ./...`、`go test -race ./...`（全部包）；WSLg 原生窗口冒烟；Linux tar.gz 在 WSL 解压后 `--version` 与冒烟通过；中文经 fontconfig 回退到 Noto Sans CJK SC 正常显示 |
| MinIO | `scripts/test-integration.ps1`：固定版本 RELEASE.2025-04-22（本地从源码构建）与最新上游 RELEASE.2025-10-15（从源码构建），存储层与应用层套件全部通过 |
| VersityGW | 自托管，前面是 Cloudflare，path-style，桶 `test`，另有 CDN 域名。外部套件通过，每次运行结束后前缀为空、桶内原有对象不变、日志中无密钥 |
| macOS / arm64 | v0.1.0 发布提交 `3125867` 的 GitHub Actions CI 通过：六个目标（Windows、Linux、macOS 的 amd64 与 arm64）原生 `go vet`、`go test` 与版本冒烟，另有 Race、MinIO、govulncheck 与 CodeQL。人工 GUI 验收未完成，`.app` 没有开发者签名、未公证 |
| GitHub Release | v0.1.0 于 2026-10-20 发布，六个安装包与 `SHA256SUMS` 由 Release 工作流打包、校验后发布 |
| 网站 | https://pail.download 由 Woodpecker 部署到 netcup-vps500，2026-10-20 实测：`/`、`/zh/`、`/docs/` 返回 200 且显示 v0.1.0，缺失路径返回 404，HTML `no-cache`、带指纹的资源长期缓存、启用 HSTS |
| AWS | 没有测试 |

更名为 Pail 之后（2026-10-20）重跑了：Windows `gofmt`/`go vet`/`go test`、Linux `go vet` 与 `go test -race`（全部包）、六个目标的发布构建与包内容检查、MinIO 固定版本套件、`--demo` 冒烟与 README 截图重新生成。VersityGW 外部套件和 MinIO 最新上游套件在更名后尚未重跑，上表这两行的结果来自更名前的同一份功能代码。

VersityGW 已确认的行为：PutObject 与 CompleteMultipartUpload 遵守 `If-None-Match` / `If-Match`，CopyObject 忽略 `If-None-Match`；五种 checksum 与对象标签可用；读取对象 ACL 返回 NotImplemented（应用报 Unsupported）；存储类别与 SSE 被静默忽略（应用把这些项报为 failed）；未开启版本控制；Cloudflare 会解压 gzip 对象，HEAD 与 GET 的大小不一致（应用拒绝发布这类下载）。

## 功能

### 账户与凭据

| 功能 | API | 界面 | 协议测试 | 真实端到端 | 备注 |
| --- | --- | --- | --- | --- | --- |
| 账户增删改、测试连接（静态密钥、profile、默认凭据、匿名、AssumeRole） | ✅ | ✅ 账户对话框 | ✅ AssumeRole 只在 `httptest` STS 上验证 | ⚠️ 每个服务端一个账户 | 测试连接单次尝试，15 秒上限，连接被拒约 0.03 秒内报 Network |
| 机密存入系统钥匙串 | ✅ | ✅ | ✅ 用内存 Keyring 验证配置文件不含机密 | ❌ | 系统钥匙串由第三方库提供 |
| 旧名 openS3 数据迁移（配置目录、钥匙串服务、保险库格式、`opens3-*` metadata、临时文件） | ✅ | n/a 启动时自动 | ✅ `legacy_test.go` 等：改名失败转复制、复制失败沿用旧目录、钥匙串写入被拒与回读不一致、旧格式保险库解锁后升级 | ❌ 未在真实系统钥匙串上验证 | 只在新位置复核成功后才删除旧数据 |
| 加密保险库（Argon2id + XChaCha20-Poly1305） | ✅ | ✅ 设置 → 安全、解锁对话框、状态栏锁标识 | ✅ 迁移与回滚、自动锁定、并发写入合并、参数范围检查 | n/a | 本地功能，无服务端参与；加密代码经人工复核 |
| 每账户 CDN 域名 | ✅ | ✅ 账户字段、检查器、命令、上传后提示 | ✅ | ✅ VersityGW：运行中上传的对象经 CDN 返回 200 且内容一致 | 测试桶本身非公开时 CDN 返回 403，会被记录为"非公开"而不是失败 |

### 浏览与预览

| 功能 | API | 界面 | 协议测试 | 真实端到端 | 备注 |
| --- | --- | --- | --- | --- | --- |
| 桶列表（含 region）、文件夹导航、自动分页、搜索、排序、多选 | ✅ | ✅ | ✅ | ✅ MinIO（含 1005 个对象跨 1000 边界）、VersityGW | 对象表 5,000 行虚拟化 |
| 平铺（flat）视图 | ✅ | ✅ | ✅ | ⚠️ 与列表共用路径 | |
| 预览（JSON、CSV / TSV、XML 与其他文本、图片、十六进制；文本上限 1 MiB） | ✅ | ✅ | ✅ | ❌ | 大目录的 "at least" 计数只有单元测试 |
| 新建文件夹、复制 key / URI / URL | ✅ | ✅ | ✅ | ✅ | key 含 `+`、`?`、空格、Unicode 的场景在两个服务端上验证 |
| 命令面板、右键菜单、快捷键 | n/a | ✅ | ✅ 命令表与对话框可达性 | n/a | 右键弹出层只在 headless 测试里验证，没有目视审阅 |
| 浮层统一外观（未发布）：`overlayPanel`、`listRow`、键帽、命令面板与下拉选项、存储桶设置对话框、Toast | n/a | ⚠️ headless 测试（`overlay_test.go`、`chrome_test.go`、`theme_test.go`）：读取渲染像素确认三种面板亮暗两种边缘（亮色阴影、暗色 1px 描边、Toast 无描边）、命令面板与下拉选项的高亮（键盘与鼠标同为 `SurfaceHover`）和当前值对勾（`Accent`）、禁用行不响应悬停、键帽逐键拆分；`theme.go` 之外没有颜色字面量；新 token 的对比度下限；960×600 下全部对话框按钮可达；亮、暗、960×600、1440×900 的快照由人工逐张目视审阅（命令面板、下拉选项、对话框、存储桶设置、Toast） | n/a | n/a | 只在 headless 渲染里看过，没有在原生窗口里目视；暗色阴影与描边的取舍未在真实显示器上比较。Tooltip 只套用了主题的反色颜色，圆角（5）与阴影仍是 mygo v0.5.5 内置的，库里没有改写钩子。右键菜单与 `⋯` 菜单仍是系统原生菜单，尚未套用本规范 |
| 检查器标头标签与缩放布局（未发布修复） | n/a | ⚠️ headless 测试（`layout_test.go`）：检查器 320 与 280 宽下六个标头标签的布局宽度不为 0，且与宽面板中一致（修复前为 0，测试会失败）；搜索框宽度与对象表列由当前帧尺寸算出，不再读上一帧的 `Bounds()`；工具栏、路径栏、表格列的断点带 ±12 回滞；取消 1100 处的面板自动缩窄，960×600 下 `chrome_test.go`、`ux_audit_test.go`、i18n 审计仍通过；分隔条悬停 `BorderStrong`、拖动 `TextSubtle` | n/a | n/a | 只做了 headless 快照的目视确认（标签显示），没有在原生窗口里拖动缩放对比。分隔条拖动色按"最近 200 ms 内移动过"判断，因为 mygo 不报告按键状态；没有目视审阅 |

### 传输

| 功能 | API | 界面 | 协议测试 | 真实端到端 | 备注 |
| --- | --- | --- | --- | --- | --- |
| 文件、文件夹、整桶上传与下载，拖放 | ✅ | ✅ | ✅ | ✅ MinIO、VersityGW | 含 11 MiB 分片往返与 SHA-256 校验 |
| 三类队列并发、取消、重试 | ✅ | ✅ 传输面板（事件驱动） | ✅ | ⚠️ 队列跑了全部端到端传输，上限数值没有断言 | |
| 五种冲突策略 | ✅ | ✅ | ✅ | ✅ MinIO、VersityGW | |
| create-only 条件写入与降级 | ✅ | ✅ | ✅ 412 不会被当成"不支持"，501 会降级 | ✅ 并发写竞态（MinIO、VersityGW） | 降级到 HEAD 时存在竞态，只写权限账户在 rename 策略下无法识别重放产生的 412 |
| 分片上传与下载、四种 checksum 模式 | ✅ | ✅ 设置 | ✅ | ✅ 含 VersityGW 的服务端 checksum | 自定义 endpoint 默认 `WhenRequired` |
| 下载先写临时文件、校验后原子发布 | ✅ | ✅ | ✅ | ✅ | 不依赖硬链接；大小不符的下载被拒绝 |
| 未完成的分片上传：列出与中止 | ✅ | ✅ | ✅ | ✅ MinIO、VersityGW | MinIO 对"文件夹"前缀返回空，应用改为列整桶后本地过滤 |

### 对象属性与批量操作

| 功能 | API | 界面 | 协议测试 | 真实端到端 | 备注 |
| --- | --- | --- | --- | --- | --- |
| 复制、移动、重命名（同桶、跨桶、版本化源） | ✅ | ✅ 含拖到文件夹移动、F2 | ✅ | ✅ MinIO、VersityGW | 移动先复制并校验再删源；仅限同一账户 |
| 批量删除，逐项结果 | ✅ | ✅ 带数量估算 | ✅ unknown 不重发 | ✅ 1105 个 key（MinIO） | |
| headers、metadata、tags 编辑 | ✅ | ✅ 单对象直接保存，多选弹范围确认 | ✅ 未改动的属性全部保留 | ✅ MinIO、VersityGW（tags 可用） | |
| ACL 查看与编辑 | ✅ | ✅ 公开 ACL 需确认 | ✅ | ⚠️ VersityGW 读取 ACL 返回 NotImplemented，应用报 Unsupported | 读不到 ACL 时拒绝改写 |
| 存储类别与加密改写（对象、整桶） | ✅ | ✅ | ✅ 改写后 HEAD 复核 | ⚠️ MinIO 存储类别支持有限、SSE-S3 需 KMS；VersityGW 静默忽略并被正确报为 failed | SSE-S3、KMS、SSE-C 成功路径从未在真实服务端验证 |

### 版本

| 功能 | API | 界面 | 协议测试 | 真实端到端 | 备注 |
| --- | --- | --- | --- | --- | --- |
| 版本列表、"显示已删除" | ✅ | ✅ | ✅ | ✅ MinIO / ❌ VersityGW（未开启版本控制，场景跳过） | |
| 恢复版本、恢复已删除对象 | ✅ | ✅ | ✅ 并发上传优先；历史不删除 | ✅ MinIO | 版本化桶内移动用删除标记加当前版本复核，不会让旧版本复活 |
| 多版本下载、永久删除 | ✅ | ✅ | ✅ | ⚠️ MinIO：下载含在往返测试里，多选未断言；删除版本与标记 ✅ | |

### 桶管理与设置

| 功能 | API | 界面 | 协议测试 | 真实端到端 | 备注 |
| --- | --- | --- | --- | --- | --- |
| 建桶（region、对象锁）、删桶、清空桶 | ✅ | ✅ 删桶需输入桶名 | ✅ | ✅ MinIO / ❌ VersityGW（按规则不建桶、不删桶） | |
| 14 项桶配置的读、写、删 | ✅ | ✅ 简单项表单，其余 JSON | ✅ 403 不当作"未配置" | ⚠️ MinIO：版本控制、策略、生命周期、标签 ✅，CORS 不支持（报 Unsupported）；VersityGW 只读 | |
| 整桶复制、移动、下载 | ✅ | ✅ 桶右键菜单 | ✅ | ⚠️ 经对象级用例间接覆盖，界面入口本身没有端到端 | |

### 同步

| 功能 | API | 界面 | 协议测试 | 真实端到端 | 备注 |
| --- | --- | --- | --- | --- | --- |
| 同步作业增删改、预览、执行（上传 / 下载 / 镜像，三种比较模式，排除规则） | ✅ | ✅ 预览筛选、执行确认（删除项用危险色） | ✅ 执行前逐项复核前提 | ✅ MinIO、VersityGW | `size_time` 会漏检同尺寸改动；checksum 模式下，其他工具写入、缺少 `pail-sha256` 的对象会被反复判为需要传输 |
| 本地删除进系统回收站 | ✅ | ✅ | ✅ Windows 与 Linux 回收站各有测试 | ⚠️ 只验证了预览，没有真的移入回收站 | Windows 拒绝长路径与网络盘 |

### 上传规则与压缩

| 功能 | API | 界面 | 协议测试 | 真实端到端 | 备注 |
| --- | --- | --- | --- | --- | --- |
| 规则（headers、存储类别、SSE、压缩），草稿测试条 | ✅ | ✅ | ✅ | ✅ MinIO、VersityGW | |
| GZIP、ZIP、AES-256 ZIP 与压缩级别；下载时解码 | ✅ | ✅ | ✅ 编解码级别验证了各级别，并与 7z 互通 | ⚠️ 各格式往返 ✅，级别未在端到端变化 | 经 Cloudflare 的 gzip 对象下载会被拒绝（代理解压导致大小不符），不会写出错误文件 |
| MIME 类型表 | ✅ | ✅ | ✅ | ⚠️ 经上传间接覆盖 | |

### 预签名 URL 与 CloudFront

| 功能 | API | 界面 | 协议测试 | 真实端到端 | 备注 |
| --- | --- | --- | --- | --- | --- |
| 预签名 URL（GET / PUT、有效期、指定版本、多对象） | ✅ | ✅ | ✅ | ✅ MinIO、VersityGW | |
| CloudFront：distribution 列表、多路径 / 整桶失效、状态 | ✅ | ✅ 仅 AWS 账户 | ✅ `httptest` | ❌ | 从未对真实 CloudFront 或 AWS 运行 |

### 安全、日志与本地化

| 功能 | API | 界面 | 协议测试 | 真实端到端 | 备注 |
| --- | --- | --- | --- | --- | --- |
| 脱敏文件日志（默认关闭，5 MiB 轮转保留 3 个） | ✅ | ✅ 设置开关、打开日志文件夹 | ✅ 10 种机密形态的脱敏表 | ✅ 外部套件运行后扫描日志，不含密钥与 access key | 手动输入、未存为引用的 ZIP 密码和 SSE-C key 只能靠标签或字段名识别 |
| 中英文界面 | n/a | ✅ 设置 → 外观、`⋯` 菜单，无需重启 | ✅ 键与占位符一致、中文文案风格、伪语言在 960×600 下全部对话框可达 | n/a | `internal/app` 的任务标题和命令行未本地化 |
| 亮 / 暗主题、对比度 | n/a | ✅ | ✅ 对比度下限与状态双线索检查 | n/a | 对象表表头的列分隔线与字重无法按规范修改（mygo 限制） |

### 命令行与发布

| 功能 | API | 界面 | 协议测试 | 真实端到端 | 备注 |
| --- | --- | --- | --- | --- | --- |
| `pail` 命令行（accounts、buckets、ls、cp、mv、rm、mb、rb、presign、url、versions、restore、sync、vault status） | ✅ | n/a | ✅ 进程内对 `internal/s3test` | ❌ 命令行外壳本身没有对真实服务端运行 | 它调用的应用层在真实服务端验证过 |
| 发布包（Windows / Linux / macOS，amd64 与 arm64）与 SHA256SUMS | n/a | n/a | ✅ 打包工具保留 Unix 权限位（tar.gz 与 zip 各有测试） | ⚠️ Windows 与 Linux amd64 解压后运行过；v0.1.0 六平台 CI 原生测试与版本冒烟通过，不等于人工 GUI 验收 | v0.1.0 已在 GitHub Releases 发布 |

## 已知限制与未验证

- 真实 AWS：S3、STS AssumeRole、CloudFront 都没有测试（AssumeRole 与 CloudFront 只在 `httptest` 上验证）。
- macOS 与 ARM64 的人工 GUI 验收未完成；原生 CI 结果单独记录。macOS `.app` 没有开发者签名、未公证。
- VersityGW 上的版本控制：服务端未开启，相关场景被跳过。
- 经 Cloudflare 的 gzip 对象：代理会解压，下载被拒绝（不会写出错误文件）。
- SSE-S3、SSE-KMS、SSE-C 成功路径：MinIO 没有配置 KMS，VersityGW 忽略加密；https 端点上的 SSE-C 成功路径从未运行。
- 超过 5 GiB 的对象经原地改写（存储类别、加密、属性）的分片复制路径：未验证。
- 本地镜像删除移入真实回收站：只验证了预览。
- 右键弹出菜单、macOS 系统菜单栏：没有目视审阅。
- 窗口拖动缩放时的布局抖动：应用侧的晚一帧回读和断点翻转已修，只有 headless 测试；mygo 侧（Windows 每个 `WM_SIZE` 同步重绘、系统按钮不与帧原子合成）未处理，也没有在原生窗口里对比修复前后。
- `internal/app` 产生的任务标题（传输面板）和命令行输出未本地化，仍为英文。
- MinIO 套件没有加 `-race` 运行；`-race` 只跑了单元与协议测试。
- 只写权限账户在 rename 策略下无法识别重放产生的 412；大小写不同的同名 key 的编号不会跨扫描记忆。
- 命令行外壳没有对真实服务端运行。
