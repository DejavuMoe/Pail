# internal/app 接口

本文件定义应用层 `internal/app`。桌面程序（`cmd/pail-desktop`）与命令行（`cmd/pail`）只依赖这里列出的类型和方法。整体结构、线程规则和安全不变量见 [architecture.md](architecture.md)。

## 0. 通用规则

- **不在锁内做 I/O。** `Service.mu` 只保护内存 map；钥匙串、网络、磁盘一律在锁外执行。账户凭据首次读取后缓存，保存或删除账户时失效。
- **网络调用都带 `ctx`。** 返回 task ID 的方法立即返回，工作在队列里进行。
- **确认归 UI。** Service 不保存计划、令牌、过期时间，也不做指纹绑定。`Estimate` 只供确认框显示数量和字节。
- **批量逐项结果。** 统一用 `ItemResult`。结果未知（`unknown`）的写操作不自动重放；`Retry` 只重试 `failed` 和 `unprocessed` 的项。
- **错误统一包装。** 用 `*OpError{Op, Code, Message, RequestID, Kind}` 包装，`Kind` 取 `NotFound | AccessDenied | Unsupported | Conflict | Network | Canceled | Invalid | Other | Locked`，UI 据此显示。403 不能当作"不支持"。`Locked` 表示加密保险库已锁定（`errors.Is(err, ErrVaultLocked)`），UI 应弹出主密码输入框后重试；CLI 退出码 2。
- **内存有界。** 已结束的任务最多保留 1000 条（FIFO 淘汰），`ClearFinished` 可手动清理。

## 1. 配置与存储（`settings.go`、`secrets.go`）

配置目录：`os.UserConfigDir()/Pail`，可用 `Options.ConfigDir` 覆盖。文件一律原子写入（临时文件 → fsync → rename），权限 0600。若存在旧版的 `openS3` 目录且 `Pail` 目录尚不存在，首次启动时自动迁移一次。

| 文件 | 内容 |
| --- | --- |
| `accounts.json` | `[]Account`（不含机密） |
| `settings.json` | `Settings` |
| `upload-rules.json` | `[]rules.UploadRule` |
| `mime-types.json` | `map[string]string`（扩展名 → Content-Type） |
| `sync-jobs.json` | `[]SyncJob` |
| `secret-refs.json` | 机密引用名（不含值） |
| `vault.json` | 可选的加密保险库，见 §1.1（仅 `SecretBackend == "vault"` 时存在） |
| `logs/pail.log` | 脱敏日志（`Settings.LogToFile` 开启时） |

文件损坏时加载报错，不静默重置；多实例之间以最后一次写入为准。

钥匙串服务名为 `Pail`（旧服务名 `openS3` 的条目自动迁移），条目键如下：

- `account/<id>/secret`
- `account/<id>/session`
- `ref/<name>`：SSE-C key 或 ZIP 密码

```go
type Options struct {
	ConfigDir string
	Keyring   Keyring // nil = OS keyring (zalando/go-keyring)
	Now       func() time.Time
}
type Keyring interface {
	Get(key string) (string, error)
	Set(key, value string) error
	Delete(key string) error
}
func New(opts Options) (*Service, error)
func (s *Service) Shutdown(ctx context.Context) error // cancels running tasks, waits up to ctx

type Settings struct {
	Theme              string // "system" | "light" | "dark"
	Limits             Limits
	BulkWorkers        int    // per bulk task, default 8
	PartSizeMiB        int    // default 16
	DefaultDownloadDir string
	Conflict           ConflictPolicy // default for uploads/downloads
	ShowVersions       bool
	LogToFile          bool
	SecretBackend        string // "keyring" (default) | "vault"; only SetSecretBackend changes it, SaveSettings keeps the current value
	VaultAutoLockMinutes int    // default 15, 0 = never; 0 is a real value and is NOT replaced by the default in SaveSettings
}
type Limits struct{ Upload, Download, Other int } // each >=1, default 8
func (s *Service) Settings() Settings
func (s *Service) SaveSettings(Settings) error // applies Limits to the running queue

func (s *Service) SetSecret(ref, value string) error
func (s *Service) DeleteSecret(ref string) error
func (s *Service) SecretRefs() []string
```

### 1.1 加密保险库（`vault.go`、`secretbackend.go`）

机密（secret key、session token、SSE-C key、ZIP 密码）默认放系统钥匙串。可选后端 `vault`：配置目录下的 `vault.json`，用主密码加密。`Service.kr` 是按 `Settings.SecretBackend` 转发的 `secretStore`，账户、引用、客户端缓存的代码不区分后端。

