# Pail 架构

本文描述现在的代码结构、线程模型和必须长期保持的安全不变量。接口细节见 [app-api.md](app-api.md)，视觉与交互见 [design.md](design.md)，验证状态见 [status.md](status.md)。

## 1. 包与职责

```
cmd/pail-desktop   桌面程序（Go + mygo）。只调用 internal/app
cmd/pail           命令行。只调用 internal/app，与桌面程序共用配置目录和秘密存储
internal/app       Service：账户、浏览、任务队列、传输、批量、版本、属性、规则、同步、桶管理、CloudFront、日志、加密保险库
internal/storage   S3 协议层：client、传输（分片）、复制、条件写入、能力探测
internal/cloud     AWS 专有服务（类型化 CloudFront）
internal/rules     上传规则、GZIP / ZIP / AES-256 ZIP、同步比较
internal/safepath  下载路径安全：拒绝 `..`、绝对路径、Windows 保留名、符号链接逃逸；临时文件加发布
internal/trash     本地删除进系统回收站（Windows、Linux 按 freedesktop 规范）
internal/s3test    内存版 S3 测试服务，`--demo` 也用它
integration/       真实服务端验收：MinIO 夹具与外部 S3 兼容端点套件
tools/genicons     从 SVG 生成图标、.ico、.icns、Windows 资源对象
tools/pack         发布打包，保证可执行文件权限为 0755
```

依赖方向只能向下：`cmd/*` → `internal/app` → `internal/storage` / `internal/cloud` / `internal/rules` / `internal/safepath` / `internal/trash`。界面和命令行不直接访问 S3 SDK。

## 2. 线程规则

- 网络和文件传输绝不在 UI 线程上执行。UI 调用 Service 的方法，返回 task ID 的方法立即返回，工作在队列里进行。
- 后台协程不直接改界面状态。需要更新界面时，把闭包交给 `Window.Update`，由 UI 线程执行。事件回调（`Subscribe`）在非 UI 线程触发，每个任务最多每秒 10 次。
- 钥匙串、网络、磁盘 I/O 不在 `Service.mu` 内执行；该锁只保护内存 map。钥匙串可能弹出系统对话框，持锁调用会冻结界面。
- 所有网络调用带 `ctx`，关闭窗口时取消。

## 3. 任务队列模型

- 三类队列（上传、下载、其他）各自 FIFO，各有并发上限（默认 8，可在设置中调整）。下调上限不打断已运行的任务。
- 一个任务占一个槽。上传或下载多个文件时，每个文件一个任务，共享 `GroupID`；扫描文件夹或前缀本身是一个"其他"任务，扫描完成后再展开。
- 批量服务端操作（删除、复制、改属性等）是一个"其他"任务，内部用 `BulkWorkers` 个 worker 处理各项。任务持有逐项结果 `ItemResult`，状态为 `succeeded`、`failed`、`unknown`、`unprocessed`、`skipped`。
- 进度写入 O(1)；已结束的任务最多保留 1000 条，超出按 FIFO 淘汰；事件对 UI 节流。
- 取消：排队中的任务直接移除，运行中的任务取消 `ctx`。重试只创建新任务处理 `failed` 和 `unprocessed` 的项。

## 4. 安全不变量

这些规则在代码里有测试固定，修改前要先理解为什么存在。

1. **不做探测写入。** 不为了判断 provider 能力而写入探测对象。条件头被 provider 拒绝（501 等）时，按账户缓存该结论并降级为先 HEAD 再写；412 是真实的前提失败，不能当成"不支持"。
2. **逐项结果。** 批量操作单项失败不影响其他项，也不在首错处停止。结果汇总为成功、失败、未知、未处理。
3. **未知结果不重放。** 写操作结果未知（例如 `DeleteObjects` 的确认缺失、重复或不是请求的 key）时，标记为 `unknown`，不自动重试。整体 4xx 才标为 `failed`。
4. **移动 = 先复制并校验，再删除。** 校验目标的 ETag 与大小与复制结果一致后才删源。版本化桶按复制时的 VersionId 精确删除源；未版本化时按 key 删除，存在竞态。
5. **下载原子且不覆盖。** 先 `safepath.Join` 得到目标路径，在同目录创建临时文件，GET 带 `If-Match` ETag，校验大小与 ETag 后按冲突策略发布（Windows `MoveFileEx` 不带 REPLACE；Linux `renameat2` NOREPLACE，回退 link 再回退 Lstat + rename）。失败时删除临时文件，绝不发布不完整的文件。大小不一致的下载（例如代理解压了 gzip 对象）被拒绝。
6. **条件写入与降级。** 冲突策略 skip / rename 优先使用 `If-None-Match: *`（分片上传的 Complete 请求也带），provider 不支持时退化为先 HEAD，竞态在文档中说明。
7. **原地改写后复核。** 改存储类别、加密、属性用 CopyObject 加 `MetadataDirective: REPLACE`，带回所有未修改的 headers 和 metadata；tags 保持，ACL 尽力保留。完成后对每个对象 HEAD 复核：服务端返回 200 却没有生效的项记为 `failed`，复核本身失败记为 `unknown`。读不到 tags 或 ACL（AccessDenied）时拒绝改写，避免丢失。超过 5 GiB 走分片复制。版本化桶会产生新版本。
8. **同步只做预览过的动作。** 执行时每个动作先复核前提（create-only、`If-Match`、本地大小与 mtime），世界变了就报告 skipped。本地扫描出错即中止预览；被跳过或排除的项保护其远端对应物不被删除。本地删除只进回收站，Windows 拒绝长路径与网络盘（否则系统会改为永久删除）。
9. **确认归界面。** 破坏性、批量和云端范围的操作只弹一个确认框，写明账户、桶、前缀、数量、字节；只有删除桶需要输入桶名。Service 不保存计划、令牌或过期时间，`Estimate` 只为确认框计数。
10. **秘密不落配置文件。** secret key、session token、SSE-C key、ZIP 密码只进系统钥匙串或加密保险库；配置文件只保存引用名。日志脱敏：URL 查询串、Authorization、SigV4、密钥标签与敏感字段名，以及从秘密存储读出的字面值。
11. **403 不是"不支持"。** 错误统一包装为 `*OpError`，`Kind` 区分 NotFound、AccessDenied、Unsupported、Conflict、Network、Canceled、Invalid、Locked、Other。
12. **自定义 endpoint 的 checksum 默认 `WhenRequired`**（含 SDK 分片上传器）。经代理时 `Accept-Encoding` 照常发送但不参与签名，复制源 `x-amz-copy-source` 完整百分号编码。

