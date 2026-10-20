<p align="center">
  <img src="assets/brand/pail-icon.svg" width="80" height="80" alt="">
</p>

<h1 align="center">Pail</h1>

<p align="center">
  A desktop client for Amazon S3, Cloudflare R2, MinIO and other S3-compatible storage.<br>
  Windows, macOS and Linux. Free and open source.
</p>

<p align="center">
  <a href="https://pail.download">Website</a> ·
  <a href="https://github.com/DejavuMoe/Pail/releases/latest">Download</a> ·
  <a href="https://pail.download/docs/">Documentation</a> ·
  <a href="README.zh-CN.md">简体中文</a>
</p>

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/images/main-dark.webp">
  <img src="docs/images/main-light.webp" alt="The Pail main window: bucket list, object table and inspector">
</picture>

## Features

- **Accounts.** Access keys, AWS profiles, the default credential chain, AssumeRole or anonymous access. For services other than AWS, set the endpoint, region and path-style addressing, and test the connection before saving.
- **Browsing.** A sortable, multi-select object table that pages automatically, with breadcrumbs, a filter, a flat view and an inspector. Preview text files (JSON, CSV, XML and others), images and hex. Copy keys, URIs, URLs and presigned URLs.
- **Transfers.** Upload and download files, folders, versions and whole buckets, or drag files into the window. Uploads and downloads have their own queues with adjustable concurrency, progress, speed, cancel and retry. When a file already exists: skip, overwrite, keep both, overwrite if newer, or overwrite if the size differs. Large files are uploaded in parts with checksums; downloads are written to a temporary file and moved into place after they are verified.
- **Object properties.** Headers, metadata, tags, ACL, storage class and server-side encryption (SSE-S3, SSE-KMS, SSE-C) for one object or a selection. Properties you do not change are kept.
- **Versions.** List versions and delete markers, download several versions at once, restore an older version or a deleted key, and permanently delete the versions you select.
- **Buckets.** Create buckets (with region and object lock), empty and delete them, and copy or move a bucket's contents. Edit 14 bucket settings as a form or as JSON: versioning, encryption, policy, CORS, lifecycle, logging, tags, static website, public access block, ownership, object lock, requester pays, transfer acceleration and replication. List and abort incomplete multipart uploads.
- **Sync jobs.** Save jobs that upload, download or mirror a folder. Compare by size and time, size or checksum, exclude files by pattern, and review the plan before it runs.
- **Upload rules.** Match by bucket and key pattern to set headers, storage class and encryption, or to compress with GZIP, ZIP or AES-256 ZIP. Compressed objects can be decoded on download.
- **Links.** Presigned GET and PUT URLs with an expiry, for a chosen version or for many objects at once. An optional CDN domain per account, with the CDN link offered after each upload. CloudFront invalidations for AWS accounts.
- **Interface.** Command palette (`Ctrl+K`, or `⌘K` on macOS) and keyboard shortcuts. English and Simplified Chinese, light and dark themes.

## Install

