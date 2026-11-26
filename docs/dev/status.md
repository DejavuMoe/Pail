# Pail 验证状态

本文记录每个功能现在被验证到什么程度，不是工作日志。每一项分四层记录，彼此不能替代：

- **API**：`internal/app`（或 `internal/storage` 等）里有对应的导出函数。
- **界面**：`cmd/pail-desktop` 里有入口（命令、菜单、对话框或工具页）。界面按钮存在不代表它对着真实服务端点过。
- **协议测试**：针对 `internal/s3test`（内存 S3，经真实 SDK 驱动）或 `httptest` 的命名测试。
- **真实端到端**：针对真实服务端或真机的测试，分三类：MinIO（`integration/app_*_test.go`）、VersityGW（`integration/ext_*_test.go`，经 Cloudflare，只写唯一前缀并清理），以及所有者手动完成的真机与真实云服务验证（见“验证环境”表中的 macOS、Amazon S3、Cloudflare R2、Wasabi）。

标记：✅ 有证据，⚠️ 部分覆盖，❌ 没有，n/a 不适用。

## 验证环境

| 环境 | 做了什么 |
| --- | --- |
| Windows（原生） | `gofmt`、`go vet ./...`、`go test ./...`、`go build ./...`；原生窗口亮、暗、中文截图经人工审阅；解压后的发布包 exe 冒烟通过 |
| Linux（WSL，含 WSLg） | `go vet ./...`、`go test -race ./...`（全部包）；WSLg 原生窗口冒烟；Linux tar.gz 在 WSL 解压后 `--version` 与冒烟通过；中文经 fontconfig 回退到 Noto Sans CJK SC 正常显示 |
| MinIO | `scripts/test-integration.ps1`：固定版本 RELEASE.2025-04-22（本地从源码构建）与最新上游 RELEASE.2025-10-15（从源码构建），存储层与应用层套件全部通过 |
| VersityGW | 自托管，前面是 Cloudflare，path-style，桶 `test`，另有 CDN 域名。外部套件通过，每次运行结束后前缀为空、桶内原有对象不变、日志中无密钥 |
| macOS（arm64 与 amd64，真机） | 所有者在 Apple silicon（arm64）与 Intel（amd64）两台真实 Mac 上手动运行应用，每个版本都运行。下载得到的应用首次启动时，需要先执行 `xattr -dr com.apple.quarantine` 清除隔离属性，macOS 才会打开。`.app` 没有开发者签名、未公证。CI 另有六个目标（Windows、Linux、macOS 的 amd64 与 arm64）的原生 `go vet`、`go test` 与版本冒烟 |
| GitHub Release | 六个安装包与 `SHA256SUMS` 由 Release 工作流打包、校验后发布；下载公开产物再次验包通过，全部不含 README |
| 网站 | https://pail.download 由 Woodpecker 部署；发布后检查过中英文页面、下载链接、404、robots、sitemap 与分享元信息 |
| Amazon S3、Cloudflare R2、Wasabi | 所有者手动测试，可用（2026-10-20 确认）。这是手动的真实端到端验证，没有对应的自动化套件，具体覆盖的功能没有逐项记录 |
| AWS 的 STS AssumeRole 与 CloudFront | 没有测试 |

VersityGW 已确认的行为：PutObject 与 CompleteMultipartUpload 遵守 `If-None-Match` / `If-Match`，CopyObject 忽略 `If-None-Match`；五种 checksum 与对象标签可用；读取对象 ACL 返回 NotImplemented（应用报 Unsupported）；存储类别与 SSE 被静默忽略（应用把这些项报为 failed）；未开启版本控制；Cloudflare 会解压 gzip 对象，HEAD 与 GET 的大小不一致（应用拒绝发布这类下载）。

## 功能

### 内存诊断与回收

| 功能 | API | 界面 | 协议测试 | 真实端到端 | 备注 |
| --- | --- | --- | --- | --- | --- |
| 完成任务淘汰后的回收 | ✅ 保留数量不变 | n/a | ✅ `TestRetentionReleasesEvictedTask`：旧实现强制 GC 后仍可达，清空被淘汰槽位后可回收；原有淘汰与清空测试通过 | n/a | 修复底层切片残留引用 |
| 对象名称排序分配 | n/a | ✅ 排序与过滤入口不变 | ✅ `TestRebuildRowsPreservesNamesAndStableTies`：目录优先、稳定顺序、Unicode、过滤和索引；`FuzzCompareNames` 对照原比较规则，Windows 20 秒约 10.8 万次通过 | ⚠️ Windows 本地服务一万对象浏览通过 | 一万条随机混合大小写 ASCII 名称的重复排序，基准分配约 12.2 MB → 1.48 MB，约 20 ms → 7 ms；不代表常驻内存等比例下降 |

### 账户与凭据

| 功能 | API | 界面 | 协议测试 | 真实端到端 | 备注 |
| --- | --- | --- | --- | --- | --- |
| 账户增删改、测试连接（静态密钥、profile、默认凭据、匿名、AssumeRole） | ✅ | ✅ 账户对话框 | ✅ AssumeRole 只在 `httptest` STS 上验证 | ⚠️ 每个服务端一个账户 | 测试连接单次尝试，15 秒上限，连接被拒约 0.03 秒内报 Network |
| 本地 AWS 配置文件（含 `aws login --profile`）：读取本地 profile 列表、沿用 profile 区域（新建存储桶与公开链接同样使用）、刷新缓存会话并在配置文件变更时重建客户端 | ✅ `LocalAWS()`、`AccountRegion`、`ErrLoginRequired`、共享配置文件变更检测；AWS 上 `CreateBucket` 发往新桶所在区域 | ⚠️ headless 测试（`cmd/pail-desktop/aws_profile_test.go`）：账户对话框列出本机 profile 与认证类型/区域、选择"其他…"手动输入、切换凭证时清空未改动的默认区域、区域占位符按 `AWS_REGION` 与 `AWS_PROFILE` 显示、登录会话过期文案；新建存储桶对话框预填 `AccountRegion`（无 headless 测试）；`cmd/pail/aws_profile_test.go`：`account profiles` 与 `account add --profile` | ✅ `internal/storage/login_test.go`、`internal/app/aws_profile_test.go`：读取 `login_session` 缓存并签名请求、沿用 profile 区域、过期令牌经 DPoP `CreateOAuth2Token` 刷新并回写缓存、缺失缓存报 `ErrLoginRequired`、更新配置文件后重建客户端；`TestAWSProfileRegionForCreateBucketAndPublicURL` 用记录请求的 HTTP 客户端检查未填区域的 profile 账户：`PublicURL` 用 profile 区域，`CreateBucket` 的目标主机、签名区域与 `LocationConstraint`（留空、`us-east-1`、其他区域三种）；`TestLocalAWSEnvironment` 检查环境变量优先级 | ❌ 未在真实 AWS 账户上运行 `aws login --profile` 端到端验证 | 未在原生窗口实机操作 |
| 多实例账户与保险库写入 | ✅ 保存、删除、桶别名、凭据迁移、保险库重置共用账户事务；保险库读写经 `vault.lock`；两把锁限时 15 秒，超时返回 `ErrConfigLocked` | ✅ 原有保存入口；超时显示“另一个 Pail 窗口或命令正在保存设置”（`TestHumanErrorConfigBusy`） | n/a；`accounts_concurrent_test.go`、`n2_vault_multiprocess_test.go`、`lock_timeout_test.go` 在 Windows 与 Linux 上覆盖独立进程写入、旧凭据代次、存储切换冲突、改主密码与写入并发、同时持两把锁的路径和持锁进程卡住时的超时，Linux race 通过 | n/a | 只保证账户与保险库写入，不代表所有配置文件都有跨进程事务；轮询加锁不保证先到先得 |
| 设置写入与机密存储切换（v0.7.4） | ✅ `settings.json` 的写入都先重读磁盘，机密存储与内存不一致时返回 Conflict 且不写入；测试连接只在账户使用全局代理时读取代理密码 | ✅ 原有设置入口，错误走原有提示 | n/a；`internal/app/settingsconflict_test.go` | n/a | `settings.json` 被外部删除而内存里是保险库时，设置写入会一直返回 Conflict，重启后恢复 |
| 指定存储桶：密钥没有列桶权限或只能用某个文件夹时，账户填写 `bucket` 或 `bucket/folder/`，侧栏与命令行只显示这些并在该文件夹中打开 | ✅ `Account.Buckets`、`ParseAccountBuckets`；`Buckets()` 不发 ListBuckets，`TestAccount` 逐个列出指定的文件夹 | ✅ 账户对话框"存储桶"字段、测试被拒时的提示；侧栏列桶被拒时的"填写存储桶…"按钮；`--bucket` 参数、`account show` / `account test`、`buckets` 输出 | ✅ `internal/app/account_buckets_test.go`、`cmd/pail/named_buckets_test.go`、`cmd/pail-desktop/named_buckets_test.go`（headless）：内存 S3 只放行指定文件夹的列表与其下的对象请求，其余（含 ListBuckets 和打开存储桶时的版本控制读取）一律 AccessDenied；多个存储桶并行测试、按顺序报告第一个失败 | ⚠️ MinIO `TestAppNamedBuckets`：桶策略只允许匿名请求按 `s3:prefix` 列出 `dir/` 并读取其下对象，测试连接、列表、预览、下载文件夹通过，桶根与其他文件夹被拒；没有对 AWS 的受限 IAM 密钥运行 | 未在原生窗口实机操作。面包屑与"上一级"停在指定文件夹，整桶操作作用于该文件夹；同一桶只能指定一个文件夹 |
| 配置链接（`pail://account`）：分享账户设置、点击或粘贴后预填"添加账户" | ✅ `SetupLink`、`SetupLinkFrom`、`ParseSetupLink`（`internal/app/setuplink_test.go`）：往返、`SetupLinkFrom` 不带账户自己的密钥、代理、跳过 TLS 与别名，无法解析的值留空或取默认，非配置链接被拒绝 | ⚠️ headless 测试（`cmd/pail-desktop/setuplink_test.go`）：链接预填表单并立即标出缺少的密钥、没有密钥时"保存"不保存、补全后保存的账户与链接一致、带密钥的链接有提示、粘贴对话框拒绝非配置链接、分享对话框不带自己的密钥、为接收者填写的密钥进入链接且私有密钥不显示；没有账户时从欢迎页粘贴（剪贴板里的链接自动填入）、保存后出现第一个账户与它的存储桶；"添加账户"顶部的入口打开画在表单之上的粘贴对话框，非链接的剪贴板内容不填入，编辑账户时不显示入口；伪语言与中文文案 | n/a 不经过服务端 | ❌ 没有在真实桌面上点击过 `pail://` 链接：Windows 注册表、Linux `x-scheme-handler`、macOS `CFBundleURLTypes` 的登记以及已运行实例接收链接都未实机验证 | 只有发布版本（非 `dev`、非 `--demo`、默认配置目录）启动时登记协议；登记失败只写日志。Windows 的 zip 没有安装程序，首次启动前点击链接不会打开 Pail。Linux 的 `install.sh` 在 `pail.desktop` 里声明 `x-scheme-handler/pail` 并在有 `xdg-mime` 时设为默认（`tools/release` 的安装测试只检查条目内容；在 WSL 的 Debian 里用临时 HOME 手动运行 `install.sh` 后，`xdg-mime query default x-scheme-handler/pail` 返回 `pail.desktop`，没有在带桌面环境的 Linux 上点击过链接）。macOS 的 Info.plist 只在 `tools/release` 测试里检查了声明 |
| 机密存入系统钥匙串 | ✅ | ✅ | ✅ 用内存 Keyring 验证配置文件不含机密 | ❌ | 系统钥匙串由第三方库提供 |
| 加密保险库（Argon2id + XChaCha20-Poly1305） | ✅ | ✅ 设置 → 安全、解锁对话框、状态栏锁标识 | ✅ 迁移与回滚、自动锁定、并发写入合并（含独立进程）、另一进程改主密码后旧密钥返回 Conflict、参数范围检查 | n/a | 本地功能，无服务端参与；加密代码经人工复核 |
| 每账户 CDN 域名 | ✅ | ✅ 账户字段、检查器、命令、上传后提示 | ✅ | ✅ VersityGW：运行中上传的对象经 CDN 返回 200 且内容一致 | 测试桶本身非公开时 CDN 返回 403，会被记录为"非公开"而不是失败 |
| 桶别名：在侧边栏、路径栏、命令面板、检查器标题和"返回"按钮里显示自定的名字，真实名称不变 | ✅ `SetBucketAlias`、`BucketAlias`，保存在账户记录 `bucketAliases` 里；`SaveAccount` 保留已有别名；`DeleteBucket` 清除该桶的别名 | ✅ 桶右键菜单"设置别名…"与对话框；侧边栏筛选同时匹配别名与真实名称；删除、清空、复制等对话框与传输任务仍显示真实名称 | ✅ `internal/app/proxy_alias_test.go`：保存、清除、重复别名、长度按字符计、重启后保留、`List` 请求仍用真实桶名；`cmd/pail-desktop/bucket_mgmt_test.go`（headless）：对话框、侧边栏、路径栏、命令面板、中文文案 | n/a 只存在于本机配置，不发往服务端 | 未在原生窗口实机操作。命令行不显示别名 |
| 代理服务器（设置 › 网络）：S3、STS、CloudFront 的请求经 HTTP、HTTPS 或 SOCKS5 代理，可带用户名和密码；"跟随环境变量"时沿用 `HTTP_PROXY`、`HTTPS_PROXY`、`NO_PROXY` | ✅ `Settings.Proxy`（`ProxySettings`）、`SaveProxy`；密码存机密存储，随后端迁移，保险库重置时清除并报告；改动后丢弃已建的客户端，命令行读同一设置 | ✅ 设置 › 网络：协议选择框、主机、端口、用户名、密码与"应用"；每种问题在字段下说明，保存失败时保留字段；保险库重置对话框列出代理密码 | ✅ `internal/storage/proxy_test.go`：本地 HTTP 代理与自制 SOCKS5 服务器转发 S3 请求，两者的用户名密码（含特殊字符）通过与拒绝，本机地址与 `NO_PROXY` 直连，无效地址不会退回直连；`internal/app/proxy_alias_test.go`：校验表、密码只进机密存储、保存失败回滚、密码缺失与保险库迁移和重置、测试连接走代理并因错误密码失败；`cmd/pail-desktop/proxy_view_test.go`（headless）：表单、中文文案 | ❌ 没有对真实代理软件（如 Clash）运行过 | 只读环境变量，不读操作系统的系统代理设置。不支持需要证书或其他认证方式（如 NTLM）的代理 |