```go
const (SecretBackendKeyring = "keyring"; SecretBackendVault = "vault")
type VaultState struct{ Exists, Unlocked bool; AutoLockMinutes int }
type VaultReset struct{ Accounts, Refs []string } // 账户 ID / 引用名：需要重新输入的机密

func (s *Service) SecretBackend() string
func (s *Service) VaultState() VaultState                 // 不访问钥匙串
func (s *Service) UnlockVault(password string) error      // ErrNotFound 无文件；ErrAccessDenied 密码错；ErrInvalid 文件损坏（文件不会被改动）
func (s *Service) LockVault()
func (s *Service) ChangeVaultPassword(old, new string) error // 成功后保险库为已解锁
func (s *Service) SetSecretBackend(ctx context.Context, backend, password string) error
func (s *Service) ResetVault() (VaultReset, error)
```

- **主密码**：至少 8 个字符（按 rune），至多 1024 字节。不做 Unicode 规范化。
- **文件格式**：`{"format":"pail-vault","version":1,"kdf":{"name":"argon2id","time":3,"memoryKiB":65536,"threads":4,"salt":b64(16)},"cipher":"xchacha20poly1305","nonce":b64(24),"data":b64}`。旧格式名 `opens3-vault` 的文件仍可打开。`data` 是 JSON 对象（键 → 值）的密文，附加数据（AAD）是除 `nonce`、`data` 之外的头部的 JSON 编码，所以 KDF 参数、盐、算法名不能被调换。每次写入重新生成随机 nonce；文件 0600，临时文件 + fsync + rename。读取时拒绝范围外参数（内存 8 MiB–1 GiB、time 1–10、threads 1–16），先校验再做 KDF。
- **解密失败的文件永不覆盖**：每次写入前重新读取并用持有的密钥解密；文件头与已解锁状态不一致（别的进程改了密码）返回 `Conflict`，内容无法解密返回 `Invalid`，都不写盘。别的进程对不同键的并发写入会被合并，不会丢失。
- **锁定**：`LockVault` 清零密钥和已解密条目（尽力而为，Go 字符串无法保证擦除），并丢弃客户端缓存（缓存中含明文凭据）；运行中的任务保留已持有的客户端。后端为 vault 且已锁定时，所有需要机密的操作（`Buckets`、`TestAccount`、`SaveAccount` 带新机密、`SetSecret`、`ResolvePassword` ...）立即返回 `Kind == Locked` 的 `*OpError`，不阻塞、不弹系统对话框。启动时后端为 vault 则处于锁定状态。
- **自动锁定**：`VaultAutoLockMinutes` 分钟无活动后锁定（`s.now` 计时，每 10 秒检查一次，访问时也检查）。任何机密读写、使用账户（包括命中缓存）都算活动。
- **迁移 `SetSecretBackend`**：钥匙串不能枚举，所以以账户当前代（`secretGen`）的 secret/session 与 `secret-refs.json` 为清单。顺序：读出全部 → 写入目标 → 回读比较 → 持久化设置 → 删除源。持久化设置之前的任何失败都会清理已写入目标的条目（新建的 vault.json 直接删除），旧后端保持权威且未被改动。只有最后清理源失败时，切换已生效，返回 `Other` 并说明残留。源中已缺失的条目（账户本来就要重新输入）跳过。到 vault：用 `password` 新建（已有文件则必须能用该密码打开）；回钥匙串：保险库须已解锁，或给出 `password`；成功后删除 `vault.json`。迁移期间持有 `acctMu`、`refMu`，账户和引用的写操作被阻塞；读取共享 `backendMu`，不会看到中间状态。
- **`ResetVault`（忘记密码）**：删除 `vault.json`，后端改回钥匙串，把有机密的静态账户的 `HasSecret/HasSession` 清除，使 `Account.SecretMissing`（仅派生、不持久化）为 true；引用名保留、值丢失，返回受影响的账户 ID 和引用名。**数据丢失，调用方必须先展示后果并取得明确确认**，Service 不再确认。
- **无系统钥匙串**：钥匙串返回"没有 Secret Service / D-Bus"类错误（`org.freedesktop.secrets`、`dbus`、`unlock correct collection` 等）时，错误满足 `errors.Is(err, ErrKeyringUnavailable)`，`Kind == Unsupported`，消息 `No system keychain available — use an encrypted vault (...原始错误)`。UI 据此提示并引导到 `SetSecretBackend(vault, ...)`；该切换本身不需要钥匙串（源里没有机密可读时，只需能读到清单中的条目）。
- **日志**：不记录密码、密钥、条目值；密码和读出的值登记到 `lgAddSecret`；只记录 `secret backend switched`、`vault unlocked/locked/password changed/reset`。

