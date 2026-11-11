# Pail 架构

本文描述现在的代码结构、线程模型和必须长期保持的安全不变量。接口细节见 [app-api.md](app-api.md)，视觉与交互见 [design.md](design.md)，验证状态见 [status.md](status.md)。

## 1. 包与职责

```
cmd/pail-desktop   桌面程序（Go + mygo）。只调用 internal/app；tray.go 管托盘图标与关闭即隐藏，foldersize.go 管文件夹大小的界面状态
cmd/pail           命令行。只调用 internal/app，与桌面程序共用配置目录和机密存储
internal/app       Service：账户、浏览、文件夹大小（FolderSize）、任务队列、传输、批量、版本、属性、规则、同步、桶管理、CloudFront、日志、加密保险库
internal/storage   S3 协议层：client、传输（分片）、复制、条件写入、能力探测
internal/cloud     AWS 专有服务（类型化 CloudFront）
internal/rules     上传规则、GZIP / ZIP / AES-256 ZIP
internal/safepath  下载路径安全：拒绝 `..`、绝对路径、Windows 保留名、符号链接逃逸；临时文件加发布
internal/trash     本地删除进系统回收站（Windows、Linux 按 freedesktop 规范）
internal/s3test    内存版 S3 测试服务
internal/demo      演示环境（s3test + 示例数据），桌面端测试和 `-tags pail_demo` 的 `--demo` 使用，不进发布构建
integration/       真实服务端验收：MinIO 夹具与外部 S3 兼容端点套件
tools/genicons     从 SVG 生成图标、.ico、.icns、Windows 资源对象
tools/release      本地与 CI 共用的 Go 发布入口，跨平台构建并自动验包
tools/pack         归档写入，保证可执行文件权限为 0755
```

依赖方向只能向下：`cmd/*` → `internal/app` → `internal/storage` / `internal/cloud` / `internal/rules` / `internal/safepath` / `internal/trash`。界面和命令行不直接访问 S3 SDK。

## 2. 线程规则

- 网络和文件传输绝不在 UI 线程上执行。UI 调用 Service 的方法，返回 task ID 的方法立即返回，工作在队列里进行。
- 后台协程不直接改界面状态。需要更新界面时，把闭包交给 `Window.Update`，由 UI 线程执行。事件回调（`Subscribe`）在非 UI 线程触发，每个任务最多每秒 10 次。
- 异步回调携带发起时的账户、对象或对话框代次，应用结果前核对当前作用域。重新进入工具或重开对话框时，代次继续递增；旧操作成功仍可通知，但不能覆盖新草稿或改变新对话框。
- 对象操作（保存标头、重命名、改存储类别、恢复或删除版本）完成后经 `headTouched` 只刷新它操作的那个对象（账户、桶、key）；详情面板显示的是别的对象时不动。该对象有草稿时在后台重新读取，以新值为基线保留草稿，不增加 `headGen`。
- 保存完成只推进已提交内容的基线，保留等待期间继续输入的内容。即时设置按用户操作顺序写入；旧保存的失败不能回滚更新的选择。轮询同一列表时只保留一个在途请求。
- 窗口隐藏到托盘后 mygo 不再画帧，但 `Window.Update` 的闭包照常在 UI 线程运行。任务完成后的后续处理（通知、列表刷新）由唤醒回合 `uiTurn` 执行，不依赖画帧，每个回调只运行一次。
- 钥匙串、网络、磁盘 I/O 不在 `Service.mu` 内执行；该锁只保护内存 map。钥匙串可能弹出系统对话框，持锁调用会冻结界面。
- 所有网络调用带 `ctx`，关闭窗口时取消。
- 菜单（`ctxmenu.go`）在 Windows 与 Linux 由界面线程在窗口内绘制，状态只有一份 `app.popup`，每帧由 `menuLayer` 构建；菜单项的动作在菜单关闭后的下一帧经 `post` 在界面线程上运行。macOS 交给系统菜单，动作在 mygo 报告选中的那一帧运行。

## 3. 任务队列模型