### 浏览与预览

| 功能 | API | 界面 | 协议测试 | 真实端到端 | 备注 |
| --- | --- | --- | --- | --- | --- |
| 桶列表（含 region）、文件夹导航、自动分页、搜索、排序、多选 | ✅ | ✅ | ✅ | ✅ MinIO（含 1005 个对象跨 1000 边界）、VersityGW | 对象表 5,000 行虚拟化 |
| 对象表拖动选择：复选框列刷选、空白处框选 | n/a | ⚠️ headless 测试（`selection_test.go`）：沿复选框列刷选、从空白处框选、`Ctrl/⌘` 加选、单击空白清除、`Esc` 恢复、拖出底部自动滚动（5,000 行）；矩形快照经人工目视 | n/a | n/a 本地交互 | 未在原生窗口实机操作；按住 `Ctrl/⌘` 的拖动由测试直接把事件交给表格。列表填满视野时没有空白处可起框，只能从复选框列刷选 |
| 对象表复选框列、三态全选表头、选择条 | n/a | ⚠️ headless 测试（`selection_test.go`、`motion_test.go`、`theme_test.go`）：复选框只切换该行，`Shift` 范围扩选，表头三态，`Esc` 清除，选中两项及以上出现选择条（960×600 下按钮都在条内），复选框填充对比度 ≥ 3:1；亮暗快照经人工目视 | n/a | n/a 本地交互 | 未在原生窗口实机操作。`Shift` 扩选的起点是上一次普通点击或 `Ctrl` 点击的行（mygo 不公开设置起点的接口）。960×600 选中行时表格出现横向滚动条，改动前已存在 |
| 文件夹大小：右键菜单、工具栏按钮、命令面板，检查器显示，按大小排序 | ✅ `FolderSize(ctx, loc, progress)` | ⚠️ headless 测试（`foldersize_test.go`）：触发计算、进度、按大小排序、写操作后结果作废、失败可重试、已算出的文件夹可再次计算 | ✅ `internal/app/foldersize_test.go` 对内存 S3：分页、URL 编码的 key、错误、取消 | n/a 没有对真实服务端运行 | 未在原生窗口实机操作。只统计当前对象，不含旧版本、删除标记和未完成的分段上传。超大前缀的耗时没有测量 |
| 托盘图标、关闭即隐藏、单实例、窗口隐藏时的系统通知 | n/a | ⚠️ headless 测试：托盘状态文字、托盘图案、窗口隐藏时 Toast 改发系统通知、托盘设置的保存（`hideTrayIcon`，缺省为显示） | n/a | ❌ 只构建并通过单元测试，没有在真实的 Windows、macOS、Linux 桌面上运行 | 托盘菜单、点击行为、关闭即隐藏、单实例、系统通知、macOS 菜单栏百分比、Linux AppIndicator 缺失时的回退都未实机验证；macOS 交叉编译不代表真机 |
| 异步结果与编辑草稿 | ✅ 原有保存、查询接口 | ⚠️ headless 测试：设置按操作顺序保存（`accounts_test.go`）；工具重入和对话框重开拒绝旧回调（`tools_test.go`、`bucket_mgmt_test.go`、`accounts_test.go`）；标签、MIME、同步任务保存保留后续输入（`edit_test.go`、`tools_test.go`）；托盘上传可用状态随导航更新（`foldersize_test.go`） | ✅ 标签和建桶经内存 S3；CloudFront 使用可控 API 夹具 | ❌ 这些边界尚未在修复后的原生窗口和真实云端验收 | 回归通过受控完成顺序触发，不依赖网络延迟概率 |
| 状态栏显示最近一次成功操作的用时：加载存储桶列表、打开文件夹、上传、下载、删除 | n/a | ⚠️ headless 测试（`optime_test.go`）：打开文件夹与加载存储桶后更新，上传与删除完成后保留其用时、之后自动的刷新不覆盖，切换账户清除，悬停说明与中文文案；`recentops_test.go`：失败和取消的操作不更新它 | n/a | n/a 本地计时 | 未在原生窗口实机操作。用时从发出请求算到最后一页返回，上传与删除按任务的首次开始到最后结束 |
| 最近操作列表：点击状态栏的用时打开，列出加载存储桶列表、打开文件夹、上传、下载、删除的结果（成功、失败、已取消）与用时；设置里可调条数（1 到 1000，默认 100）和是否在切换账户后保留 | ✅ `Settings.RecentOps`、`Settings.KeepRecentOps`：默认值、两端边界、越界被拒绝且不改动已保存的值、旧设置文件取默认值、重启后保留（`internal/app/settings_test.go`） | ⚠️ headless 测试（`recentops_test.go`）：各行的文字（计数、状态、失败原因、位置、账户名、用时与结束时刻）；点击用时打开、再点或 `Esc` 关闭、`Enter` 重新打开，最新的在最上；内存 S3 注入 403 的目录列表记为失败、注入延迟后被另一个文件夹取代的目录列表记为已取消、失败与已取消的上传、删除、下载经完成回调记录，且不更新状态栏；条数上限、调低后立即截断、"清空"；切换账户默认清空、开启后保留并写账户名、切走后才结束的操作在关闭时被丢弃；设置页的范围校验与开关保存；伪语言下没有未翻译文案，中文文案；亮暗、中英快照经人工目视 | n/a | n/a 只在内存里，没有写盘 | 未在原生窗口实机操作。任务的取消没有从传输面板的取消按钮走一遍，测试把已取消的任务直接交给完成回调。条数到 1000 时的滚动只在 headless 窗口里看过。命令行没有这个列表 |
| 平铺（flat）视图 | ✅ | ✅ | ✅ | ⚠️ 与列表共用路径 | |
| 预览（JSON、CSV / TSV、XML 与其他文本、图片、十六进制；文本上限 1 MiB） | ✅ | ✅ | ✅ | ❌ | 大目录的 "at least" 计数只有单元测试 |
| 图片预览缩放：捏合、Ctrl + 滚轮、放大 / 缩小按钮，放大后拖动或滚动平移，双击回到适配宽度 | n/a | ⚠️ headless 测试（`preview_zoom_test.go`）：捏合与按钮缩放（1 至 8 倍）、平移、Ctrl 加滚轮、换对象后回到适配；放大快照经人工目视 | n/a | n/a | 未在原生窗口用真实触控板或鼠标操作；双击还原没有自动化测试。捏合依赖 mygo 的 `InputZoom`：macOS、Linux Wayland、Windows 触控板的上报方式均未实机验证 |
| 新建文件夹、复制 key / URI / URL | ✅ | ✅ | ✅ | ✅ | key 含 `+`、`?`、空格、Unicode 的场景在两个服务端上验证 |
| 命令面板、右键菜单、快捷键 | n/a | ✅ | ✅ 命令表与对话框可达性；浏览时只列出能执行的命令，搜索时不能执行的也灰显列出（`TestPaletteBrowsingHidesWhatCannotRun`）；主题与语言在浏览时各收成一行，进入后只列其值、光标落在当前值、空搜索框里的 Backspace 返回而删掉最后一个字的 Backspace 不返回（`TestPaletteChoiceOpensItsValues`）；存储桶单独成组，按别名、真实名称和"打开存储桶"都能搜到 | n/a | 菜单的验证见下方"窗口内菜单"一行。2026-11-20 的面板改版只在 headless 快照里看过，没有在原生窗口里操作 |
| 鼠标滚轮平滑滚动（v0.3.0） | n/a | ⚠️ headless 测试（`motion_test.go`，`Tester.Wheel` 与受控时钟）：对象表一格滚轮 30ms 时位于起止之间、静止于正好一格；"减少动态效果"下立即到位 | n/a | n/a | Windows v0.3.0 发布包原生窗口已用系统注入的滚轮输入验证正反向滚动，截图捕获到缓动中间位置与停稳位置；未测连续帧率、物理触控板或系统减少动态效果。触控板的精确滚动按 mygo 的设计不做缓动 |
| 浮层统一外观（v0.2.0）：`overlayPanel`、`listRow`、键帽、命令面板与下拉选项、存储桶设置对话框、Toast | n/a | ⚠️ headless 测试（`overlay_test.go`、`chrome_test.go`、`theme_test.go`）：读取渲染像素确认三种面板亮暗两种边缘（亮色阴影、暗色 1px 描边、Toast 无描边）、命令面板与下拉选项的高亮（键盘与鼠标同为 `ActiveFill`，2026-11-20 起）和当前值对勾（`Accent`）、禁用行不响应悬停；列表行里的快捷键是一段文字（2026-11-20 起不再画键帽）；`theme.go` 之外没有颜色字面量；新 token 的对比度下限；960×600 下全部对话框按钮可达；亮、暗、960×600、1440×900 的快照由人工逐张目视审阅（命令面板、下拉选项、对话框、存储桶设置、Toast） | n/a | n/a | 只在 headless 渲染里看过，没有在原生窗口里目视；暗色阴影与描边的取舍未在真实显示器上比较。Tooltip 采用 mygo 原生外观（延迟、位置、圆角与阴影），颜色取主题的 `Inverse` / `InverseText`。右键菜单与 `⋯` 菜单见下一行 |
| 窗口内菜单（v0.2.0，Windows / Linux）：对象表与存储桶的右键菜单，上传、账户切换器、`⋯` 按钮的菜单 | n/a | ⚠️ headless 测试（`ctxmenu_test.go` 与改写后的各菜单用例，在所有平台上驱动窗口内菜单）：`↑`/`↓` 循环并跳过分隔线与禁用项、`Home`/`End`、首字母跳转、`Enter` 执行动作一次并关闭整条菜单链；`→`/`←`/`Enter` 进出子菜单，`Esc` 先关子菜单再关菜单，关闭后焦点回到对象表；菜单外的左键、右键只关闭菜单，不触发下面的控件；鼠标停留 150ms 打开子菜单，禁用项不响应悬停与单击；窗口缩放、失去焦点时关闭；`Shift+F10` 与菜单键在当前行下方打开、高亮第一项且不改变选择；读取渲染像素确认只有勾选项有 `Accent` 对勾、禁用项文字更淡、禁用项悬停不高亮；960×600 下 en、zh-Hans、en-XA 的右键菜单（在对象表右下角打开）、`⋯` 菜单及其主题子菜单都在窗口内，宽度在 200–320 之间，子菜单在右边缘翻到左侧；比窗口高的菜单限高并在内部滚动；macOS 原生路径（`nativeMenus`）经 mygo 测试器的模拟系统菜单验证；亮、暗、中英文快照（对象右键菜单、`⋯` 菜单、主题子菜单、960×600 右键菜单与账户菜单）经人工目视 | n/a | n/a | 只在 headless 渲染里看过，没有在 Windows 原生窗口或 WSLg 里手动操作；该菜单只在 Windows 与 Linux 使用，macOS 走系统菜单（`nativeMenus`）。macOS 真机运行过应用，但没有逐项记录对菜单的检查。无障碍角色（`RoleMenu`、`RoleMenuItem`、`RoleMenuItemCheckBox`，主题、语言、账户选择用 `RoleMenuItemRadio`，勾选、展开、禁用、`ActiveDescendant`）已设置，`TestMoreMenuChoicesAreRadioItems` 检查主题与语言子菜单为单选项且只勾一项；Windows 原生窗口里已用键盘打开 `⋯` 的主题子菜单、切到深色再切回，但测试没有读无障碍树（mygo 测试器的 `Access`、`AccessByRole` 可以读，还没有用上），也没有用 Narrator、NVDA 或 Orca 验证。自绘菜单不能超出窗口边界；文本输入框与可选文字的编辑菜单仍是系统菜单 |
| 界面动效（v0.2.0）：按钮与行的颜色淡变，对话框、命令面板、下拉、菜单、子菜单、Toast 的出入场，指示线与指示条滑动，开关圆钮，传输面板折叠，检查器内容淡入，进度条，计数滚动，"已保存"淡入淡出 | n/a | ⚠️ headless 测试，用 mygo 测试器的受控时钟逐帧推进（`motion_test.go`）：对话框以弹簧入场，起始约 0.94 倍，过冲不超过 3%，静止位置与关掉动画时的位置相差不超过 0.05，静止后不再请求帧；入场中的对话框按钮与菜单的键盘、指针立即可用；关闭中的对话框与菜单残影不拿焦点、不挡指针，下面的侧栏立即响应；向上翻转的菜单从下方进入；检查器下划线在 40ms 时位于两标签之间、静止于新标签下；开关圆钮 40ms 时在两端之间；传输面板按开关时高度过渡、拖动分隔条时跟手无延迟；Toast 从下方约 8 升起；进度条 50ms 时在新旧宽度之间、静止于 128（0.8）；"已保存"与对话框退出时像素在出现与消失之间；悬停 30ms 时填充色在两色之间、静止为 `SurfaceHover`；检查器打开时内容淡入、位置不动；"减少动态效果"下对话框、菜单、Toast、传输面板、下划线、圆钮直接到位，遮罩仍淡入；窗口缩放的那一帧布局即为最终位置，不再移动；所有动画结束后窗口不再请求帧。亮、暗快照（对话框、命令面板、菜单、子菜单的入场与退出中间帧，传输面板展开、Toast 升起）经人工目视：缩放中的面板阴影未被裁切，弹簧过冲肉眼不可见 | n/a | n/a | 只在 headless 渲染与受控时钟里看过，没有在 Windows 原生窗口、WSLg 或 macOS 上实际观看动画，帧率与流畅度未知；没有在开启系统"减少动态效果"的真实桌面上验证 mygo 读取到该设置。行为测试关掉动画运行（`options.still`）。Tooltip 无入场动画（mygo v0.6.3 仍无钩子）；浮层内元素的位移过渡会被 mygo 在过渡后重新布局覆盖，所以浮层入场用 `AnimatePose`，多个 Toast 叠放时位置变化不过渡 |
| 信息密度：状态栏左侧去重、桶行区域只在不同时显示、传输条无任务时隐藏（状态栏上传下载计数可开关面板）、TOOLS 折叠、工具栏选择类按钮按需显示且右侧开关位置固定 | ✅ `Settings.CollapseTools`：缺省展开、读旧写新、展开时不写键（`settings_test.go`） | ⚠️ headless 测试（`density_test.go`）：布局位置、折叠与键盘切换、传输条从 0 长出与减少动态效果、标签断点最坏情形（en、zh-Hans、en-XA）；Windows 原生 `--demo` 截图 4 张 | n/a | n/a | 没有在原生窗口里目视传输条长出的动画 |
| 检查器标头标签与缩放布局 | n/a | ⚠️ headless 测试（`layout_test.go`、`chrome_test.go`、`ux_audit_test.go`）：检查器 320 与 280 宽下标头标签宽度不为 0；布局由当前帧尺寸算出；断点带 ±12 回滞；960×600 下审计通过 | n/a | n/a | Windows 发布包已实际拖动缩至约 960×600 并检查最大化和还原，稳定帧布局正常；未测拖动期间的连续帧，分隔条拖动色没有目视审阅 |