## 5. 数据位置与旧版迁移

配置目录是 `os.UserConfigDir()/Pail`（Windows `%AppData%\Pail`，Linux `~/.config/Pail`，macOS `~/Library/Application Support/Pail`），可用 `--config-dir` 覆盖，权限 0700。文件原子写入（临时文件、fsync、rename），权限 0600：

| 文件 | 内容 |
| --- | --- |
| `accounts.json` | 账户（不含秘密；访问密钥 ID 不是秘密，保存在这里） |
| `settings.json` | 设置 |
| `upload-rules.json` | 上传规则 |
| `mime-types.json` | 扩展名到 Content-Type 的表 |
| `sync-jobs.json` | 同步作业 |
| `secret-refs.json` | 秘密引用名（SSE-C key、ZIP 密码），不含值 |
| `vault.json` | 可选的加密保险库（格式 `pail-vault`） |
| `logs/pail.log` | 脱敏日志，默认关闭；5 MiB 轮转，保留 3 个，权限 0600 |

秘密默认放系统钥匙串，服务名 `Pail`，条目键 `account/<id>/secret`、`account/<id>/session`、`ref/<name>`。文件损坏时加载报错，不静默重置。

界面语言选择单独保存在用户数据目录的 `language` 文件中（空表示跟随系统），因为窗口创建前就要读取。

**兼容旧版数据：永远不能破坏。** 项目早期叫 openS3，已有用户数据必须继续可用：

- 已有的 `openS3` 配置目录在首次启动时自动迁移一次到 `Pail`；
- 钥匙串旧服务名 `openS3` 的条目自动迁移到 `Pail`；
- 旧格式的保险库（`opens3-vault`）仍可打开；
- 对象 metadata 读取旧键 `opens3-mtime`、`opens3-size`、`opens3-sha256`、`opens3-compression`，写入新键 `pail-*`。

原则：读旧写新，不删除用户数据。

## 6. 本地化

- 界面文案放在 `cmd/pail-desktop/locales/en.json`（源语言）和 `zh-Hans.json`，采用 ICU MessageFormat；由 `mygo generate` 生成类型化函数，缺键在编译期报错。
- 测试检查：两个目录的键与占位符一致、中文文案风格（全角标点、中英文之间留空格、不出现"您"）、伪语言 `en-XA` 与 `zh-Hans` 在最小窗口 960×600 下所有对话框可达、目录里的每个键都被代码使用。
- 术语表见 [design.md](design.md)。服务端错误详情保留原文，前面加一句本地化概括。
- 尚未本地化：`internal/app` 产生的任务标题（传输面板）和命令行输出仍为英文。

## 7. 测试分层

| 层 | 位置 | 说明 |
| --- | --- | --- |
| 单元与协议 | `internal/*` | 内存 S3（`internal/s3test`）经真实 SDK 驱动；`httptest` 用于 STS、CloudFront 和故障注入 |
| 界面 headless | `cmd/pail-desktop` | 不开真实窗口：命令表、对话框可达性、对比度、本地化、键盘流程；设置 `PAIL_UI_SNAPSHOT_DIR` 时额外写出亮暗、中英文的截图供人工审阅 |
| 命令行 | `cmd/pail` | 进程内运行，对 `internal/s3test` |
| MinIO 集成 | `integration/` + `scripts/test-integration.ps1` | 隔离的 MinIO 容器，覆盖存储层与应用层 |
| 外部端点 | `integration/ext_*_test.go` + `scripts/test-external.ps1` | 真实 S3 兼容服务；只写本次运行的唯一前缀，结束时清理 |
| 原生冒烟 | `--smoke-exit` / `--smoke-screenshot` | Windows 原生与 WSLg 的窗口截图，由人工审阅 |

`internal/s3test` 不校验签名，只检查 Authorization 头存在；条件请求、版本、分片、tagging 与桶配置（可配置为 NotImplemented）都有实现，并提供故障注入（503、断连、缺失 DeleteObjects 确认、延迟）。真实协议兼容性以 MinIO 和外部端点为准。每层覆盖到什么程度，逐功能记录在 [status.md](status.md)。