- 三类队列（上传、下载、其他）各自 FIFO，各有并发上限（默认 8，可在设置中调整）。下调上限不打断已运行的任务。
- 一个任务占一个槽。上传或下载多个文件时，每个文件一个任务，共享 `GroupID`；扫描文件夹或前缀本身是一个"其他"任务，扫描时逐步展开。重试扫描创建新的批次，扫描和新产生的文件任务归入同一批次，结果汇总不混入前一次失败。
- 批量服务端操作（删除、复制、改属性等）是一个"其他"任务，内部用 `BulkWorkers` 个 worker 处理各项。任务持有逐项结果 `ItemResult`，状态为 `succeeded`、`failed`、`unknown`、`unprocessed`、`skipped`。
- 进度写入 O(1)；已结束的任务最多保留 1000 条，超出按 FIFO 淘汰；事件对 UI 节流。
- 任务快照保留完整的逐项计数，CLI 汇总不依赖有数量上限的 `TaskItems`。`TaskGroup` 汇总批次的待完成、实际传输、失败与取消数；只要还有成员在队列或历史中就保留，最后一个成员移除时释放。完成通知使用批次汇总，跳过项不计入传输成功数。
- 取消：排队中的任务直接移除，运行中的任务取消 `ctx`。重试只创建新任务处理 `failed` 和 `unprocessed` 的项。
- 暂停只作用于逐文件的上传和下载：排队中的离开队列，运行中的按取消清理（中止分片上传、删除临时文件）后成为 Paused，不是终态。继续时排到队尾，文件从头传输。
- 限速在 `storage` 的共享传输层按令牌桶限制请求体（上传）和响应体（下载），所有账户共用。

## 4. 安全不变量

这些规则在代码里有测试固定，修改前要先理解为什么存在。

1. **不做探测写入。** 不为了判断 provider 能力而写入探测对象。条件头被 provider 拒绝（501 等）时，按账户缓存该结论并降级为先 HEAD 再写；412 是真实的前提失败，不能当成"不支持"。
2. **逐项结果。** 批量操作单项失败不影响其他项，也不在首错处停止。结果汇总为成功、失败、未知、未处理。
3. **未知结果不重放。** 写操作结果未知（例如 `DeleteObjects` 的确认缺失、重复或不是请求的 key）时，标记为 `unknown`，不自动重试。整体 4xx 才标为 `failed`。
4. **移动 = 先复制并校验，再删除。** 跨账户时复制是带 `If-Match` 的 GET 流式上传到目标账户，正文长度必须等于列出的大小，否则中止上传、不留对象；之后的校验与删除规则相同。目标必须与源的大小、metadata、内容标头、存储类别和 tags 一致；源与目标的 ETag 都是内容 MD5（不含 `-`，即非分片上传或分片复制，且未使用 SSE-KMS / SSE-C）时，还要求 ETag 相同。重用先前的复制或同内容目标时，也要核对属性；读不到 tags 时保留源并报告失败，服务端明确不支持 tags 时允许继续。删源前重新检查当前 ETag、VersionId 和标头；版本化桶的普通删除创建删除标记。未版本化的检查与删除之间仍存在竞态。
5. **下载原子且不覆盖。** 先 `safepath.Join` 得到目标路径，在同目录创建临时文件，GET 带 `If-Match` ETag，校验大小与 ETag 后按冲突策略发布（Windows `MoveFileEx` 不带 REPLACE；Linux `renameat2` NOREPLACE，回退 link 再回退 Lstat + rename）。失败时删除临时文件，绝不发布不完整的文件。大小不一致的下载（例如代理解压了 gzip 对象）被拒绝。
6. **条件写入与降级。** 冲突策略 skip / rename 优先使用 `If-None-Match: *`（分片上传的 Complete 请求也带），provider 不支持时退化为先 HEAD，竞态在文档中说明。
   上传写入遇到重放冲突时，按 ETag 保护的 GET 核对正文后才能宣称已提交；大小、mtime 和 metadata 不能单独证明正文相同。rename 的重复上传识别也核对正文，加密 ZIP 则核对解密后的内容。