### 传输

| 功能 | API | 界面 | 协议测试 | 真实端到端 | 备注 |
| --- | --- | --- | --- | --- | --- |
| 文件、文件夹、整桶上传与下载，拖放 | ✅ | ✅ | ✅ | ✅ MinIO、VersityGW | 含 11 MiB 分片往返与 SHA-256 校验 |
| 三类队列并发、取消、重试 | ✅ | ✅ 传输面板（事件驱动） | ✅ | ⚠️ 队列跑了全部端到端传输，上限数值没有断言 | |
| 批次完成通知与历史上限 | ✅ `TaskGroup`；重试扫描创建独立批次 | ✅ headless 通知与最近操作断言 | ✅ `TestUploadOutcomeSurvivesHistoryEviction` 与 `TestRetryScanOutcomeSurvivesHistoryEviction`：1002 个文件首项失败，通知为 1001 成功、1 失败，历史仍为 1000 条；`TestSkippedTransfersAreNotAnnouncedAsCompleted`：上传与下载的全部跳过、混合结果 | ❌ 这些边界未对真实服务端验证 | `TestTransferScanRetryUsesFreshGroup` 覆盖上传、下载在有逐项失败、扫描启动前失败和排队取消后的重试；`TestGroupResultSurvivesEvictionAndActiveClear` 验证活动批次清空历史后保留汇总 |
| 五种冲突策略 | ✅ | ✅ | ✅ | ✅ MinIO、VersityGW | |
| create-only 条件写入与降级 | ✅ | ✅ | ✅ 412 不会被当成"不支持"，501 会降级 | ✅ 并发写竞态（MinIO、VersityGW） | 降级到 HEAD 时存在竞态，只写权限账户在 rename 策略下无法识别重放产生的 412 |
| 分片上传与下载、四种 checksum 模式 | ✅ | ✅ 设置 | ✅ | ✅ 含 VersityGW 的服务端 checksum | 自定义 endpoint 默认 `WhenRequired` |
| 下载先写临时文件、校验后原子发布 | ✅ | ✅ | ✅ | ✅ | 不依赖硬链接；大小不符的下载被拒绝 |
| 上传重放与下载扫描重试 | ✅ | ✅ 原有重试入口 | ✅ `upload_replay_test.go`：竞争对象正文不同、同尺寸同 mtime 重传、单次及分片上传成功响应丢失；`download_mkdirretry_test.go`：排除目录障碍后重试，非法路径仍被拒绝 | ❌ 新增边界仅经本地协议夹具验证 | 上传正文确认会增加读取；加密 ZIP 重复上传核对解密内容 |
| 服务端忽略 Range 时改用单连接下载 | ✅ | n/a | ✅ `download_norange_test.go`：httptest 对每个 GET 返回整个对象，内容与进度正确，只发一次带 Range 的探测和一次不带 Range 的 GET；小对象不重试；回退前已取消则不回退 | ❌ 没有忽略 Range 的真实服务端可验证（issue #6 的用户 NAS 未复测） | 修复前同一测试稳定复现 `EOF` |
| 未完成的分片上传：列出与中止 | ✅ | ✅ | ✅ | ✅ MinIO、VersityGW | MinIO 对"文件夹"前缀返回空，应用改为列整桶后本地过滤 |
| 任务栏进度（v0.3.0） | n/a | ⚠️ headless 测试（`flows_test.go`）：合计规则（字节、无字节时按项目数、不确定、0.5% 步长、只算排队与进行中的任务）；本地测试服务上传进行中时设置进度、值不变时不重复设置、取消后清除；不绘制界面时，后台任务事件仍更新进度并在完成后清除 | n/a | ⚠️ Windows v0.3.0 发布包连接隔离的本地限速 S3 测试服务：128 MiB 上传全程最小化，任务栏出现进度并在完成后清除；512 MiB 上传最小化时显示进度，恢复窗口取消后再次最小化，进度清除，服务端确认请求取消 | 未验证 macOS Dock 或 Linux dock；本地测试服务不代表真实云端验收。v0.7.4 起按批次合计（`TestTaskbarProgressOfGroups`）：排队中的文件按扫描时的大小计入总字节；批次还在扫描时总量仍会增长，百分比可能回落 |
| 上传进度按字节推进，上传不再按分段缓冲（v0.7.4） | ✅ `storage.Upload` 自行分段：可定位的文件每个请求直接读文件的一段，进度在 HTTP 层按实际发送的字节计数 | ⚠️ Windows 原生 `--demo` 窗口（内存 S3，上传限速 2 MiB/s）：同时上传 60 MiB 与 10 MiB 文件，约 2 秒时批行和两个文件行已显示进度、速度与剩余时间 | ✅ `internal/storage/upload_test.go`：单次与分段上传每步不超过 64 KiB、重试的分段只计一次、失败的调用扣回已计字节、两种 checksum 模式下 Create / UploadPart / Complete 的字段、条件头在 PutObject 与 Complete 上、流式正文、中止保留 ExpectedBucketOwner 与 RequestPayer、超过 10000 段时放大分段；Linux race 通过 | ⚠️ MinIO（固定版本，`scripts/integration-minio.sh`）：存储层与应用层套件通过，含分段上传往返与四种 checksum 模式；没有对 VersityGW 或真实云端重跑 | 内存 S3、限速 4 MiB/s、默认设置下的测量：10、60、200 MiB 上传分别在 0.10、0.10、0.20 秒首次出现进度，此前 60 MiB 要 12 秒；8 个 60 MiB 并发上传的堆增量 8.8 MiB，此前 904 MiB。不可定位的正文（跨账户复制）仍按分段缓冲，总量上限 64 MiB，分段本身更大时为一个分段 |
| 传输卡住检测（v0.7.4） | ✅ 对象传输的正文连续 60 秒没有数据时中止该次 HTTP 尝试，`storage.StallError` | n/a | ✅ `internal/storage/transport_test.go`：卡住的下载与上传被重试并恢复、始终卡住时报错、慢速和限速下的传输不误触发、列表请求不受影响 | ❌ 没有对真实代理或断网场景运行 | 只对带 `TransferContext` 的请求生效；等待响应头仍由 60 秒的头部超时负责 |
| 文件夹传输跨重启续传，扫描可暂停（v0.7.4） | ✅ `transfers.json` 格式 2（仍读格式 1）保存扫描位置；扫描不占 `Limits.Other`；`PauseAll` 与整批暂停连扫描一起暂停 | ⚠️ headless `TestGroupRowPauseResumeAndCancelWholeFolder`；Windows 原生 `--demo` 窗口：150 个文件的文件夹上传，暂停整批、继续整批、取消整批各操作一次 | ✅ `internal/app/resume_test.go`、`scanonce_test.go`：上传与下载在扫描中途保存，恢复到新的 Service 后每个文件只传一次、已完成的文件不再 HEAD 或 GET；读格式 1；只有扫描在运行时的 `PauseAll`；扫描等待时批量删除照常开始；跳过和失败的条目跨暂停、跨会话只报告一次；Linux race 通过 | ❌ 没有在原生窗口里退出并重启验证，也没有对真实服务端运行 | 两次会话之间在扫描位置之前新建的条目不会被发现；保存与关闭之间完成的文件恢复后会再传一次；恢复后的批次只统计本次会话的文件 |
| 批次行与任务阶段（v0.7.4） | ✅ `GroupProgress`、`TaskGroups`、`PauseGroup`、`ResumeGroup`、`Task.Phase`（`internal/app/groupprogress_test.go`：历史淘汰后计数仍准确、按实际大小修正、20000 个任务的整批暂停、继续与取消、速度过期后发一次 0） | ⚠️ headless `transfers_group_test.go`：批行、成员行、排队行与已暂停行，进行中、已暂停、排队中的顺序，成功的扫描不计数，状态栏与托盘的计数，已暂停任务不阻止重启或关闭，下载完成不重列文件夹，同步结束的消息与最近操作；亮暗、中英文、960×600 与 1440×900 快照经目视；Windows 原生 `--demo` 窗口目视批行、成员行和“还有 N 个排队中” | ✅ 内存 S3 | ❌ | 阶段标签（准备中、压缩中、校验中、解码中、正在完成）只在 headless 快照里看过；“失败”标签的数字仍包含已取消的任务；整批暂停后的“N 个已暂停”一行只有 headless 测试 |
| 界面在大量任务与大选择下的开销（v0.7.4） | n/a | ⚠️ headless `perf_test.go`，按次数断言：5 万个任务移除一半只应用一批且不整表重建索引、1000 个进度事件合并、静止帧不重算选择汇总、筛选不重新排序、文件夹大小成批到达、预览在后台生成、超过 4000 万像素的图片被拒绝；`motion_test.go`：进度条直接到位，静止后不再请求帧 | n/a | n/a | 没有在原生窗口测量帧率或 CPU 占用。传输列表没有显示时的无帧唤醒只在真实窗口里接上，headless 测试不覆盖。选择缓存读取 mygo `Selection` 的私有字段 `stamp`，读不到时退回每帧重算，测试断言该字段存在 |