Download the archive for your system from the [latest release](https://github.com/DejavuMoe/Pail/releases/latest). The same files are mirrored at [pan.zsh.moe/Pail](https://pan.zsh.moe/Pail/).

- **Windows:** `Pail-v<version>-windows-amd64.zip` (x64) or `Pail-v<version>-windows-arm64.zip` (Arm)
- **macOS 11 or later:** `Pail-v<version>-darwin-arm64.zip` (Apple silicon) or `Pail-v<version>-darwin-amd64.zip` (Intel)
- **Linux:** `Pail-v<version>-linux-amd64.tar.gz` (x64) or `Pail-v<version>-linux-arm64.tar.gz` (Arm)

Each archive holds the desktop app and the `pail` command-line tool. The macOS archive holds `Pail.app`; the Linux archive also has a `.desktop` entry and an icon. There are no 32-bit builds.

Executable names include the version and platform, for example `pail-v0.2.0-windows-amd64.exe`. The examples in this document use the short names `pail-desktop` and `pail`: rename the files or create aliases when you install them. The Linux `.desktop` entry starts the versioned desktop executable from your `PATH`.

### Check the download

Compare the archive's SHA-256 hash with its line in `SHA256SUMS` from the same release:

```sh
sha256sum Pail-v0.2.0-linux-amd64.tar.gz           # Linux
shasum -a 256 Pail-v0.2.0-darwin-arm64.zip         # macOS
Get-FileHash Pail-v0.2.0-windows-amd64.zip         # Windows PowerShell
```

### First launch on macOS

The app is not signed or notarized, so macOS refuses to open a downloaded copy. Unzip the archive, move `Pail.app` to Applications, and clear the quarantine flag once:

```sh
xattr -dr com.apple.quarantine /Applications/Pail.app
```

### First launch on Windows

The executables are not signed. If SmartScreen says "Windows protected your PC", choose **More info**, then **Run anyway**.

## Quick start

1. Start `pail-desktop`. Open the account menu at the top of the sidebar and add an account. For a self-hosted or third-party service, enter:
   - the endpoint, such as `https://s3.example.com`, and the region, such as `us-east-1`;
   - the access key ID and secret access key;
   - **Path-style addressing**: on for MinIO and most self-hosted gateways, off for AWS;
   - optionally a **CDN URL**, such as `https://cdn.example.com`.

   Choose **Test connection**, then save. The secret key is stored in the system keychain.
2. Pick a bucket in the sidebar. Drag files into the window to upload them; select objects and choose **Download** to save them.
3. To try Pail without an account, run `pail-desktop --demo`. It starts an in-memory S3 server inside the app; nothing is sent anywhere and nothing is saved.

`pail-desktop` also accepts these flags:

| Flag | Effect |
| --- | --- |
| `--config-dir` | Use another configuration directory. |
| `--theme` | `system`, `light` or `dark`. |
| `--size` | Initial window size, such as `1280x800`. |
| `--tool` | Open a tool at start: `sync`, `cloudfront`, `rules`, `mime`, `multipart` or `settings`. |
| `--version` | Print the version and exit. |

## Command line

`pail` uses the accounts and saved sync jobs of the desktop app. Add accounts in the desktop app first.

```sh
pail accounts
pail ls s3://my-bucket/photos/ -r
pail cp ./dist s3://my-bucket/site/ -r --conflict newer
pail sync ./photos s3://my-bucket/photos/ --dry-run
pail presign s3://my-bucket/report.pdf --expires 1h
```

Every command, its flags, JSON output and exit codes are in the [command-line reference](docs/cli.md).

## Data and privacy

- **Configuration** lives in the `Pail` folder of your user configuration directory: `%AppData%\Pail` on Windows, `~/Library/Application Support/Pail` on macOS, `~/.config/Pail` on Linux, or the directory given with `--config-dir`. It holds settings, accounts, sync jobs, upload rules and the names of stored secrets. A folder from Pail's earlier name, `openS3`, is migrated once on first start.
- **Secrets** (secret access keys, session tokens and saved passwords) go to the system keychain under the service name `Pail`: Windows Credential Manager, macOS Keychain or the Secret Service on Linux. Where there is no keychain, choose the encrypted vault `vault.json` in **Settings › Security**. Access key IDs are not secret and are stored with the accounts.
- **Logs** are off by default. When you turn them on, Pail writes `logs/pail.log` in the configuration directory with URL query strings, Authorization headers, signatures and known secret values removed. Passwords you type by hand are recognised only by their label, so read a log before you share it.
- **Network:** Pail connects only to the endpoints of the accounts you add. It has no telemetry and does not check for updates.

## Tested platforms and services

- The desktop app has been run by hand on Windows (x64), Linux (x64) and macOS (Apple silicon and Intel). The Arm builds for Windows and Linux have not been tried by hand yet.
- Tested services: Amazon S3, Cloudflare R2, Wasabi, MinIO and VersityGW.
- Not yet tested on a real AWS account: AssumeRole credentials and CloudFront.

Known limitations are listed in the [changelog](CHANGELOG.md).

## Build from source

You need Go 1.27.2. No C compiler is required.

```sh
go build -o build/ ./cmd/pail-desktop ./cmd/pail
```

Tests, release packaging and the project layout are described in [CONTRIBUTING.md](CONTRIBUTING.md).

## License

Pail is released under the [MIT License](LICENSE). It bundles [Lucide](https://lucide.dev) icons (ISC License) and the Inter font (SIL Open Font License 1.1).

Amazon S3 is a trademark of Amazon.com, Inc. Pail is not affiliated with, endorsed by or sponsored by AWS.