**界面约定**：设置页"安全"区（后端选择、创建/切换、修改密码、自动锁定分钟、忘记密码）；收到 `Kind == Locked` 时弹出主密码框，`UnlockVault` 成功后重试原操作；`VaultState` 轮询（或操作后刷新）显示锁图标，菜单提供"立即锁定"；`Account.SecretMissing` 的账户在列表上标红并引导重新输入；收到 `ErrKeyringUnavailable` 时在账户对话框显示引导。所有调用放在后台线程，通过 Window.Update 回到 UI。

## 2. 账户（`accounts.go`、`clients.go`）

```go
type Account struct {
	ID, Name           string
	Provider           string // "aws" | "s3" (generic compatible)
	Endpoint, Region   string
	PathStyle          bool
	Credentials        string // "static" | "profile" | "default" | "anonymous"
	AccessKeyID        string // not secret
	Profile            string
	RoleARN            string
	ExternalID         string
	RoleSessionName    string
	InsecureSkipVerify bool
	ChecksumMode       string // "auto" (default) | "when_required" | "when_supported"
	CDNURL             string   // optional base URL or template; see CDN below
	CDNBuckets         []string // buckets served by the CDN; empty = all
}
type Secrets struct{ SecretAccessKey, SessionToken string }

func (s *Service) Accounts() []Account
func (s *Service) SaveAccount(a Account, sec *Secrets) (Account, error)
// sec == nil 表示保留已有机密。先写新机密，再更新账户记录，最后删除旧机密。
func (s *Service) DeleteAccount(id string) error // also deletes its keyring entries
func (s *Service) TestAccount(ctx context.Context, a Account, sec *Secrets) error // ListBuckets with a throwaway client
```

**CDN。** `CDNURL` 是基础 URL 或模板。含 `{key}` 时替换 `{bucket}`（原桶名）与 `{key}`（按 `PublicURL` 同样的规则转义，保留 `/`）；不含占位符时为 `<base>/<bucket>/<key>`；只有 `{bucket}` 没有 `{key}` 的模板无效。只允许 http(s)，要求 host，不含用户信息、query、fragment。`CDNBuckets` 去空、去重，每项须是合法桶名。`CDNURL(loc, key)` 不发请求，账户无 CDN、桶不在 `CDNBuckets`、桶名为空、key 为空或以 `/` 结尾时返回 `""`。

客户端缓存按 accountID 保存 `*storage.Client` 和 `*cloud.Client`，惰性构建，账户变更时失效。

## 3. 浏览与对象信息（`browse.go`、`objects.go`）

```go
type Location struct{ AccountID, Bucket, Prefix string } // Prefix "" or ends with "/"
type Bucket struct {
	Name, Region string
	Created      time.Time
}
func (s *Service) Buckets(ctx context.Context, accountID string) ([]Bucket, error)

type Entry struct {
	Key, Name     string // Name = last path segment (folders keep trailing "/" stripped)
	Folder        bool
	Size          int64
	Modified      time.Time
	StorageClass  string
	ETag          string
}
type ListOptions struct {
	Flat    bool   // no delimiter
	Token   string
	MaxKeys int32  // default 1000
}
type Page struct {
	Entries   []Entry
	NextToken string
}
func (s *Service) List(ctx context.Context, loc Location, opt ListOptions) (Page, error)

type ObjectInfo struct {
	Key, VersionID, ETag   string
	Size                   int64
	Modified               time.Time
	StorageClass           string
	ContentType            string
	Headers                []Header          // the 6 editable standard headers, in fixed order
	Metadata               map[string]string // lower-case keys, without x-amz-meta-
	SSE, KMSKeyID          string
	Restore                string // raw x-amz-restore
	LockMode               string
	RetainUntil            time.Time
	LegalHold              bool
	Tags                   map[string]string // nil + TagsErr if unsupported/denied
	TagsErr                error
	ReadOnlyReason         string // non-empty ⇒ UI disables property editing (e.g. SSE-C, KMS without access)
}
type Header struct{ Name, Value string }
func (s *Service) Head(ctx context.Context, loc Location, key, versionID string) (ObjectInfo, error)
func (s *Service) Preview(ctx context.Context, loc Location, key, versionID string, maxBytes int64) (data []byte, truncated bool, err error)
func (s *Service) PresignURL(ctx context.Context, loc Location, key, versionID, method string, ttl time.Duration) (string, error)
func (s *Service) PublicURL(loc Location, key string) string
func (s *Service) CDNURL(loc Location, key string) string // "" without CDN, filtered-out bucket, folder key or unknown account
func (s *Service) HasCDN(loc Location) bool
func (s *Service) CreateFolder(ctx context.Context, loc Location, name string) error // zero-byte "name/" object; rejects "/" inside name
```

