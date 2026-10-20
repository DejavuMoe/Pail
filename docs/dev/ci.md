# CI and release process

## Repository ownership

Forgejo (`ssh://git@ssh.via.moe/dejavu/Pail.git`) is the primary repository. Push commits and tags there from Windows. Forgejo mirrors them to [GitHub](https://github.com/DejavuMoe/Pail), where Actions tests the code and publishes release assets. Do not commit or merge source changes on GitHub. The default branch is `master` on both hosts.

Woodpecker deploys only the static website. Its live acceptance is separate from the application release gates; see [site.md](site.md). GitHub Actions never deploys the website.

`.gitattributes` marks `.agents/skills/**` and `designs/pail-site/**` as `linguist-vendored` to exclude authoring tools and prototypes from GitHub/Forgejo language statistics. Application and shipped website sources remain counted. Hosts recompute their statistics after a push; local `git check-attr` verifies the rules, not the remote refresh.

## Locked tools and dependencies

`mise.toml` pins Go 1.27.2 and Node.js 24.19.0. Run `mise trust` and `mise install go node`; use `mise exec -- <command>` without shell activation. `GOTOOLCHAIN=local` prevents implicit Go upgrades and `GOFLAGS=-mod=readonly` prevents implicit module changes. `go.mod` / `go.sum` pin application dependencies. Node runs small standard-library CI scripts only; the website and application need no npm packages.

Actions uses `setup-go` with `go-version-file: go.mod` and its module/build cache. A regression check keeps its version equal to mise. Jobs needing Node install it with the pinned mise action. Application builds, tests and release packaging use Go directly on every host; no CI job installs or invokes PowerShell. `scripts/release.ps1` is an optional compatibility wrapper that forwards old parameters to the Go release tool. Third-party actions are pinned to full commit SHAs, and website containers to image digests. Changing the Go pin also requires updating the website builder image.

Windows owns all source edits and Git writes. For Linux validation use `linux-task.ps1 -Mode build -Project <repo> -Command '...'`; build mirrors are disposable and contain no `.git`. Linux CI runs the same commands directly. Pass workflow paths explicitly to actionlint, which otherwise relies on Git discovery.

## Shared local and CI commands

Run these from the repository root in PowerShell, Bash or another shell; the commands themselves do not depend on the shell.

| Purpose | Command | Runtime requirements |
| --- | --- | --- |
| Static checks | `go vet ./...` | Go on the current host |
| Native tests | `go test ./...` | Go on the current host; protocol fixtures are in-process |
| Native build | `go build -o build/ ./cmd/pail ./cmd/pail-desktop` | Go; application builds do not require cgo |
| All release archives | `go run ./tools/release -version 0.2.0` | Go; includes archive verification |
| One target | `go run ./tools/release -version 0.2.0 -targets linux/amd64` | Same tool and rules as a full release |
| Downloaded archive verification | `go run ./tools/verify-release -dir build/release/0.2.0 -version 0.2.0` | Go |
| Race detection | `go test -race ./...` | Go, cgo and a C compiler; Linux in CI |
| MinIO integration | `bash scripts/integration-minio.sh` | Linux, Bash, Docker, curl and Go; Windows uses the optional WSL wrapper |

CI keeps six native OS/architecture test jobs because cross-compiling a binary does not test platform-specific runtime behavior. Packaging uses three Linux jobs, one per target OS with both architectures, sharing the same Go tool. This avoids six separate packaging implementations and repeated native runner setup. Node is used for CI/release metadata checks, not application builds. Website deployment remains a Linux shell operation. Real external endpoints and native GUI interaction remain manual-only checks.

## Workflows

| Workflow | Trigger | Required jobs |
| --- | --- | --- |
| `CI` | `master` push or manual | Six native test jobs; Quality; Race; MinIO |
| `CodeQL` | `master` push, manual, or Wednesday 03:23 UTC | Go on Linux, Windows and macOS; site JavaScript; Actions |
| `Release` | `v*` tag push | Exact-commit preflight; three parallel packaging jobs; verified publication |

Tests and CodeQL run in parallel. A later branch push cancels obsolete validation; release runs are not cancelled. Go analysis uses native OS builds to include OS-specific source. JavaScript analysis excludes `.agents/` and `designs/`, which are authoring tools/prototypes rather than the shipped site.

The weekly CodeQL scan picks up new analysis rules without a source push. Scheduled runs have a separate concurrency group and do not replace the push/manual runs required for release. GitHub may delay scheduled runs and disables schedules in public repositories after 60 days without repository activity; check Actions when resuming an inactive project.

| Target | Native test runner | Release archive |
| --- | --- | --- |
| windows/amd64 | windows-2025 | ZIP |
| windows/arm64 | windows-11-arm | ZIP |
| linux/amd64 | ubuntu-24.04 | tar.gz |
| linux/arm64 | ubuntu-24.04-arm | tar.gz |
| darwin/amd64 | macos-15-intel | ZIP with Pail.app |
| darwin/arm64 | macos-15 | ZIP with Pail.app |

Each native job checks its actual GOOS/GOARCH, runs `go vet ./...`, `go test -count=1 -timeout 10m ./...`, builds both commands without cgo and executes their version commands. UI unit tests are headless. They do not prove an interactive GUI, GPU rendering or working system keychain on a user's desktop.

Quality checks formatting, workflow syntax, release-gate tests, deployment fixtures and known Go vulnerabilities. Race runs `go test -race -count=1 -timeout 15m ./...` with cgo and a C compiler on Linux. MinIO uses the existing isolated integration suite, loading a fixed-source image built with the GitHub Actions Buildx cache. No real endpoint credentials are supplied to CI.

Security triage for v0.1.0: Goldmark was upgraded to v1.7.17 for [GO-2026-5320](https://pkg.go.dev/vuln/GO-2026-5320). Govulncheck also reports [GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932) at module level: it concerns `golang.org/x/crypto/openpgp`, which Pail does not import. A passing scan means no vulnerable imported packages or reachable symbols were found, not that every package in every required module is vulnerability-free.

CodeQL alerts `go/weak-sensitive-data-hashing` in `internal/app/upload_rules.go` and `internal/app/rewrite.go` were reviewed as protocol-related false positives. Both compute the SSE-C `SSECustomerKeyMD5` header specified by the [S3 API](https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutObject.html#API_PutObject_RequestSyntax); both paths require HTTPS before reading or sending the 32-byte customer key. The MD5 is a transfer checksum, not password storage, key derivation or the encryption algorithm (AES256). Replacing it with SHA-256 would break the protocol. Keep the CodeQL rule enabled; review each new occurrence separately. This source review does not claim real AWS SSE-C acceptance.

## Security alerts and reporting

The GitHub repository has dependency graph, Dependabot vulnerability and malware alerts, private vulnerability reporting, secret scanning and push protection enabled. Report suspected vulnerabilities using [SECURITY.md](../../SECURITY.md). These repository settings live on GitHub, outside Git; check them again if the repository is recreated.

Maintainers should subscribe to **Watch > Custom > Security alerts** and enable GitHub/email delivery in their personal notification settings. Dependabot's new-vulnerability notifications and digest have separate account preferences. Enabling a repository feature alone does not prove email delivery; GitHub's immediate Dependabot notifications focus on new high/critical advisories, so also review the alerts dashboard and digest.

Dependabot security/version update PRs and agentic autofix remain disabled: fixes must originate in Forgejo, pass CI and CodeQL, and mirror back to GitHub. The existing low-impact development-dependency triage preset remains enabled; include dismissed alerts when reviewing coverage. Review applicability before dismissing an alert and record the reason. GitHub push protection guards the mirror's incoming pushes, not the initial Forgejo push; a blocked mirror requires addressing the leaked credential and synchronization failure at the source.

## Release sequence

1. Prepare both changelogs and download documentation on `master`. Commit and push to Forgejo; verify that GitHub has the identical SHA.
2. Wait for every required CI and CodeQL job for that SHA. Review CodeQL alerts; a successful analysis upload alone is not a claim that the code has no findings.
3. Run `node scripts/check-release.mjs DejavuMoe/Pail <sha>`. The check rejects missing, skipped, failed, pending, wrong-commit, wrong-branch and changed-attempt results. The same check runs again inside Release.
4. On Windows, create a signed annotated tag, for example `git tag -s v0.2.0 -m "Pail v0.2.0"`, and push only that tag to Forgejo: `git push origin v0.2.0`.
5. Use local `gh run list --repo DejavuMoe/Pail --commit <sha>` and `gh run watch <run-id> --repo DejavuMoe/Pail --exit-status` to monitor the mirrored workflow. Release verifies that the tag is a semantic version and its commit belongs to `master`, then extracts matching English and Chinese changelog sections.
6. Package all six targets, verify the archives, upload them to a draft Release, then publish only after every upload succeeds. A published Release is not overwritten; failed draft uploads can be rerun. Do not move an existing release tag.
7. Download the published archives and `SHA256SUMS`, verify them again, and run native version checks where available. Trigger the `master` website workflow separately after the release is public.

The release workflow needs `actions: read` for preflight and `contents: write` only for publication. No Forgejo write token or website deployment secret is stored in GitHub. Tags originate in Forgejo; GitHub's write permission is for Release metadata/assets.

## Building and checking release archives

```sh
go run ./tools/release -version 0.2.0
# Optional: -targets linux/amd64,linux/arm64 -out-dir <directory> -cli-only
go run ./tools/verify-release -dir build/release/0.2.0 -version 0.2.0
```

`-version` is required and accepts SemVer without the `v` prefix. `-targets` accepts a nonempty comma-separated subset of the six supported targets without duplicates. The repository root is discovered from the current directory or its parents, without Git metadata. The default output is `<repo>/build/release/<version>/`; an explicit relative `-out-dir` resolves from the caller's directory. Temporary staging is created under the output parent and removed on completion or ordinary errors. Existing release output is replaced only after all selected targets build and pass verification; linked outputs and unrelated files are refused. Run only one release build for a given output/version at a time. Interruption or filesystem failure during final replacement can require a rerun; atomic replacement is not claimed.

The optional `scripts/release.ps1` wrapper retains `-Version`, `-Targets`, `-OutDir` and `-CliOnly` for existing callers, and requires PowerShell 7.3+. It has no build, platform or archive logic of its own and is not used by CI.

| File | Contents |
| --- | --- |
| `Pail-v<V>-windows-<arch>.zip` | `pail-desktop-v<V>-windows-<arch>.exe`, `pail-v<V>-windows-<arch>.exe`, license |
| `Pail-v<V>-linux-<arch>.tar.gz` | Versioned desktop/CLI binaries, `pail.desktop`, `pail.png`, license |
| `Pail-v<V>-darwin-<arch>.zip` | `Pail.app/Contents/MacOS/pail-desktop-v<V>-darwin-<arch>`, matching `CFBundleExecutable`, versioned CLI, license |
| `SHA256SUMS` | Exactly one SHA256 for every archive |

Each archive has a top-level directory matching its filename without the extension. `tools/release` invokes the existing `tools/pack` command, which preserves executable mode 0755 even when packaging on Windows. `tools/verify-release` rejects missing/unexpected files, incorrect checksums, unsafe archive entries, wrong executable permissions, wrong GOOS/GOARCH, cgo-enabled binaries, incorrect executable packages and broken platform launch entries. The release tool invokes it automatically with the selected targets; when independently verifying a subset, pass the same `-targets` list. `-cli-only` produces local CLI-only archives without desktop launch assets, explicitly skips complete-release verification, and must not be published as a full release.

The Go tool supplies target environment variables only to child processes; it never changes the caller's environment or working directory. The PowerShell wrapper restores its working directory. Generated icons and Windows resources are committed; regenerate them with `go run ./tools/genicons`. Archives are cross-compiled with `CGO_ENABLED=0`, `-trimpath`, `-s -w` and `-X main.version=<version>`. Windows GUI adds `-H windowsgui`. Binaries have no developer signature; macOS is not notarized. Byte-for-byte reproducible builds are not claimed.

## Integration and manual-only checks

`bash scripts/integration-minio.sh` builds the pinned MinIO fixture, binds it only to localhost, uses generated credentials and tmpfs data, and removes only its owned container. `PAIL_IT_MINIO_IMAGE=local:<image>` uses a previously loaded local image and fails if missing; the CI Buildx step uses this to avoid rebuilding or pulling the fixture twice. `-MinioImage latest` in the PowerShell wrapper remains an explicit compatibility check, not a moving CI dependency. See [integration/README.md](../../integration/README.md) for all inputs.

Plain `go test ./...` skips integration tests unless their endpoint variables are present. Never provide `PAIL_EXT_*` to ordinary CI: doing so enables the real external suite. `scripts/test-external.ps1` is manual only, scoped to the authorized endpoint and a unique `pail-e2e/` prefix. It must never run with fork credentials or automatically after a push.

`pail-desktop --demo --smoke-exit 5s --smoke-screenshot out.png` opens an actual window and needs a desktop session. Snapshot, API, protocol, native-runtime and real-server results are recorded separately in [status.md](status.md).