7. **原地改写后复核。** 改存储类别、加密、属性用 CopyObject 加 `MetadataDirective: REPLACE`，带回所有未修改的 headers 和 metadata；tags 保持，ACL 尽力保留。完成后对每个对象 HEAD 复核：服务端返回 200 却没有生效的项记为 `failed`，复核本身失败记为 `unknown`。读不到 tags 或 ACL（AccessDenied）时拒绝改写，避免丢失。超过 5 GiB 走分片复制。版本化桶会产生新版本。
8. **同步只做预览过的动作。** 执行时每个动作先复核前提（create-only、`If-Match`、本地大小与 mtime），世界变了就报告 skipped。本地扫描出错即中止预览；被跳过或排除的项保护其远端对应物不被删除。本地删除只进回收站，Windows 拒绝长路径与网络盘（否则系统会改为永久删除）。
   本地路径规则只用于访问本地文件的动作；远端删除按预览中的对象键执行。checksum 上传保留逻辑内容的 `pail-sha256`；压缩对象另存实际正文的 `pail-body-sha256`，用于原始格式的下载比较。旧压缩对象缺少正文校验和时，预览通过带 `If-Match` 的 GET 计算，不改写旧对象。
9. **确认归界面。** 破坏性、批量和云端范围的操作只弹一个确认框，写明账户、桶、前缀、数量、字节；只有删除桶需要输入桶名。Service 不保存计划、令牌或过期时间，`Estimate` 只为确认框计数。
10. **机密不落配置文件。** secret key、session token、SSE-C key、ZIP 密码只进系统钥匙串或加密保险库；配置文件只保存引用名。日志脱敏：URL 查询串、Authorization、SigV4、密钥标签与敏感字段名，以及从机密存储读出的字面值。
11. **403 不是"不支持"。** 错误统一包装为 `*OpError`，`Kind` 区分 NotFound、AccessDenied、Unsupported、Conflict、Network、Canceled、Invalid、Locked、Other。
12. **自定义 endpoint 的 checksum 默认 `WhenRequired`**（含 SDK 分片上传器）。经代理时 `Accept-Encoding` 照常发送但不参与签名，复制源 `x-amz-copy-source` 完整百分号编码。

## 5. 数据位置与旧版迁移

配置目录是 `os.UserConfigDir()/Pail`（Windows `%AppData%\Pail`，Linux `~/.config/Pail`，macOS `~/Library/Application Support/Pail`），可用 `--config-dir` 覆盖，权限 0700。文件原子写入（临时文件、fsync、rename），权限 0600：

| 文件 | 内容 |
| --- | --- |
| `accounts.json` | 账户（不含机密；访问密钥 ID 不是机密，保存在这里） |
| `settings.json` | 设置 |
| `upload-rules.json` | 上传规则 |
| `mime-types.json` | 扩展名到 Content-Type 的表 |
| `sync-jobs.json` | 同步作业 |
| `transfers.json` | 桌面程序退出时未完成的逐文件传输，下次启动以暂停状态恢复后删除 |
| `secret-refs.json` | 机密引用名（SSE-C key、ZIP 密码），不含值 |
| `vault.json` | 可选的加密保险库（格式 `pail-vault`） |
| `logs/pail.log` | 脱敏日志，默认关闭；5 MiB 轮转，保留 3 个，权限 0600 |

机密默认放系统钥匙串，服务名 `Pail`，条目键 `account/<id>/secret`、`account/<id>/session`、`ref/<name>`。文件损坏时加载报错，不静默重置。