### 对象属性与批量操作

| 功能 | API | 界面 | 协议测试 | 真实端到端 | 备注 |
| --- | --- | --- | --- | --- | --- |
| 复制、移动、重命名（同桶、跨桶、版本化源） | ✅ | ✅ 含拖到文件夹移动、F2 | ✅ `bulk_moveattrs_test.go` 另覆盖相同正文但 metadata、标头、存储类别或 tags 不同，以及复制后属性改变 | ✅ MinIO、VersityGW；新增属性竞争场景只在本地夹具验证 | 移动先复制并校验再删源；仅限同一账户；无法读取 tags 时保留源并报失败 |
| 批量删除，逐项结果 | ✅ | ✅ 带数量估算 | ✅ unknown 不重发；SDK 重发后才被拒绝的请求记为 unknown（`TestDeleteObjectsRefusedResendIsUnknown`） | ✅ 1105 个 key（MinIO） | |
| headers、metadata、tags 编辑 | ✅ | ✅ 单对象与多选编辑；离开对象时清除标签草稿（headless） | ✅ 未改动的属性全部保留；`TestTagsDraftDoesNotFollowBucket`、`TestTagsDraftClearsWhenSelectionChanges` 经内存 S3 验证切桶、取消选择、选择文件夹和多选；`edit_test.go` 与 `objops_test.go`（重命名）覆盖保存标头、改存储类别、重命名、恢复或删除版本完成时保留另一对象和同一对象的草稿与选择 | ✅ MinIO、VersityGW（tags 可用） | 标签草稿切换与 N1 草稿保留回归尚未在修复后的原生窗口或真实端点验收 |
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
| 建桶（region、对象锁）、删桶、清空桶 | ✅ | ✅ 删桶需输入桶名；删桶与清空不在桶右键菜单里，只在"存储桶设置 › 清空或删除"，两个对话框开头说明修改发生在存储服务上并列出账户和服务（`bucket_mgmt_test.go`、`objops_test.go`、`i18n_screens_test.go`，headless） | ✅ | ✅ MinIO / ❌ VersityGW（按规则不建桶、不删桶） | 新入口与提示框只在 headless 渲染里看过 |
| 14 项桶配置的读、写、删 | ✅ | ✅ 简单项表单，其余 JSON | ✅ 403 不当作"未配置" | ⚠️ MinIO：版本控制、策略、生命周期、标签 ✅，CORS 不支持（报 Unsupported）；VersityGW 只读 | |
| 整桶复制、移动、下载 | ✅ | ✅ 桶右键菜单 | ✅ | ⚠️ 经对象级用例间接覆盖，界面入口本身没有端到端 | |

### 同步

| 功能 | API | 界面 | 协议测试 | 真实端到端 | 备注 |
| --- | --- | --- | --- | --- | --- |
| 同步作业增删改、预览、执行（上传 / 下载 / 镜像，三种比较模式，排除规则） | ✅ | ✅ 预览筛选、执行确认（删除项用危险色） | ✅ 执行前逐项复核前提 | ✅ MinIO、VersityGW | `size_time` 会漏检同尺寸改动；checksum 模式下，其他工具写入、缺少 `pail-sha256` 的对象会被反复判为需要传输 |
| 压缩对象 checksum 下载与远端镜像删除 | ✅ | ✅ 原有同步入口 | ✅ `sync_checksum_artifact_test.go`：新旧压缩对象下载后收敛、同尺寸修改仍被识别；`syncrun_remoteonly_test.go`：远端 `CON` 等对象可删除，本地非法路径仍被拒绝 | ❌ 这些边界仅经本地协议夹具验证 | 旧压缩对象缺少正文校验和时，每次 checksum 比较会读取正文 |
| 镜像跳过目录链接的远程子树 | ✅ `PreviewSync`、`RunSync` | ✅ 复用同步预览 | ✅ `TestMirrorProtectsSkippedDirectoryDescendants`：子树保留，名称仅有相同前缀的其他目录仍按镜像规则删除 | ❌ 该边界未对真实服务端验证 | 系统无法创建目录链接时测试明确跳过 |
| 本地删除进系统回收站 | ✅ | ✅ | ✅ Windows 与 Linux 回收站各有测试 | ⚠️ 只验证了预览，没有真的移入回收站 | Windows 拒绝长路径与网络盘 |

