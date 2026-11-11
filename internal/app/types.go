package app

import "time"

// This file holds every exported data type of the app layer (docs/rewrite/APP_API.md
// sections 1-10) so that the packages that build on it (UI, CLI) and the files
// that implement the individual subsystems share one vocabulary.

// ---- service construction and settings ---------------------------------

// Options configures New.
type Options struct {
	ConfigDir string  // default os.UserConfigDir()/Pail
	Keyring   Keyring // nil = the OS keyring (service "Pail")
	Now       func() time.Time
}

// Keyring stores secrets. Implementations may block (OS prompts); the Service
// never calls them while holding the lock that guards its in-memory state.
type Keyring interface {
	Get(key string) (string, error)
	Set(key, value string) error
	Delete(key string) error
}

// Limits are the per-kind queue concurrency limits. Each must be >= 1.
type Limits struct {
	Upload   int `json:"upload"`
	Download int `json:"download"`
	Other    int `json:"other"`
}

// Settings is persisted in settings.json.
type Settings struct {
	Theme       string `json:"theme"`       // "system" | "light" | "dark"
	Limits      Limits `json:"limits"`      // queue slots per kind
	BulkWorkers int    `json:"bulkWorkers"` // workers inside one bulk task
	PartSizeMiB int    `json:"partSizeMiB"` // multipart part size
	// UploadKiBps and DownloadKiBps limit the bandwidth of all transfers, in
	// KiB per second; 0 (the default) means no limit.
	UploadKiBps        int            `json:"uploadKiBps,omitempty"`
	DownloadKiBps      int            `json:"downloadKiBps,omitempty"`
	DefaultDownloadDir string         `json:"defaultDownloadDir,omitempty"`
	Conflict           ConflictPolicy `json:"conflict"` // default for uploads/downloads
	ShowVersions       bool           `json:"showVersions"`
	LogToFile          bool           `json:"logToFile"`
	// SecretBackend is where secrets live: SecretBackendKeyring (default, the
	// OS keychain) or SecretBackendVault. It can only be changed with
	// SetSecretBackend; SaveSettings keeps the current value.
	SecretBackend string `json:"secretBackend"`
	// VaultAutoLockMinutes locks the vault after this many idle minutes
	// (default 15; 0 = never). Unlike the other numeric settings 0 is a real
	// value, so SaveSettings does not replace it by the default.
	VaultAutoLockMinutes int `json:"vaultAutoLockMinutes"`
	// HideTrayIcon removes the desktop app's icon from the notification area
	// (the menu bar on macOS); closing the window then quits. The zero value
	// shows it, so settings saved before it existed show it too.
	HideTrayIcon bool `json:"hideTrayIcon,omitempty"`
	// QuitWhenIdle makes closing the window quit the desktop app when no
	// transfer is running, even with the tray icon showing; with transfers
	// running, closing still hides it so they finish. The zero value keeps the
	// app in the tray.
	QuitWhenIdle bool `json:"quitWhenIdle,omitempty"`
	// Proxy sends the requests of every account (S3, STS and CloudFront)
	// through a proxy server. The zero value follows HTTP_PROXY, HTTPS_PROXY and
	// NO_PROXY from the environment. It can only be changed with SaveProxy (its
	// password lives in the secret store); SaveSettings keeps the current value.
	// The command line reads the same setting.
	Proxy ProxySettings `json:"proxy,omitzero"`
	// RecentOps is how many finished operations the desktop app's list of recent
	// operations keeps (DefaultRecentOps when absent, MinRecentOps to
	// MaxRecentOps). The list lives in memory only and is gone on restart.
	RecentOps int `json:"recentOps"`
	// KeepRecentOps keeps that list when another account is chosen. The zero
	// value clears it, so that the list shows what was done with this account.
	KeepRecentOps bool `json:"keepRecentOps,omitempty"`
	// CollapseTools folds the desktop app's TOOLS group in the sidebar down to
	// its header (the row of an open tool stays). The zero value shows the
	// group, so settings saved before it existed show it too.
	CollapseTools bool `json:"collapseTools,omitempty"`
}