## 4. 任务队列（`queue.go`）

```go
type Kind int    // KindUpload, KindDownload, KindOther
type Status int  // Queued, Running, Succeeded, Failed, Canceled, PartiallyFailed
type Task struct {
	ID, GroupID        string // GroupID ties per-file tasks of one user action
	Kind               Kind
	Title, Detail      string // e.g. "Upload report.pdf", "→ s3://bucket/prefix/"
	Status             Status
	BytesTotal         int64
	BytesDone          int64
	Items              int
	ItemsDone          int
	ItemsFailed        int
	Speed              float64 // bytes/s, smoothed
	Created            time.Time
	Started            time.Time
	Finished           time.Time
	Err                string
}
type ItemResult struct {
	Key, VersionID string
	Status         string // "succeeded" | "failed" | "unknown" | "unprocessed" | "skipped"
	Err            string
}
type Event struct{ Task Task; Removed bool }

func (s *Service) Subscribe(fn func(Event)) (unsubscribe func()) // fn is called off the UI thread, throttled to ≤10 events/s per task
func (s *Service) Tasks() []Task
func (s *Service) TaskItems(id string) []ItemResult
func (s *Service) Cancel(id string) error           // queued: removed immediately; running: ctx canceled
func (s *Service) CancelGroup(groupID string) error
func (s *Service) Retry(id string) (string, error)  // new task with failed+unprocessed items only
func (s *Service) ClearFinished()
```

- 三类队列各自 FIFO，提交时登记顺序；`Limits` 下调不打断已运行的任务。
- 一个任务占一个槽。批量服务端操作（删除、复制、改属性等）是一个 Other 任务，内部用 `BulkWorkers` 个并发 worker 处理各项。
- 上传或下载多个文件时展开为每文件一个任务（共享 GroupID），队列额度即并发文件数。扫描文件夹或前缀的过程本身是一个 Other 任务，扫描完再展开。

## 5. 传输（`transfers.go`、`upload_rules.go`、`mime.go`）

```go
type ConflictPolicy string // "skip" | "overwrite" | "rename" | "newer" | "different_size"

type UploadRequest struct {
	Dest       Location
	Paths      []string // local files and/or folders (recursive, folders keep relative structure)
	Conflict   ConflictPolicy
	NoRules    bool
}
func (s *Service) Upload(req UploadRequest) (groupID string, err error)

type DecodeMode string // "" (raw) | "gzip" | "zip" | "auto"
type DownloadRequest struct {
	Source        Location
	Keys          []string     // objects; keys ending "/" are folders (recursive)
	Versions      []ObjectRef  // specific versions; file name gets " (v<short-id>)" suffix
	WholeBucket   bool
	DestDir       string
	Conflict      ConflictPolicy
	Decode        DecodeMode
	PasswordRef   string
	MaxDecoded    int64 // default 1 GiB
}
type ObjectRef struct{ Key, VersionID string }
func (s *Service) Download(req DownloadRequest) (groupID string, err error)
```

上传的不变量：

1. 只上传普通文件；跳过符号链接，并在结果中注明。
2. Content-Type 优先级依次为：ZIP 产物、规则中的 Content-Type、MIME 表、扩展名推断、`application/octet-stream`。
3. 规则在任务开始时求值，可设置 headers、metadata、存储类别、SSE、压缩。GZIP 设置 `Content-Encoding: gzip` 且不改 key；ZIP 与 AES-ZIP 在 key 后追加 `.zip`。压缩产物写入临时文件，任务结束后删除。
4. 冲突策略 skip / rename 优先用 `If-None-Match: *`，provider 不支持时退化为先 HEAD（存在竞态，在文档中说明）。

下载的不变量：