### 上传规则与压缩

| 功能 | API | 界面 | 协议测试 | 真实端到端 | 备注 |
| --- | --- | --- | --- | --- | --- |
| 规则（headers、存储类别、SSE、压缩），草稿测试条 | ✅ | ✅ | ✅ | ✅ MinIO、VersityGW | |
| GZIP、ZIP、AES-256 ZIP 与压缩级别；下载时解码 | ✅ | ✅ | ✅ 编解码级别验证了各级别，并与 7z 互通 | ⚠️ 各格式往返 ✅，级别未在端到端变化 | 经 Cloudflare 的 gzip 对象下载会被拒绝（代理解压导致大小不符），不会写出错误文件 |
| 跳过已有加密 ZIP 目标 | ✅ `Upload` | ✅ 复用跳过策略 | ✅ `TestSkippedEncryptedUploadDoesNotNeedPassword`：跳过不读取缺失密码，真正上传时仍失败，原对象不变 | ❌ 该边界未对真实服务端验证 | 尺寸比较仍先计算压缩产物 |
| MIME 类型表 | ✅ | ✅ | ✅ | ⚠️ 经上传间接覆盖 | |

### 预签名 URL 与 CloudFront

| 功能 | API | 界面 | 协议测试 | 真实端到端 | 备注 |
| --- | --- | --- | --- | --- | --- |
| 预签名 URL（GET / PUT、有效期、指定版本、多对象） | ✅ | ✅ | ✅ | ✅ MinIO、VersityGW | |
| CloudFront：distribution 列表、多路径 / 整桶失效、状态 | ✅ | ✅ 仅 AWS 账户；headless 回归覆盖跨账户旧结果、重开失效对话框、慢轮询收敛 | ✅ `httptest`，可控 API 完成顺序 | ❌ | 从未对真实 CloudFront 或 AWS 运行 |

### v0.6.2：传输控制、跨账户、归档与网络

| 功能 | API | 界面 | 协议测试 | 真实端到端 | 备注 |
| --- | --- | --- | --- | --- | --- |
| 传输改用 `feature/s3/transfermanager`（v0.5.7，替代已弃用的 `feature/s3/manager`） | ✅ | n/a | ✅ `internal/storage` 原有上传、下载、取消即中止、忽略 Range、进度测试全部通过；下载固定用 Range、不请求校验和 | ⚠️ MinIO 集成套件通过（含分片往返、分片复制、特殊键）；外部端点未跑 | transfermanager 仍是 0.x |
| 暂停、继续、全部暂停/继续；退出时保存、启动时以暂停恢复 | ✅ `pause_test.go`：排队与运行中暂停、暂停组的后续文件、暂停中继续、取消已暂停、保存后在新会话恢复并上传 | ✅ 传输面板按钮；headless 套件通过，未单独断言按钮 | ✅ 内存 S3 | ❌ | 继续时文件从头传输；同步任务不保存；文件夹扫描的暂停与续传见“文件夹传输跨重启续传”一行 |
| 跨账户复制、移动（流式 GET → 上传） | ✅ `crosscopy_test.go`：两个内存服务、移动删源、标头、元数据、标签保留、7 MiB 分片、断连不留对象 | ✅ 粘贴跨账户；整任务失败时提示错误 | ✅ | ❌ | 命令行的 `cp`/`mv` 仍限一个账户 |
| 上传、下载限速 | ✅ 设置校验 | ✅ 设置 › 传输 | ✅ `ratelimit_test.go`：96 KiB 在 64 KiB/s 下两个方向均不少于 1 s；取消立即返回；没有传输标记的请求不限速；改速率会唤醒等待中的读取；40 路并发时单次等待不超过约 450 ms 且总量准确 | ❌ | v0.7.4 起只限对象传输，新值立即生效 |
| 归档取回（Glacier 灵活检索、Deep Archive） | ✅ `archive_test.go` | ✅ 对话框、检查器状态；`TestRestoreFromArchiveDialog` | ✅ 内存 S3 新增 RestoreObject 与 `x-amz-restore` | ❌ 未对 AWS 运行 | 不估算费用 |
| 请求者付费、账户自己的代理或直连 | ✅ `account_network_test.go` | ✅ 账户对话框高级选项 | ✅ 每个请求带 `x-amz-request-payer`；代理模式到传输配置 | ❌ | 账户代理不支持用户名和密码 |
| CloudFront 启用、停用分配 | ✅ | ✅ 先确认；`TestCloudFrontDisableAsksFirst` | ✅ `httptest`：GET 配置、带 ETag 的 PUT，只改 `Enabled` | ❌ | 从未对真实 CloudFront 运行 |
| 预签名链接以机密内容写入剪贴板（mygo `ClipboardOptions.Concealed`）；macOS 传输时 `Power.KeepRunning` | n/a | ⚠️ headless 无系统剪贴板与电源管理，回退普通写入 | n/a | ❌ 未在真机确认剪贴板历史与 App Nap | |

### 安全、日志与本地化

| 功能 | API | 界面 | 协议测试 | 真实端到端 | 备注 |
| --- | --- | --- | --- | --- | --- |
| 脱敏文件日志（默认关闭，5 MiB 轮转保留 3 个） | ✅ | ✅ 设置开关、打开日志文件夹 | ✅ 10 种机密形态的脱敏表 | ✅ 外部套件运行后扫描日志，不含密钥与 access key | 手动输入、未存为引用的 ZIP 密码和 SSE-C key 只能靠标签或字段名识别 |
| 中英文界面 | n/a | ✅ 设置 → 外观、`⋯` 菜单，无需重启 | ✅ 键与占位符一致、中文文案风格、伪语言在 960×600 下全部对话框可达 | n/a | `internal/app` 的任务标题和命令行未本地化 |
| 亮 / 暗主题（米纸、石墨）、对比度 | n/a | ⚠️ 无头渲染的两套配色截图（浏览、对话框、设置页）经目视审阅；2026-11-20 用 `--demo` 在 Windows 原生窗口里看过浏览、命令面板、菜单、设置、工具页 | ✅ 两套配色过同一组对比度下限与状态双线索检查；旧设置文件里的 `lightColors` / `darkColors` 被忽略 | n/a | 对象表表头的列分隔线与字重无法按规范修改（mygo 限制，2026-11-20 核实 v0.10.0 与主分支仍写死 600）。2026-11-20 调整米纸的中性色度、石墨的层次和统一的选中色之后，只看过 headless 前后对比，没有在原生窗口里重看。README 截图仍是调整前的颜色；官网已不沿用应用配色 |
| 设置页（分区栏、成组卡片、主题缩略图） | n/a | ⚠️ 无头渲染的亮、暗、中、英截图（1333×833 与 960×600）经目视审阅；没有在原生窗口里看过 | ✅ 点分区后页面滚到该分区并保持标记、手动滚动后标记交还给页面位置、窄窗收起分区栏；原有设置、代理、保险库测试按新布局通过 | n/a | |
| 动效：当前项标记的移动（分段控件、侧栏、分区栏、标签下划线、缩略图的环）、开关、配色淡变 | n/a | ⚠️ 无头渲染里抽取起步、途中、静止三帧目视；没有在原生窗口里连续观看，实际帧率与流畅度没有测量 | ✅ 状态在点击当帧提交；标记从旧项出发、途中在两者之间、静止时正好在新项上且窗口不再请求帧；途中改选从所在位置接着走；宽度不同的段之间宽度同时变化；超出距离上限、上一帧没有标记、"减少动态效果"、动画关闭时当帧到位；标签不随标记移动；配色淡变途中没有东西移动 | n/a | |

### 命令行与发布