// SecretBackend values.
const (
	SecretBackendKeyring = "keyring"
	SecretBackendVault   = "vault"
)

// maxVaultAutoLockMinutes is one week.
const maxVaultAutoLockMinutes = 7 * 24 * 60

// ---- accounts ------------------------------------------------------------

// Account is one storage account. It never contains secrets.
type Account struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Provider           string `json:"provider"` // "aws" | "s3" (generic compatible)
	Endpoint           string `json:"endpoint,omitempty"`
	Region             string `json:"region,omitempty"`
	PathStyle          bool   `json:"pathStyle,omitempty"`
	Credentials        string `json:"credentials"` // "static" | "profile" | "default" | "anonymous"
	AccessKeyID        string `json:"accessKeyId,omitempty"`
	Profile            string `json:"profile,omitempty"`
	RoleARN            string `json:"roleArn,omitempty"`
	ExternalID         string `json:"externalId,omitempty"`
	RoleSessionName    string `json:"roleSessionName,omitempty"`
	InsecureSkipVerify bool   `json:"insecureSkipVerify,omitempty"`
	ChecksumMode       string `json:"checksumMode,omitempty"` // "auto" (default) | "when_required" | "when_supported"
	// RequesterPays sends x-amz-request-payer: requester with every request,
	// to read and write buckets whose owner bills the requester.
	RequesterPays bool `json:"requesterPays,omitempty"`
	// ProxyMode overrides the proxy of Settings for this account: "" uses it,
	// ProxyDirect connects without a proxy, ProxyCustom uses ProxyURL.
	ProxyMode string `json:"proxyMode,omitempty"`
	// ProxyURL is the account's own proxy, scheme://host:port with the scheme
	// http, https or socks5. It takes no user name or password.
	ProxyURL string `json:"proxyUrl,omitempty"`
	// CDNURL is an optional base URL or template (placeholders {bucket}, {key})
	// under which the account's objects are served by a CDN; CDNBuckets limits it
	// to the listed buckets (empty = all).
	CDNURL     string   `json:"cdnUrl,omitempty"`
	CDNBuckets []string `json:"cdnBuckets,omitempty"`
	// BucketAliases maps a bucket name to the name the app shows for it. The
	// bucket itself is never renamed: requests, links and the command line keep
	// using the real name. Only SetBucketAlias changes it; SaveAccount keeps the
	// saved aliases whatever the caller passes.
	BucketAliases map[string]string `json:"bucketAliases,omitempty"`
	// Buckets, when set, are the buckets the account shows in place of the
	// service's list: Buckets() returns them without a request and TestAccount
	// lists each one's folder instead of calling ListBuckets. It is for
	// credentials that may not list buckets (no s3:ListAllMyBuckets) or that
	// may only see one folder of a bucket. Empty: the service lists them.
	Buckets []AccountBucket `json:"buckets,omitempty"`
	// SecretMissing is derived by Accounts(), never persisted or read back: a
	// static account whose secret is not stored (after ResetVault). The UI asks
	// the user to enter it again; SaveAccount with new secrets clears it.
	SecretMissing bool `json:"-"`
}

// Account.ProxyMode values.
const (
	ProxyGlobal = ""
	ProxyDirect = "direct"
	ProxyCustom = "custom"
)

// Account.Provider values.
const (
	ProviderAWS = "aws"
	ProviderS3  = "s3"
)

// Account.Credentials values.
const (
	CredStatic    = "static"
	CredProfile   = "profile"
	CredDefault   = "default"
	CredAnonymous = "anonymous"
)

// Secrets are the keyring-backed parts of a static credential.
type Secrets struct{ SecretAccessKey, SessionToken string }

// ---- browsing --------------------------------------------------------------