1. 先用 `safepath.Join` 得到目标路径，再 `CreateTemp`。
2. GET 带上列表或 HEAD 得到的 `If-Match` ETag，可选解码。
3. 用 `safepath.Publish` 按冲突策略发布。失败时删除临时文件，绝不发布不完整的文件。

```go
func (s *Service) UploadRules() []rules.UploadRule
func (s *Service) SaveUploadRules([]rules.UploadRule) error
func (s *Service) PreviewUploadRules(bucket, key string) (rules.UploadOptions, error)
func (s *Service) MIMETypes() map[string]string
func (s *Service) SaveMIMETypes(map[string]string) error
func (s *Service) ContentTypeFor(path string) string
```

## 6. 批量与对象操作（`bulk.go`）

```go
type Selection struct {
	Location
	Keys     []string // objects and folders (folder keys end with "/", always recursive)
	Versions []ObjectRef
	All      bool     // everything under Location.Prefix (whole bucket when Prefix == "")
}
type Estimate struct {
	Objects   int
	Bytes     int64
	Truncated bool // stopped counting at 100k objects
}
func (s *Service) Estimate(ctx context.Context, sel Selection) (Estimate, error)

type CopyOptions struct {
	Conflict ConflictPolicy
	Move     bool
}
func (s *Service) Copy(sel Selection, dest Location, opt CopyOptions) (taskID string, err error)
func (s *Service) Rename(ctx context.Context, loc Location, key, newName string) (taskID string, err error) // object or folder
func (s *Service) Delete(sel Selection) (taskID string, err error)
func (s *Service) SetStorageClass(sel Selection, class string) (taskID string, err error)
func (s *Service) SetEncryption(sel Selection, e Encryption) (taskID string, err error)
type Encryption struct {
	Mode     string // "none" | "AES256" | "aws:kms" | "SSE-C"
	KMSKeyID string
	KeyRef   string // SSE-C (HTTPS only)
}

type PropertiesPatch struct {
	SetHeaders  map[string]string
	DelHeaders  []string
	SetMetadata map[string]string
	DelMetadata []string
}
func (s *Service) SetProperties(sel Selection, p PropertiesPatch) (taskID string, err error)
func (s *Service) SetTags(ctx context.Context, loc Location, ref ObjectRef, tags map[string]string) error
func (s *Service) ACL(ctx context.Context, loc Location, ref ObjectRef) (ACL, error)
func (s *Service) SetACL(ctx context.Context, loc Location, ref ObjectRef, acl ACL) error
```

批量与对象操作的不变量：

1. **移动**：先复制，确认成功（HEAD 目标的 ETag 和大小与复制结果一致）后才删源。源在版本化桶中时，按复制时的 VersionId 精确删除；未版本化时按 key 删除，存在竞态，需在文档中说明。
2. **允许同桶操作。** 拒绝目标与源相同、把文件夹移进它自身的情况；目标已存在时遵循冲突策略。
3. **原地重写**（改存储类别、加密、属性）：用 CopyObject 加 `MetadataDirective: REPLACE`，带回所有未修改的 headers 和 metadata；tags 保持（尽力而为）；ACL 尽力保留，桶强制 ownership 时跳过。超过 5 GiB 走分片复制。版本化桶会因此产生新版本，这一点要在文档中写明。
4. **逐项结果**：任务持有 `[]ItemResult`；单项失败不影响其他项。删除走 `storage.DeleteObjects` 对账，结果为 unknown 的项不重试。

## 7. 版本（`versions.go`）

```go
type Version struct {
	Key, VersionID string
	Latest         bool
	DeleteMarker   bool
	Size           int64
	Modified       time.Time
	ETag           string
	StorageClass   string
}
type VersionPage struct {
	Versions                       []Version
	NextKeyMarker, NextVersionMarker string
}
func (s *Service) ListVersions(ctx context.Context, loc Location, keyMarker, versionMarker string) (VersionPage, error)
func (s *Service) RestoreVersion(ctx context.Context, loc Location, ref ObjectRef) error // copy with CopySourceIfMatch → new current; never deletes history
func (s *Service) RestoreDeleted(sel Selection) (taskID string, err error)
// for keys whose latest is a delete marker: copy newest non-marker version as current, If-None-Match:* where supported
func (s *Service) DeleteVersions(loc Location, refs []ObjectRef) (taskID string, err error) // permanent; null versions allowed only for unversioned/suspended semantics with explicit VersionId "null"
```

## 8. 桶管理（`buckets.go`）