账户写入通过 `accounts.lock` 的操作系统文件锁串行化，持锁期间重新读取账户、更新凭据、原子写入账户文件并清理旧凭据。保存、删除、桶别名、凭据迁移和保险库重置共用这个边界；进程退出会释放锁，锁文件保留。另一实例切换机密存储后，旧实例的账户写入返回 Conflict，需重新打开应用。保险库写入同样通过 `vault.lock` 串行化：持锁期间重新读取并用当前密钥解密磁盘内容，再合并写入；另一进程改过主密码时，持旧密钥的实例返回 Conflict，文件保持不变。两把锁的顺序固定为先 `accounts.lock` 后 `vault.lock`，保险库持锁期间不回调 Service。两把锁都限时获取（默认 15 秒，能取得 context 时也响应取消），超时返回包装 `ErrConfigLocked` 的 Conflict，界面显示“另一个 Pail 窗口或命令正在保存设置”。其他配置文件不因此获得跨进程事务保证。

界面语言选择单独保存在用户数据目录的 `language` 文件中（空表示跟随系统），因为窗口创建前就要读取。

**用户数据格式的兼容：** 配置目录、钥匙串服务名、保险库格式（`pail-vault`）和对象 metadata（`pail-*`）都是已发布的数据格式。以后要改，必须读旧写新，不删除用户数据。

## 6. 本地化

- 界面文案放在 `cmd/pail-desktop/locales/en.json`（源语言）和 `zh-Hans.json`，采用 ICU MessageFormat；由 `mygo generate` 生成类型化函数，缺键在编译期报错。
- 测试检查：两个目录的键与占位符一致、中文文案风格（全角标点、中英文之间留空格、不出现"您"）、伪语言 `en-XA` 与 `zh-Hans` 在最小窗口 960×600 下所有对话框可达、目录里的每个键都被代码使用。
- 术语表见 [design.md](design.md)。服务端错误详情保留原文，前面加一句本地化概括。

## 7. 测试分层

| 层 | 位置 | 说明 |
| --- | --- | --- |
| 单元与协议 | `internal/*` | 内存 S3（`internal/s3test`）经真实 SDK 驱动；`httptest` 用于 STS、CloudFront 和故障注入 |
| 界面 headless | `cmd/pail-desktop` | 不开真实窗口：命令表、对话框可达性、对比度、本地化、键盘流程；菜单测试在所有平台上驱动窗口内菜单（`TestMain` 关闭 `nativeMenus`），macOS 原生菜单路径另有一个测试经 mygo 的模拟系统菜单验证；设置 `PAIL_UI_SNAPSHOT_DIR` 时额外写出亮暗、中英文的截图供人工审阅 |
| 界面动效 | `cmd/pail-desktop/motion_test.go` | mygo 测试器的时钟只在测试调用 `Advance` 时前进，所以每个断言都说明自己看的是动画开始后第几毫秒：入场与退出的中间帧、静止帧（与关掉动画时的位置比较）、"减少动态效果"下直接到位、窗口缩放不触发过渡、动画中控件仍可操作、退出中的残影不拿焦点和指针、静止后不再请求帧。其余界面测试用 `options.still` 关掉动画（每帧 `c.DisableAnimations()`），看到的就是静止布局；`harness.playOut` 供需要真实动画又只关心终态的测试推进时钟。设置 `PAIL_UI_SNAPSHOT_DIR` 时 `TestMotionSnapshots` 写出各动画的中间帧 |
| 命令行 | `cmd/pail` | 进程内运行，对 `internal/s3test` |
| MinIO 集成 | `integration/` + `scripts/test-integration.ps1` | 隔离的 MinIO 容器，覆盖存储层与应用层 |
| 外部端点 | `integration/ext_*_test.go` + `scripts/test-external.ps1` | 真实 S3 兼容服务；只写本次运行的唯一前缀，结束时清理 |
| 原生冒烟 | `--smoke-exit` / `--smoke-screenshot` | Windows 原生与 WSLg 的窗口截图，由人工审阅 |

`internal/s3test` 不校验签名，只检查 Authorization 头存在；条件请求、版本、分片、tagging 与桶配置（可配置为 NotImplemented）都有实现，并提供故障注入（503、断连、缺失 DeleteObjects 确认、延迟）。真实协议兼容性以 MinIO 和外部端点为准。每层覆盖到什么程度，逐功能记录在 [status.md](status.md)。