// Location addresses a bucket and a folder inside it.
type Location struct {
	AccountID string `json:"accountId"`
	Bucket    string `json:"bucket"`
	Prefix    string `json:"prefix"` // "" or ends with "/"
}

// Bucket is one entry of Buckets: a ListBuckets result, or a bucket the account
// names (Account.Buckets). Region is empty when the provider does not report
// it, and for named buckets. Prefix is the folder a named bucket opens in ("" =
// the bucket root).
type Bucket struct {
	Name, Region string
	Created      time.Time
	Prefix       string
}

// Entry is one row of a listing.
type Entry struct {
	Key, Name    string // Name = last path segment (folders: without the trailing "/")
	Folder       bool   // Key ends with "/"
	Size         int64
	Modified     time.Time
	StorageClass string
	ETag         string // as returned by the service, quotes included
}

// ListOptions tunes List.
type ListOptions struct {
	Flat    bool // no delimiter: every key below the prefix
	Token   string
	MaxKeys int32 // default 1000
}

// Page is one page of a listing. NextToken is empty on the last page.
type Page struct {
	Entries   []Entry
	NextToken string
}

// Header is one editable standard HTTP header.
type Header struct{ Name, Value string }

// ObjectInfo is the result of Head plus best-effort extras.
type ObjectInfo struct {
	Key, VersionID, ETag string
	Size                 int64
	Modified             time.Time
	StorageClass         string
	ContentType          string
	Headers              []Header          // the 6 editable standard headers, in fixed order
	Metadata             map[string]string // lower-case keys, without x-amz-meta-
	SSE, KMSKeyID        string
	Restore              string // raw x-amz-restore
	LockMode             string
	RetainUntil          time.Time
	LegalHold            bool
	Tags                 map[string]string // nil + TagsErr if unsupported/denied
	TagsErr              error
	ReadOnlyReason       string // non-empty => UI disables property editing (SSE-C, KMS without access)
}

// ---- tasks -------------------------------------------------------------------

// Kind selects the queue a task runs in.
type Kind int

// Task kinds. Each has its own FIFO queue and concurrency limit.
const (
	KindUpload Kind = iota
	KindDownload
	KindOther
)

func (k Kind) String() string {
	switch k {
	case KindUpload:
		return "upload"
	case KindDownload:
		return "download"
	case KindOther:
		return "other"
	}
	return "unknown"
}

// Status is the lifecycle state of a task.
type Status int

// Task statuses. Succeeded, Failed, Canceled and PartiallyFailed are final;
// Paused waits for Resume or Cancel.
const (
	StatusQueued Status = iota
	StatusRunning
	StatusSucceeded
	StatusFailed
	StatusCanceled
	StatusPartiallyFailed
	StatusPaused
)

func (s Status) String() string {
	switch s {
	case StatusQueued:
		return "queued"
	case StatusRunning:
		return "running"
	case StatusSucceeded:
		return "succeeded"
	case StatusFailed:
		return "failed"
	case StatusCanceled:
		return "canceled"
	case StatusPartiallyFailed:
		return "partially failed"
	case StatusPaused:
		return "paused"
	}
	return "unknown"
}

// Finished reports whether s is a final state.
func (s Status) Finished() bool { return s >= StatusSucceeded && s != StatusPaused }

// Task is a snapshot of one queued, running or finished task.
type Task struct {
	ID, GroupID      string // GroupID ties the per-file tasks of one user action
	Kind             Kind
	Title, Detail    string // e.g. "Upload report.pdf", "-> s3://bucket/prefix/"
	Status           Status
	BytesTotal       int64
	BytesDone        int64
	Items            int
	ItemsDone        int // items with a final result other than "unprocessed"
	ItemsFailed      int // "failed" and "unknown" items
	ItemsSkipped     int
	ItemsUnknown     int
	ItemsUnprocessed int
	Speed            float64 // bytes/s, smoothed
	Created          time.Time
	Started          time.Time
	Finished         time.Time
	Err              string
	// Pausable marks a per-file upload or download: Pause and Resume apply.
	Pausable bool
}