| 功能 | API | 界面 | 协议测试 | 真实端到端 | 备注 |
| --- | --- | --- | --- | --- | --- |
| `pail` 命令行（accounts、account、vault、bucket alias、buckets、ls、cp、mv、rm、mb、rb、presign、url、versions、restore、stat、cat、mkdir、du、tags、acl、props、storage-class、multipart、cloudfront、settings、sync 及 sync job） | ✅ | n/a | ✅ 进程内对 `internal/s3test` | ❌ 命令行外壳本身没有对真实服务端运行 | 它调用的应用层在真实服务端验证过。`cloudfront` 的 `invalidate` 与 `ls` 在命令行层只测了非 AWS 账户被拒绝和确认流程，没有对 CloudFront 或模拟端点运行 |
| CLI 结果与参数边界 | ✅ | n/a | ✅ `TestBulkSummaryBeyondItemRetention` 删除 10001 项并核对汇总；`TestSavedSyncJobSelectsItsAccount` 覆盖多账户、显式账户冲突和保险库；`TestDoubleDashDownloadDestination`、`TestInterspersedEndOfOptions` 验证 `--` 与选项值 | ❌ 这些边界未对真实服务端验证 | 不从截断的逐项明细推导总数 |
| CLI 年龄参数、URL 与 JSON 输出 | ✅ | n/a | ✅ `output_age_regression_test.go`：非有限数、溢出、微小天数被拒绝，JSON 写失败返回非零且不执行未成功输出的同步预览；`browse_publicurl_regression_test.go` 保留端点转义 | ⚠️ Windows 实际 CLI 进程对本地 HTTP 夹具验证参数和 URL；无真实云端 | 输出失败不回滚已经完成的远端操作 |
| 发布内容与版本验证 | n/a | n/a | ✅ 真实 PE、ELF、Mach-O 的 amd64 与 arm64 夹具（含剥离符号的构建）核对版本戳，拒绝版本前缀或后缀、未写版本戳、重复、格式错误和截断的数据；同名 CLI-only 包不能替换完整包 | ✅ v0.6.2 起由发布流程实际构建、打包、验包 | 发布产物用 `-s -w` 剥离符号，版本从 `internal/buildstamp` 的标记读取；v0.6.1 及更早的产物用旧方式验证 |
| 外部验收清理结果 | n/a | n/a | ✅ `ext_cleanup_regression_test.go`：版本和 MPU 列举被拒绝、出错或最终复查失败时报告错误；明确不支持的 API 保留兼容回退 | ❌ 修复后未重跑真实外部端点 | 列举失败不能视为没有残留 |
| 官网上的命令行演示（`site/cli-demo.json`） | n/a | ⚠️ 2026-11-20 用 ego-browser 截图目视过中英文页面上的终端（四个标签、首个示例、亮暗两种主题） | ✅ `TestSiteCLIDemo`（`cmd/pail`）进程内对 `internal/s3test` 真跑四条命令并与黄金文件比较；`tools/site` 的测试核对页面与内嵌 JSON；`node --test scripts/site-motion.test.mjs` 核对"减少动态效果"在页面打开时切换也立即生效 | ❌ 没有对真实服务端录制 | 时间戳与预签名的凭据、签名做了规范化（见测试注释）；预签名示例用 AWS 类型账户在本地签名，没有联网 |
| 官网落地页与文档页（2026-11-21 第六次重做：配色、系统字体栈、终端、下载表、文档页三栏沿用第五次；首屏的点云桶随滚动散开，拼成下方应用截图的窗口线框，截图随后淡入；功能区改为五个章节（对象、传输、版本、预签名 URL、同步计划）共用一个吸顶窗口，窗口内容由滚动位置决定，不再定时切换；窗口标签是跳到对应章节的链接；每个章节链接到自己的文档页，文档页开头是同一个视图的静止完成态；其余功能逐行链接到文档；下载区之前有文档引导；窄屏和矮窗口不吸顶，每个章节的视图跟在文字下面，进入视口时播放一次；关闭脚本或减少动态效果时显示完成态） | n/a | ⚠️ 本地构建后用 ego-browser（Chromium）打开并截图目视：中文首页 1440×900 亮暗两色的首屏、点云到线框到截图的过渡、五个章节各自的中间态与完成态；英文首页 1440 亮色；中文首页 390×844 与 360 的首屏和五个章节；1440×2000 高窗口（桶不散开，没有空白）；减少动态效果（桶静止、截图直接显示、视图为完成态）；关闭脚本（视图在文字旁或文字下，画布隐藏）；版本文档页在 1440 与 390 下的开头视图。用真实滚轮滚过全页，点击过窗口标签，用 Tab 键走过章节链接，点击章节链接到了文档页。首页（中英）与中文版本文档页在 360、390、768、1024、1280、1440、1920 下没有横向滚动。脚本测量（本地服务器，匀速 1100 px/s 滚动，CPU 降速是模拟，不是真机）：1440×900 不降速 60 fps，最长帧 17 ms，没有长任务；390×844 降速 4 倍 58 fps，17 帧超过 25 ms，没有帧超过 50 ms，没有长任务；降速 6 倍约 50 fps，8 个长任务，最长 108 ms（改版前同条件 1 个，62 ms）。滚动过程 CLS 桌面 0.002、手机 0.006。没有在真实手机上看过，没有在 Firefox 和 Safari 上看过，没有用读屏软件走过，跨页过渡没有在截图里看到，没有部署，线上的 LCP 没有测 | ✅ `go test ./tools/site/`（含 `TestFeatureViewsMatchTheirGuidePages`、`TestSyncViewShowsRecordedPlan`、`TestOtherFeaturesAndGuideAreLinked`）；`node --test scripts/site-motion.test.mjs`；html-validate 33 个页面 | n/a | 功能窗口里的数据是样例，同步计划的三行取自录制的命令行输出；手机上首页比改版前长约四成（五个视图全部展开，不再是手风琴）；跨页过渡只在支持的浏览器里有 |
| 官网首屏截图（`docs/images/main-{light,dark}-{en,zh}.webp`，四张 macOS 原生截图） | n/a | ⚠️ 所有者在 macOS 上用 `--demo` 截取（2026-11-07）；用 ego-browser 读 DOM 核对过中文页在亮、暗两种配色下选中的文件与尺寸，没有目视页面 | ✅ `TestHeroScreenshotsFollowLanguageAndTheme` 核对四张尺寸一致、页面写的宽高等于文件、两种语言页与两份 README 各用自己的图 | n/a | Open Graph 卡片（`social.webp`）由英文亮色截图重新合成，未在任何社交平台上刷新验证 |
| 发布包（Windows / Linux / macOS，amd64 与 arm64）与 SHA256SUMS | n/a | n/a | ✅ 打包工具保留 Unix 权限位（tar.gz 与 zip 各有测试） | ✅ Windows 与 Linux amd64 解压后运行过；macOS arm64 与 amd64 在真机上手动运行过，首次启动需 `xattr -dr com.apple.quarantine`；六平台 CI 原生测试与版本冒烟通过 | 已在 GitHub Releases 发布（0.6.4 及以前的布局） |
| 0.7.0 包布局：顶层目录 `Pail`、稳定的可执行文件名、macOS 命令行在 `Pail.app` 里、Linux `install.sh`、`.mygo-files`、单独的更新包；桌面程序以 `production=1` 与 `mygo_noinspector,mygo_nodownload` 构建 | n/a | n/a | ✅ `tools/release`（`TestLaunchAssets`、Linux 上真跑 `TestInstallScript`）、`tools/pack`（`TestEntriesWithoutPrefix`）、`tools/verify-release` 的测试；2026-11-21 Windows 与 Linux（WSL）全量 `go test ./...` 通过 | ⚠️ v0.7.0 已按新布局发布：下载的 Windows amd64 包是 `Pail/` 下的 `pail-desktop.exe`、`pail.exe`、`.mygo-files`、`LICENSE`，原生运行通过；Linux amd64 包在 WSL 里运行 `install.sh` 到临时 HOME，`~/.local/bin` 的两个链接和 `.desktop` 条目都用绝对路径 | `install.sh` 没有在真实桌面环境里运行，启动器条目和图标没有目视；macOS 包只经 `verify-release` 与 CI，尚未在 Mac 上运行 |
| 自动更新：设置 › 更新（检查策略、下载来源、立即检查、下载并安装、重启）、`pail update [--check]`、`update-check` / `update-source` 设置、增量更新、两个来源互相回退 | ✅ `internal/selfupdate`（`Check`、`Install`、`Supported`、`Due`、`CleanLeftovers`）；`Settings.Updates` 与 `SetUpdateCheck`（`TestUpdateSettings`） | ⚠️ headless 测试（`cmd/pail-desktop/update_test.go`）：不能更新时的说明、检查、下载进度、取消、重启、传输进行中拒绝重启、失败与重试、启动时按策略检查、设置保存、更新日志链接；快照 `settings-update-available` 经人工目视。命令行 `cmds_update_test.go` 用替身检查与安装 | ✅ `httptest`：来源回退、只走 HTTPS、整包与增量安装、签名不符被拒（`internal/selfupdate`）；bsdiff 与增量应用（含 `FuzzPatch`、`FuzzApplyDelta`）、文件清单、权限位（`internal/updatefmt`）；`tools/updates` 的签名、校验与镜像（`TestSignVerifyMirror`），密钥必须与公钥配对（`TestSigningKeyMustMatchPublicKey`） | ⚠️ v0.7.0 的发布流程在 `release` 环境里用 `PAIL_UPDATE_KEY` 签了六个目标的更新包并通过 `verify`（首个带更新的版本，没有增量包）；下载后本地 `tools/updates verify` 再次通过。发布包 `pail update --check` 对 GitHub 真实运行（Windows amd64 与 WSL 里的 Linux amd64）报告 0.7.0 是最新版本，选 pail.download 时（当时镜像还没有更新文件）回退到 GitHub 也得到同样结果。v0.7.1 首次带增量包（六个目标各一个 0.7.0→0.7.1 增量，1.3–1.9 MiB）：从 GitHub 下载的 v0.7.0 发布包在临时目录里用 `pail --config-dir <临时目录> update` 真实更新到 0.7.1，Windows amd64 下载 1.5 MiB、WSL 里的 Linux amd64 下载 1.4 MiB，都走增量；更新后 `pail version` 与 `pail-desktop --version` 为 0.7.1，目录里没有残留文件，Linux 的两个程序仍是 0755，更新后的 Windows 桌面程序 `--smoke-exit 6s` 原生窗口启动并以 0 退出。⚠️ 更新后 Linux 的 `.mygo-files` 是 0600（压缩包里是 0644），不影响使用。推送 2ad66d5 后，Windows amd64 另一份 v0.7.0 把来源设为 `mirror`，从 pail.download 增量更新到 0.7.1（1.5 MiB）。v0.7.2 首次带两个基线（0.7.0、0.7.1）的增量包；从 GitHub 下载的 v0.7.1 发布包真实增量更新到 0.7.2：Windows amd64 1.1 MiB、WSL 里的 Linux amd64 998.9 KiB；v0.7.3 带三个基线，v0.7.2 发布包从 GitHub 增量更新到 0.7.3：Windows amd64 787.5 KiB、Linux amd64 702.0 KiB；推送 c3d4731 后，Windows amd64 另一份 v0.7.2 把来源设为 `mirror`，从 pail.download 增量更新到 0.7.3（787.5 KiB）；推送 257a87c 后，Windows amd64 另一份 v0.7.1 把来源设为 `mirror`，从 pail.download 增量更新到 0.7.2（1.1 MiB）。整包更新与桌面界面里的更新没有真实运行 | macOS 的替换（进程退出后由辅助进程换 `Pail.app`）只交叉编译，`TestInstall` 在 macOS 上跳过；Windows 上替换正在运行的 exe 只在测试里用普通文件模拟。没有在原生窗口里操作过更新界面 |
| pail.download 下载镜像（`/download/`、`/update/`，网站构建时由 `tools/updates mirror` 写入，只留最新稳定版） | n/a | n/a | ✅ `TestSignVerifyMirror`：校验 `SHA256SUMS` 与签名、改写清单 URL、只保留一个版本；`scripts/test-publish-site.sh`（Linux）：镜像与 `SHA256SUMS` 不符时不发布 | ⚠️ 2026-11-21 推送 de5fa88 后 Woodpecker 镜像了当时的最新版 0.6.4：`/download/` 有六个压缩包和 `SHA256SUMS`，经 Cloudflare 取到的 `SHA256SUMS` 与目录列表是 `no-cache`（`cf-cache-status: DYNAMIC`），压缩包是 `public, max-age=604800`；0.6.4 没有更新文件，`/update/` 为空。0.7.0 的 `/update/` 要等这次记录推送后的构建。v0.7.1：推送验证记录 2ad66d5 后几分钟内，`/update/update-windows-amd64.json` 报告 0.7.1（`no-cache`，`cf-cache-status: DYNAMIC`），`/download/SHA256SUMS` 列出 0.7.1 的压缩包，从镜像的增量更新真实可用。v0.7.2：推送验证记录 257a87c 后约 1 分钟，`/update/update-windows-amd64.json` 报告 0.7.2（`no-cache`，`cf-cache-status: DYNAMIC`），`/download/SHA256SUMS` 列出 0.7.2 的压缩包，首页与中文首页的下载区版本为 0.7.2 并带 macOS 隔离命令。v0.7.3：推送 c3d4731 后约 1.5 分钟，清单报告 0.7.3（`no-cache`，`cf-cache-status: DYNAMIC`），`SHA256SUMS` 与两个首页的下载区为 0.7.3 | 发版后要再推送一次 `master` 才会更新 |