```go
func (s *Service) CreateBucket(ctx context.Context, accountID, name, region string, objectLock bool) error
func (s *Service) DeleteBucket(ctx context.Context, accountID, name string) error // must be empty
func (s *Service) EmptyBucket(accountID, name string, allVersions bool) (taskID string, err error)

type BucketConfig string // "versioning" | "encryption" | "policy" | "cors" | "lifecycle" | "logging" | "tagging"
                         // | "website" | "public_access_block" | "ownership" | "object_lock" | "request_payment"
                         // | "accelerate" | "replication"
func (s *Service) GetBucketConfig(ctx context.Context, accountID, bucket string, c BucketConfig) (json string, err error)
func (s *Service) PutBucketConfig(ctx context.Context, accountID, bucket string, c BucketConfig, json string) error
func (s *Service) DeleteBucketConfig(ctx context.Context, accountID, bucket string, c BucketConfig) error
// JSON = SDK output/input struct shape; "not configured" returns ("", nil); 403/NotImplemented → OpError Kind

func (s *Service) MultipartUploads(ctx context.Context, loc Location) ([]MultipartUpload, error)
func (s *Service) AbortMultipartUploads(loc Location, uploads []MultipartUpload) (taskID string, err error)
```

## 9. 同步（`sync.go`）

```go
type SyncJob struct {
	ID, Name   string
	LocalDir   string
	Remote     Location
	Direction  string   // "upload" | "download"
	Mode       string   // "new" | "new_changed" | "mirror" (mirror also deletes extraneous destination items)
	Compare    string   // "size_time" | "size" | "checksum"
	Excludes   []string // glob on relative path
	NoRules    bool
}
func (s *Service) SyncJobs() []SyncJob
func (s *Service) SaveSyncJob(SyncJob) (SyncJob, error)
func (s *Service) DeleteSyncJob(id string) error

type SyncAction struct {
	Op       string // "upload" | "download" | "delete" | "skip"
	Path     string
	Reason   string
	Size     int64
}
type SyncPreview struct {
	Job                                 SyncJob
	Uploads, Downloads, Deletes, Skips  int
	Bytes                               int64
	Actions                             []SyncAction
}
func (s *Service) PreviewSync(ctx context.Context, job SyncJob) (SyncPreview, error)
func (s *Service) RunSync(p SyncPreview) (groupID string, err error)
// executes the previewed actions as normal transfer/delete tasks; each action re-checks its precondition (conditional write / ETag) and reports skipped if the world changed
```

上传方向在对象上写入 `pail-mtime` metadata，供下次比较时间使用（同步还写入 `pail-size`、`pail-sha256`，压缩上传写入 `pail-compression`；旧版 `opens3-*` 键仍会被读取）。checksum 模式用 SHA256：本地文件直接计算，远端读取 metadata `pail-sha256`；远端缺这项 metadata 时，在预览中标为 changed 并说明原因，不用 multipart ETag 冒充哈希。

## 10. CloudFront（`cloudfront.go`）

```go
func (s *Service) Distributions(ctx context.Context, accountID string) ([]cloud.Distribution, error)
func (s *Service) DistributionsForBucket(ctx context.Context, accountID, bucket string) ([]cloud.Distribution, error)
func (s *Service) Invalidate(ctx context.Context, accountID, distID string, paths []string) (cloud.Invalidation, error)
func (s *Service) Invalidation(ctx context.Context, accountID, distID, id string) (cloud.Invalidation, error)
func (s *Service) Invalidations(ctx context.Context, accountID, distID string) ([]cloud.Invalidation, error)
```

## 11. 测试夹具 `internal/s3test`

内存版 S3 httptest 服务器，供 app、UI 和 CLI 测试使用，UI 的 `--demo` 模式也可以用它。覆盖范围：

- 对象：ListBuckets，ListObjectsV2（delimiter 与分页），Get、Put、Head、Delete，DeleteObjects；
- 复制：CopyObject、UploadPartCopy；
- 分片：multipart create / part / complete / abort / list；
- 版本：versioning 与 delete marker，ListObjectVersions；
- 条件请求：If-Match、If-None-Match、CopySourceIfMatch；
- tagging 与 bucket 配置：可配置为 NotImplemented。

故障注入钩子：按请求返回 503、断开连接、缺失 DeleteObjects 确认、延迟。签名不做校验，只检查 Authorization 头是否存在。真实协议兼容性仍以 MinIO 集成套件为准。