// TaskGroupResult totals a group independently of its retained task history.
// Succeeded counts transferred upload/download items; the other fields count tasks.
type TaskGroupResult struct {
	Pending, Succeeded, Failed, Canceled int
}

// ItemResult.Status values.
const (
	ItemSucceeded   = "succeeded"
	ItemFailed      = "failed"
	ItemUnknown     = "unknown" // outcome ambiguous; never replayed automatically
	ItemUnprocessed = "unprocessed"
	ItemSkipped     = "skipped"
)

// ItemResult is the outcome of one object (or file) inside a task.
type ItemResult struct {
	Key, VersionID string
	Status         string // one of the Item* constants
	Err            string
}

// Event is delivered to Subscribe callbacks. Removed means the task was
// dropped from the list (ClearFinished or retention); Task is its last state.
type Event struct {
	Task    Task
	Removed bool
}

// ---- transfers ---------------------------------------------------------------

// ConflictPolicy decides what happens when the destination already exists.
type ConflictPolicy string

// Conflict policies.
const (
	ConflictSkip          ConflictPolicy = "skip"
	ConflictOverwrite     ConflictPolicy = "overwrite"
	ConflictRename        ConflictPolicy = "rename"
	ConflictNewer         ConflictPolicy = "newer"
	ConflictDifferentSize ConflictPolicy = "different_size"
)

// Valid reports whether p is a known policy.
func (p ConflictPolicy) Valid() bool {
	switch p {
	case ConflictSkip, ConflictOverwrite, ConflictRename, ConflictNewer, ConflictDifferentSize:
		return true
	}
	return false
}

// UploadRequest describes one Upload call.
type UploadRequest struct {
	Dest     Location
	Paths    []string // local files and/or folders (recursive, folders keep relative structure)
	Conflict ConflictPolicy
	NoRules  bool
}

// DecodeMode selects client-side decoding of downloaded objects.
type DecodeMode string

// Decode modes.
const (
	DecodeRaw  DecodeMode = ""
	DecodeGzip DecodeMode = "gzip"
	DecodeZip  DecodeMode = "zip"
	DecodeAuto DecodeMode = "auto"
)

// ObjectRef names an object or one specific version of it.
type ObjectRef struct{ Key, VersionID string }

// DownloadRequest describes one Download call.
type DownloadRequest struct {
	Source      Location
	Keys        []string    // objects; keys ending "/" are folders (recursive)
	Versions    []ObjectRef // specific versions; file name gets " (v<short-id>)" suffix
	WholeBucket bool
	DestDir     string
	Conflict    ConflictPolicy
	Decode      DecodeMode
	PasswordRef string
	MaxDecoded  int64 // default 1 GiB
}

// ---- bulk and object operations ---------------------------------------------

// Selection names the objects a bulk operation applies to.
type Selection struct {
	Location
	Keys     []string // objects and folders (folder keys end with "/", always recursive)
	Versions []ObjectRef
	All      bool // everything under Location.Prefix (whole bucket when Prefix == "")
}

// Estimate is shown in confirmation dialogs only.
type Estimate struct {
	Objects   int
	Bytes     int64
	Truncated bool // stopped counting at 100k objects
}

// CopyOptions tunes Copy.
type CopyOptions struct {
	Conflict ConflictPolicy
	Move     bool
}

// Encryption describes the target server-side encryption of SetEncryption.
type Encryption struct {
	Mode     string // "none" | "AES256" | "aws:kms" | "SSE-C"
	KMSKeyID string
	KeyRef   string // SSE-C key reference (HTTPS only)
}

// PropertiesPatch edits headers and metadata of many objects at once.
type PropertiesPatch struct {
	SetHeaders  map[string]string
	DelHeaders  []string
	SetMetadata map[string]string
	DelMetadata []string
}