## 已知限制与未验证

- AWS 的 STS AssumeRole 与 CloudFront 没有在真实环境测试，只在 `httptest` 上验证。Amazon S3、Cloudflare R2、Wasabi 已手动测试可用。
- macOS `.app` 没有开发者签名、未公证；下载后首次启动需要执行 `xattr -dr com.apple.quarantine`。
- VersityGW 上的版本控制：服务端未开启，相关场景被跳过。
- 经 Cloudflare 的 gzip 对象：代理会解压，下载被拒绝（不会写出错误文件）。
- SSE-S3、SSE-KMS、SSE-C 成功路径：MinIO 没有配置 KMS，VersityGW 忽略加密；https 端点上的 SSE-C 成功路径从未运行。
- 超过 5 GiB 的对象经原地改写（存储类别、加密、属性）的分片复制路径：未验证。
- 本地镜像删除移入真实回收站：只验证了预览。macOS 同名文件并发移入回收站的修复（`RENAME_EXCL`，不支持时的不覆盖兜底）只在 Windows 与 Linux 上测试了共用的占名逻辑和兜底，macOS 专用测试只交叉编译，没有在 Mac 上运行。
- 窗口隐藏到托盘时任务完成后的通知与列表刷新：mygo 隐藏窗口不画帧这一点来自源码阅读，修复只有 headless 测试（`follow_hidden_test.go`），没有在任何原生窗口里隐藏后实测。
- 窗口内菜单（Windows 与 Linux）：只有 headless 测试与快照目视，没有在原生窗口里手动操作，也没有用读屏软件验证。macOS 的系统右键菜单与系统菜单栏：没有目视审阅。
- 界面动效：只有受控时钟的 headless 测试与快照目视，没有在任何原生窗口里观看过，流畅度、帧率与"减少动态效果"的系统设置都未在真实桌面上验证。
- 窗口拖动缩放时的布局抖动：应用侧的晚一帧回读和断点翻转已修，有 headless 回归测试。缩放中的帧仍同步绘制，系统按钮与帧是否原子合成没有确认，没有采集拖动中的连续帧，因此不标记为解决。
- 依赖库（mygo）升级带来的窗口行为改动、托盘（图标、菜单、关闭即隐藏、单实例、系统通知）、文件夹大小、桶别名、代理字段和最近操作列表：只有 headless 测试，没有在原生窗口里逐项操作；Linux 桌面没有运行这些功能；macOS 上所有者每个版本都手动运行应用，但没有逐项记录这些功能。每个发布包都经 `verify-release` 复核，Windows 与 Linux amd64 包解压后冒烟通过。CPU 占用与内存没有做前后对比。
- 代理服务器：本地 HTTP 代理与 SOCKS5 服务器上的协议测试通过（含用户名和密码），但没有经过真实代理软件、真实 S3 服务或 AWS STS 验证；不支持 NTLM 等其他认证方式。
- `internal/app` 产生的任务标题（传输面板）和命令行输出未本地化，仍为英文。
- MinIO 套件没有加 `-race` 运行；`-race` 只跑了单元与协议测试。
- 只写权限账户在 rename 策略下无法识别重放产生的 412；大小写不同的同名 key 的编号不会跨扫描记忆。
- 命令行外壳没有对真实服务端运行。
- 自动更新：命令行从 GitHub 的真实检查与增量更新（0.7.0→0.7.1、0.7.1→0.7.2、0.7.2→0.7.3、0.7.3→0.7.4）已在 Windows amd64 与 WSL 里的 Linux amd64 运行；从 pail.download 的增量更新已在 Windows amd64 运行（0.7.0→0.7.1、0.7.1→0.7.2、0.7.2→0.7.3）；整包更新、arm64 与 macOS 的替换路径都没有真实运行；更新界面没有在原生窗口里操作。
- v0.7.2（`pail://` 配置链接，清空与删除存储桶移入"存储桶设置 › 清空或删除"并加远端提示，官网的 macOS 隔离命令）：Windows 全量 `gofmt`、`go vet ./...`、`go test ./...` 通过，Linux（WSL）`go build ./...` 与相关包测试通过，本地 `tools/release` 打包 darwin/arm64 与 windows/amd64 并通过校验。4e7269c 的 CI 与 CodeQL 通过，没有未处理的 CodeQL 告警，`check-release` 通过；签名标签 v0.7.2。Release 运行 45617117935 成功，GitHub 上六个压缩包、`SHA256SUMS`、六个更新包、十二个增量包（0.7.0、0.7.1 各六个，0.9–1.9 MiB）和六个清单已发布。下载后 `verify-release`（压缩包、更新包、校验和、二进制目标、权限）与 `tools/updates verify` 通过，`sha256sum -c` 全部 OK；Windows amd64 解压后两个程序的版本为 0.7.2，桌面 exe 的文件版本为 0.7.2.0，`--smoke-exit 6s` 原生窗口启动并以 0 退出（空配置）。从 GitHub 下载的 v0.7.1 发布包在临时目录里用 `pail --config-dir <临时目录> update` 真实更新到 0.7.2：Windows amd64 下载 1.1 MiB、WSL 里的 Linux amd64 下载 998.9 KiB，都走增量；更新后两个程序为 0.7.2，没有残留文件，Linux 的两个程序仍是 0755（`.mygo-files` 仍变为 0600），更新后的 Windows 桌面程序 `--smoke-exit 6s` 以 0 退出。配置链接没有在真实桌面上点击过（协议登记只在发布版启动时进行，冒烟测试用了 `--config-dir`，不登记）；macOS 与 arm64 包只经 CI 的原生测试任务。
- v0.7.3（没有账户时从欢迎页粘贴配置链接、"添加账户"标题行的"用配置链接填写…"、粘贴对话框读取剪贴板里的链接，Linux `install.sh` 登记 `pail://`）：第一次发布失败。标签 v0.7.3 先打在 949906d 上，Release 运行 45625768589 的 Linux 打包被 `verify-release` 拒绝：e16bb09 把压缩包里 `pail.desktop` 的启动行改成 `Exec=pail-desktop %u`，校验仍只认 `Exec=pail-desktop`；发布前本地只打包校验了 darwin 与 windows。没有发布任何文件。da41eb9 让校验接受两种写法并加了测试，本地六个目标全部打包并通过校验；按所有者的决定删除了 Forgejo 与 GitHub 上的旧标签，在 da41eb9 上重新签名打 v0.7.3（旧标签对象 84ca92b 不再使用）。da41eb9 的 CI 与 CodeQL 通过，没有未处理的 CodeQL 告警，`check-release` 通过。Release 运行 45629138307 成功，GitHub 上六个压缩包、`SHA256SUMS`、六个更新包、十八个增量包（0.7.0、0.7.1、0.7.2 各六个，0.6–2.0 MiB）和六个清单已发布。下载后 `verify-release` 与 `tools/updates verify` 通过，`sha256sum -c` 全部 OK；Linux 压缩包的 `pail.desktop` 为 `Exec=pail-desktop %u` 并声明 `x-scheme-handler/pail`，`install.sh` 含 `xdg-mime default`，macOS 的 Info.plist 声明 `pail`；Windows amd64 解压后两个程序的版本为 0.7.3，桌面 exe 的文件版本为 0.7.3.0，`--smoke-exit 6s` 以 0 退出（空配置）。从 GitHub 下载的 v0.7.2 发布包用 `pail --config-dir <临时目录> update` 真实更新到 0.7.3：Windows amd64 下载 787.5 KiB、WSL 里的 Linux amd64 下载 702.0 KiB，都走增量；更新后两个程序为 0.7.3，Linux 的 `pail.desktop` 随之换成新内容，更新后的 Windows 桌面程序 `--smoke-exit 6s` 以 0 退出。配置链接仍没有在真实桌面上点击过；macOS 与 arm64 包只经 CI 的原生测试任务。
- v0.7.4（上传进度与内存、传输面板的批次行、文件夹传输续传、卡住检测、限速范围，以及一批小修复）：5f12702 的 CI（运行 45857483892，15 个任务，含三个系统的原生测试、竞态与 MinIO）与 CodeQL（45857483936）通过，没有未处理的 CodeQL 告警，`check-release` 通过。签名标签 v0.7.4 打在 5f12702 上，Release 运行 45858673519 通过，发布 37 个文件。下载全部公开产物后：`SHA256SUMS` 的六项一致，`tools/verify-release`（六个安装包与六个更新包）和 `tools/updates verify` 通过。Windows amd64：解压后的 `pail version` 与 `pail-desktop --version` 报告 0.7.4，用独立配置目录运行原生窗口冒烟，截图为欢迎页；从 0.7.3 的发布包经 GitHub 增量更新到 0.7.4（下载 2.3 MiB）。WSL 里的 Linux amd64：发布包校验和一致，两个程序报告 0.7.4；从 0.7.3 增量更新到 0.7.4（下载 2.0 MiB）。arm64 与 macOS 的发布包没有运行；macOS 上不覆盖发布的改动只由 CI 的 macOS 测试运行过。发布前的验收见“传输”一节标注 v0.7.4 的各行。
- v0.7.0（自动更新与 `pail update`、`Pail` 文件夹与稳定的可执行文件名、macOS 命令行在 `Pail.app` 里、Linux `install.sh`、pail.download 镜像）：Windows 与 Linux（WSL）全量 `gofmt`、`go vet`、`go test`、`go build` 与 `scripts/test-publish-site.sh` 通过。提交 de5fa88 的 CodeQL 报告 `internal/updatefmt` 解包的 `go/zipslip` 与 `go/unsafe-unzip-symlink`：逐个检查都在目录内的链接串起来可以指向目录外，后面的条目会经它写出去（解包前已验签，要利用必须有签名密钥）。242afce 改为按解析链接后的真实路径检查，`TestExtractStaysInside` 在 Linux 上对旧代码失败、对新代码通过；CodeQL 仍按数据流报告其中两处（#7、#9），因自定义的 `within` 检查不被识别，判为误报并驳回，#8 已关闭。242afce 的 CI 与 CodeQL 通过，`check-release` 通过；签名标签 v0.7.0。Release 运行 44866700841 成功，GitHub 上六个压缩包、`SHA256SUMS`、六个更新包和六个清单已发布。下载后 `verify-release`（压缩包、校验和、二进制目标、权限）与 `tools/updates verify` 通过，`sha256sum -c` 全部 OK；Windows amd64 解压后 `pail version` 与 `pail-desktop --version` 为 0.7.0，桌面 exe 的文件版本为 0.7.0.0，`--smoke-exit 6s` 原生窗口启动并以 0 退出（空配置）；Linux amd64 包在 WSL 解压后两个程序的版本为 0.7.0。macOS 与 arm64 包只经 CI 的原生测试任务，尚未手动运行。
- v0.7.1（AWS 配置文件与 `aws login`、`pail account profiles`、按配置文件区域新建存储桶与生成公开链接，来自 #9）：Windows 全量 `gofmt`、`go vet`（含 `pail_demo`）、`go test ./...` 通过，Linux（WSL）`go vet ./...` 与相关包测试、darwin/arm64 交叉编译通过。32fd4cf 的 CI 与 CodeQL 通过，没有未处理的 CodeQL 告警，`check-release` 通过；签名标签 v0.7.1。Release 运行 45553105529 成功，GitHub 上六个压缩包、`SHA256SUMS`、六个更新包、六个增量包和六个清单已发布。下载后 `verify-release`（压缩包、更新包、校验和、二进制目标、权限）与 `tools/updates verify` 通过，`sha256sum -c` 全部 OK；Windows amd64 解压后两个程序的版本为 0.7.1，桌面 exe 的文件版本为 0.7.1.0，`--smoke-exit 6s` 原生窗口启动并以 0 退出（空配置）。真实更新见上方“自动更新”一行。AWS 配置文件相关功能没有对真实 AWS 账户运行；macOS 与 arm64 包只经 CI 的原生测试任务。
- v0.5.0（命令行账户、保险库与对象命令，状态栏计数）：新命令只有进程内对 `internal/s3test` 的测试；状态栏与折叠传输条的计数动画只有 headless 测试（含加载期间不滚动的断言）和一张开发构建的 `--demo` 截图，没有在原生窗口里目视动画过程。发布包：GitHub Actions 的 CI 与 CodeQL 通过且没有未处理的告警，`check-release` 通过；`verify-release` 复核下载的六个归档与 `SHA256SUMS` 通过；Windows amd64 解压后 `pail version` 与 `pail-desktop --version` 为 0.5.0，桌面程序 `--smoke-exit 6s` 原生窗口启动并以 0 退出（空配置，停在欢迎页），发布包不再接受 `--demo`；Linux amd64 包在 WSL 解压后两个程序的版本为 0.5.0；macOS 包由所有者在 Apple silicon 与 Intel 两台 Mac 上手动运行；Windows 与 Linux 的 arm64 包只经 CI 的原生测试任务。
- v0.6.1（审计修复：异步结果归属、草稿保留、账户与保险库跨进程写入、移动与上传正文核对、重试批次、发布验包读取 `main.version`）：每项修复各有先失败后通过的回归测试；界面修复只有 headless 测试，没有在原生窗口里逐项操作；macOS 回收站修复只交叉编译，没有在 Mac 上运行。工具链升级到 Go 1.27.4（GO-2026-6859）。发布包：GitHub Actions 的 CI 与 CodeQL 通过且没有未处理的告警，`check-release` 通过；`verify-release` 复核下载的六个归档与 `SHA256SUMS` 通过；Windows amd64 解压后 `pail version` 与 `pail-desktop --version` 为 0.6.1，桌面 exe 的文件版本为 0.6.1.0，`--smoke-exit 6s` 原生窗口启动并以 0 退出（空配置）；Linux amd64 包在 WSL 解压后两个程序的版本为 0.6.1；macOS 与 arm64 包只经 CI 的原生测试任务，尚未手动运行。
- v0.6.3（浅色“晴空”与深色“夜空”两套配色、设置页分区栏与成组卡片、当前项标记的移动与配色淡变、长对象键从开头截断、退出后限时结束以免挡住下次启动；mygo v0.10.0；CI 分片原生测试并加入 Staticcheck；官网单栏版式）：Windows 与 Linux 全量测试、Staticcheck、CI 的 `-race` 分片通过。桌面包的 `-race` 分片以 `-parallel 1` 运行：并行的 headless 窗口会同时写 mygo 的包级状态，首次 CI 运行因此报了数据竞争。Windows 本地全量测试有一次 `TestVersionsMultiSelectDeleteAndDownload` 报文件被占用，单独重跑 5 次与整包重跑均通过。界面改动只有 headless 测试与截图目视，没有在原生窗口里逐项操作；MinIO 套件只在 CI 运行；外部端点与真实 AWS 未运行。发布包：标签提交的 CI 与 CodeQL 通过且无未处理告警，`check-release` 通过；`verify-release` 复核下载的六个归档与 `SHA256SUMS` 通过；Windows amd64 解压后 `pail version` 与 `pail-desktop --version` 为 0.6.3，桌面 exe 的文件版本为 0.6.3.0，`--smoke-exit 6s` 原生窗口启动并以 0 退出；Linux amd64 包在 WSL 解压后两个程序的版本为 0.6.3；macOS 与 arm64 包只经 CI 的原生测试任务，尚未手动运行。
- v0.6.4（每个主题只保留一套配色、更平和的字号层级、选择与设置页细节、`pail restore` 用法说明修正；官网与文档重做）：提交 94a3e48 的 CI 与 CodeQL 全部通过，`check-release.mjs` 预检通过；CodeQL 对 `site/static/home.js` 的 `js/xss-through-dom` 与之前 `app.js` 的同一处判为误报并驳回。Release 运行 44800758777 成功，GitHub 上六个压缩包和 `SHA256SUMS` 已发布。没有下载发布文件做本地复验和原生版本检查。
- v0.6.2（暂停与继续、跨会话恢复传输、跨账户复制与移动、限速、归档取回、请求者付费与账户代理、CloudFront 启用停用、上传规则自定义存储类别、预签名链接机密剪贴板、macOS 传输保持全速；mygo v0.9.0；传输迁移到 transfermanager；发布产物剥离符号并以版本戳验包；网站结构化数据与 IndexNow）：Windows 与 Linux 全量测试、`internal/app` 与 `internal/storage` 的 `-race`、MinIO 集成套件通过；外部端点与真实 AWS 未运行。CodeQL 在首个提交报告 `go/incorrect-integer-conversion`（`archive.go` 的天数转换），修复后告警关闭、无未处理告警。发布包：标签提交的 CI 与 CodeQL 通过，`check-release` 通过；`verify-release` 复核下载的六个归档与 `SHA256SUMS` 通过；Windows amd64 解压后 `pail version` 与 `pail-desktop --version` 为 0.6.2，桌面 exe 的文件版本为 0.6.2.0，`--smoke-exit 6s` 原生窗口启动并以 0 退出（空配置）；Linux amd64 包在 WSL 解压后两个程序的版本为 0.6.2；macOS 与 arm64 包只经 CI 的原生测试任务，尚未手动运行。
- v0.6.0（界面与缺陷修复）：本版的修复各有先失败后通过的回归测试（`cmd/pail-desktop/regressions_test.go`、`internal/app`、`cmd/pail` 等），界面改动（单色表面、动效收敛、右键菜单、减少确认）只有 headless 测试与快照，没有在原生窗口里逐项操作。发布包：GitHub Actions 的 CI 与 CodeQL 通过，`check-release` 通过；`verify-release` 复核下载的归档与 `SHA256SUMS` 通过；Windows amd64 解压后 `pail version` 与 `pail-desktop --version` 为 0.6.0，桌面 exe 的文件版本为 0.6.0.0，`--smoke-exit 6s` 原生窗口启动并以 0 退出（空配置）；macOS 与 arm64 包只经 CI 的原生测试任务，尚未手动运行。
- v0.5.3（账户指定存储桶，来自 PR #7）：指定存储桶的协议测试只放行指定文件夹的请求，并有 MinIO 端到端测试；桌面界面只有 headless 测试，没有在原生窗口里操作，也没有对真实的受限 IAM 密钥运行。发布包：GitHub Actions 的 CI 与 CodeQL 通过，`check-release` 通过；`verify-release` 复核下载的六个归档与 `SHA256SUMS` 通过；Windows amd64 解压后 `pail version` 与 `pail-desktop --version` 为 0.5.3，桌面程序 `--smoke-exit 6s` 原生窗口启动并以 0 退出（空配置）；macOS 与 arm64 包只经 CI 的原生测试任务，尚未手动运行。
- v0.5.2（mygo 升级到 v0.8.3，选中大量对象时每帧只汇总一次选择，新建文件夹对话框不受旧结果影响，失焦窗口里选中行保持 `Selection` 底色）：新建文件夹、取消的按压、紧凑筛选的 Esc 与失焦窗口的选中行都只有 headless 测试（含像素断言）；所有者在 Windows 原生窗口里用修复前的开发构建看过激活窗口的选中行，并发现了失焦时选中行变成深色实底的问题，修复后的失焦外观没有在原生窗口里复看，其余改动也没有逐项操作；Alt 打开 Windows 菜单栏与 macOS 的失焦外观没有验证。发布包：GitHub Actions 的 CI 与 CodeQL 通过且没有未处理的告警，`check-release` 通过；`verify-release` 复核下载的六个归档与 `SHA256SUMS` 通过；Windows amd64 解压后 `pail version` 与 `pail-desktop --version` 为 0.5.2，桌面程序 `--smoke-exit 6s` 原生窗口启动并以 0 退出（空配置）；Linux amd64 包在 WSL 解压后两个程序的版本为 0.5.2；macOS 与 arm64 包只经 CI 的原生测试任务，尚未手动运行。
- v0.5.1（桌面界面布局与信息密度：路径栏紧凑筛选、窄表格列宽、工具页单一空状态、状态栏与侧栏去重、传输条按需显示、工具分组折叠、选择类按钮按需显示、拖放提示）：界面改动只有 headless 测试与开发构建的 `--demo` 截图（亮暗、960×600 与 1440×900、中英文），没有在原生窗口里逐项操作；传输条长出动画与选择条动画期间拖放提示的位置只有逐帧断言。对象表表头字重由 mygo 固定为 600，中文表头仍显得偏粗；列边悬停颜色没有目视。发布包：GitHub Actions 的 CI 与 CodeQL 通过且没有未处理的告警，`check-release` 通过；`verify-release` 复核下载的六个归档与 `SHA256SUMS` 通过；Windows amd64 解压后 `pail version` 与 `pail-desktop --version` 为 0.5.1，桌面程序 `--smoke-exit 6s` 原生窗口启动并以 0 退出（空配置）；Linux amd64 包在 WSL 解压后两个程序的版本为 0.5.1；macOS 与 arm64 包只经 CI 的原生测试任务，尚未手动运行。
