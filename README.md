# Pail

English | [简体中文](README.zh-CN.md)

Pail is a cross-platform desktop manager for S3-compatible object storage: Amazon S3, MinIO, Cloudflare R2, VersityGW, Wasabi and other services that speak the S3 API. It is written in Go with the [mygo](https://github.com/egoist/mygo) UI toolkit, runs on Windows, Linux and macOS, and comes with a command-line companion, `pail`.

Amazon S3 is a trademark of Amazon.com, Inc. Pail is not affiliated with, endorsed by or sponsored by AWS.

![Pail, light theme](docs/images/main-light.png)
![Pail, dark theme](docs/images/main-dark.png)

## Features

**Browsing**
- Unlimited accounts for AWS and S3-compatible providers (custom endpoint, region, path-style, access keys, AWS profile, default credential chain, anonymous, AssumeRole); connection test.
- Bucket list, breadcrumbs, sortable multi-select object table with automatic paging, search, flat (prefix-less) view, inspector panel.
- Preview of JSON, CSV, XML and other text, images and hex, limited to a safe size.
- Copy key, URI, URL and presigned URL.

**Transfers**
- Upload and download files, folders, versions and whole buckets; drag and drop.
- Separate upload, download and other queues with configurable concurrency, progress, speed, cancel and retry.
- Conflict policies: skip, overwrite, rename, newer, different size.
- Multipart transfers, per-account checksum modes (auto, when required, when supported), and create-only writes so a concurrent writer is never overwritten.
- Downloads go to a temporary file and are published only after verification.

**Object properties**
- Edit headers, metadata, tags, ACL, storage class and server-side encryption (SSE-S3, SSE-KMS, SSE-C); MIME type table.
- Edits keep every property you did not change, and the result is verified afterwards. Multi-selection asks for confirmation of the scope.

**Versions**
- List versions and delete markers, download several at once, restore an old version or a deleted key, permanently delete chosen versions.

**Buckets**
- Create (region, object lock), delete, empty, copy or move the contents of a whole bucket, apply a storage class or encryption to a whole bucket.
- 14 bucket settings (versioning, encryption, policy, CORS, lifecycle, logging, tagging, website, public access block, ownership, object lock, requester pays, transfer acceleration, replication) as forms or JSON. A provider that lacks one is reported as unsupported, not as "not configured".
- Incomplete multipart uploads: list and abort.

**Sync jobs**
- Saved jobs: upload, download or mirror; compare by size and time, size, or checksum; exclude globs; preview first. Execution performs only previewed actions and re-checks each precondition. Local deletions go to the OS trash.

**Upload rules and compression**
- Rules by bucket and key pattern set headers, storage class, encryption and compression: GZIP, ZIP or AES-256 ZIP, with a compression level. Compressed downloads can be decoded on the way down.

**CDN, presigned URLs, CloudFront**
- Optional CDN domain per account (base URL or `{bucket}`/`{key}` template): copy or open an object's CDN link, offered right after an upload.
- Presigned URLs for GET or PUT, with expiry, a chosen version and several objects at once.
- CloudFront (AWS accounts only): list distributions, invalidate paths, a whole bucket's distributions, view invalidations.

**Security**
- Secret keys and passwords live in the OS keychain, or in an optional encrypted vault (Argon2id key derivation, XChaCha20-Poly1305), never in config files.
- Destructive actions show their scope first; deleting a bucket requires typing its name. Optional diagnostic log with secrets redacted.

**Interface**
- Command palette (`Ctrl+K`, `⌘K` on macOS), context menus, keyboard shortcuts.
- English and 简体中文, light and dark themes, following the system by default.

## Install

Release archives are built by `scripts/release.ps1` into `build/release/<version>/` together with a `SHA256SUMS` file. Each holds the `pail-desktop` and `pail` executables, `LICENSE` and the README.

| OS | Archive |
| --- | --- |
| Windows | `Pail-<version>-windows-amd64.zip`, `Pail-<version>-windows-arm64.zip` |
| Linux | `Pail-<version>-linux-amd64.tar.gz`, `Pail-<version>-linux-arm64.tar.gz` (with `pail.desktop` and `pail.png`) |
| macOS | `Pail-<version>-darwin-amd64.zip`, `Pail-<version>-darwin-arm64.zip` (contains `Pail.app`) |

Binaries are not code-signed. The macOS app is also not notarized, so Gatekeeper blocks it until you override it manually. **Pail has never been run on real macOS hardware**; the macOS archives are cross-compiled only.

## Quick start

1. Start `pail-desktop`, open the account switcher in the sidebar and add an account. For a self-hosted or third-party service, for example:
   - Endpoint `https://s3.example.com`, region `us-east-1`, access key ID and secret key;
   - turn on **Path-style addressing** (MinIO and most self-hosted gateways need it; keep it off for AWS);
   - optionally set a **CDN URL** such as `https://cdn.example.com`.

   Press "Test connection", then save. The secret key goes to the OS keychain.
2. Pick a bucket in the sidebar and browse. Drag files in to upload; select objects and press Download.
3. To try it without credentials, run `pail-desktop --demo`: it talks to an in-memory S3 server inside the process, nothing leaves your machine and nothing is saved.

Other flags: `--config-dir DIR`, `--theme system|light|dark`, `--size WxH`, `--tool sync|cloudfront|rules|mime|multipart|settings`, `--version`.

## Command line

`pail` shares accounts and sync jobs with the desktop app:

```powershell
pail accounts
pail ls s3://my-bucket/photos/ -r
pail cp ./dist s3://my-bucket/site/ -r --conflict newer
pail sync ./photos s3://my-bucket/photos/ --mode new_changed --dry-run
pail presign s3://my-bucket/report.pdf --expires 1h
```

Commands: `accounts`, `buckets`, `ls`, `cp`, `mv`, `rm`, `mb`, `rb`, `presign`, `url`, `versions`, `restore`, `sync`, `vault status`. JSON output, exit codes and the encrypted vault are described in [docs/cli.md](docs/cli.md).

## Build from source

Requires Go 1.27.1 (see `go.mod`). No cgo is needed.

```powershell
go build ./cmd/pail-desktop ./cmd/pail
./scripts/release.ps1 -Version 0.1.0   # PowerShell 7.3+, cross-compiles every target
go run ./tools/genicons                # regenerate icons from assets/brand/pail-icon.svg
```

`scripts/release.ps1` expects the generated icons in `assets/brand/generated`. A target that fails to build is skipped and listed in `NOT-BUILT.txt`.

## Testing

```powershell
go vet ./...
go test ./...
```

These use an in-memory S3 server (`internal/s3test`) and `httptest`, so they exercise the protocol but not a real provider.

Against a real MinIO in Docker (through WSL):

```powershell
./scripts/test-integration.ps1                      # pinned MinIO build
./scripts/test-integration.ps1 -MinioImage latest   # newest upstream release, built from source
```

Against any real S3-compatible endpoint you own, with credentials passed only through `PAIL_EXT_*` environment variables (`PAIL_EXT_ENDPOINT`, `PAIL_EXT_ACCESS_KEY`, `PAIL_EXT_SECRET_KEY`, optionally `PAIL_EXT_BUCKET`, `PAIL_EXT_REGION`, `PAIL_EXT_PATH_STYLE`, `PAIL_EXT_CDN`):

```powershell
./scripts/test-external.ps1
```

The suite writes only below a unique `pail-e2e/<run>/` prefix, removes it afterwards, creates and deletes no buckets and changes no bucket settings. Details are in [integration/README.md](integration/README.md).

## Data and privacy

- **Configuration**: the `Pail` folder in the OS user config directory (Windows `%AppData%\Pail`, Linux `~/.config/Pail`, macOS `~/Library/Application Support/Pail`), or the path given with `--config-dir`. It holds the settings, the accounts file, sync jobs, upload rules and the names of secret references. An existing `openS3` folder from an earlier name is migrated automatically once.
- **Secrets**: secret access keys, session tokens and referenced passwords go to the OS keychain (Windows Credential Manager, macOS Keychain, Secret Service on Linux) under the service name `Pail`, or into the encrypted vault `vault.json` if you choose one (Settings > Security; useful where no keychain exists). The access key ID is not secret and is stored in the accounts file.
- **Logs**: off by default. When enabled, a redacted log is written to `logs/pail.log` in the configuration directory. URL query strings, Authorization headers, signatures and known secret values are removed; hand-typed passwords can be recognised only by label, so review a log before sharing it.
- **Network**: Pail talks only to the endpoints of the accounts you configure. There is no telemetry and no update check.

## Platform status

Tested on Windows and on Linux under WSLg. macOS builds are cross-compiled only and have never run on real hardware. Real-server tests so far: MinIO (two releases) and a self-hosted VersityGW behind Cloudflare. AWS itself, including STS and CloudFront, has not been tested. Per-feature results are in [docs/status.md](docs/status.md); the comparison with CS Browser Pro is in [docs/feature-matrix.md](docs/feature-matrix.md).

## Documentation

[docs/cli.md](docs/cli.md) · [docs/architecture.md](docs/architecture.md) · [docs/app-api.md](docs/app-api.md) · [docs/design.md](docs/design.md) · [docs/status.md](docs/status.md) · [docs/ci.md](docs/ci.md) · [docs/site.md](docs/site.md) · [CHANGELOG.md](CHANGELOG.md)

## License

Pail is released under the [MIT License](LICENSE), © 2026 Dejavu Moe. Bundled third-party assets: [Lucide](https://lucide.dev) icons (ISC) and the Inter font (SIL Open Font License 1.1, `cmd/pail-desktop/fonts/OFL.txt`).

Made by [Dejavu Moe](https://dejavu.moe) · https://github.com/DejavuMoe/Pail