// Grant is one ACL grant. Type is "CanonicalUser", "Group" or "AmazonCustomerByEmail";
// Grantee is the canonical user ID, group URI or e-mail address accordingly.
type Grant struct {
	Grantee, Type string
	Permission    string // READ | WRITE | READ_ACP | WRITE_ACP | FULL_CONTROL
}

// ACL is an access control list. When Canned is non-empty on SetACL it takes
// precedence over Grants (e.g. "private", "public-read").
type ACL struct {
	Owner  string
	Grants []Grant
	Canned string
}

// ---- versions ------------------------------------------------------------------

// Version is one entry of ListObjectVersions (an object version or a delete marker).
type Version struct {
	Key, VersionID string
	Latest         bool
	DeleteMarker   bool
	Size           int64
	Modified       time.Time
	ETag           string
	StorageClass   string
}

// VersionPage is one page of versions.
type VersionPage struct {
	Versions                         []Version
	NextKeyMarker, NextVersionMarker string
}

// ---- buckets ---------------------------------------------------------------------

// BucketConfig names one configurable bucket sub-resource.
type BucketConfig string

// Bucket configuration kinds.
const (
	ConfigVersioning        BucketConfig = "versioning"
	ConfigEncryption        BucketConfig = "encryption"
	ConfigPolicy            BucketConfig = "policy"
	ConfigCORS              BucketConfig = "cors"
	ConfigLifecycle         BucketConfig = "lifecycle"
	ConfigLogging           BucketConfig = "logging"
	ConfigTagging           BucketConfig = "tagging"
	ConfigWebsite           BucketConfig = "website"
	ConfigPublicAccessBlock BucketConfig = "public_access_block"
	ConfigOwnership         BucketConfig = "ownership"
	ConfigObjectLock        BucketConfig = "object_lock"
	ConfigRequestPayment    BucketConfig = "request_payment"
	ConfigAccelerate        BucketConfig = "accelerate"
	ConfigReplication       BucketConfig = "replication"
)

// MultipartUpload is one unfinished multipart upload.
type MultipartUpload struct {
	Key, UploadID string
	Initiated     time.Time
	StorageClass  string
	Initiator     string // display name or ID of the initiator, when reported
}

// ---- sync ----------------------------------------------------------------------------

// SyncJob is a saved synchronisation job (sync-jobs.json).
type SyncJob struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	LocalDir  string   `json:"localDir"`
	Remote    Location `json:"remote"`
	Direction string   `json:"direction"`          // "upload" | "download"
	Mode      string   `json:"mode"`               // "new" | "new_changed" | "mirror" (mirror also deletes extraneous destination items)
	Compare   string   `json:"compare"`            // "size_time" | "size" | "checksum"
	Excludes  []string `json:"excludes,omitempty"` // glob on relative path
	NoRules   bool     `json:"noRules,omitempty"`
}

// SyncAction is one planned step of a sync. The fields after Size record what
// PreviewSync observed; RunSync re-checks each of them before it acts and
// reports the item as skipped or failed when the world changed.
type SyncAction struct {
	Op     string // "upload" | "download" | "delete" | "skip"
	Path   string // logical relative path, "/"-separated (a skipped directory ends with "/")
	Reason string
	Size   int64 // bytes to transfer (upload: logical size before compression)

	Key         string // physical object key: after upload rules (".zip" suffix) for uploads
	ETag        string // remote ETag observed (upload: "" = the key did not exist, create-only)
	LocalExists bool   // download: the local file existed in the preview
	LocalSize   int64  // local file size observed
	LocalMTime  int64  // local modification time observed, UnixNano
	RemoteTime  int64  // download: modification time to stamp on the file, UnixNano
	SHA256      string // upload in checksum mode: hex SHA-256 of the local file
}

// SyncPreview is the plan PreviewSync returns and RunSync executes.
type SyncPreview struct {
	Job                                SyncJob
	Uploads, Downloads, Deletes, Skips int
	Bytes                              int64
	Actions                            []SyncAction
}
