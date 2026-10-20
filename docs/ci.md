# CI and release process

## Repository ownership

Forgejo (`ssh://git@ssh.via.moe/dejavu/Pail.git`) is the primary repository. Push commits and tags there from Windows. Forgejo mirrors them to [GitHub](https://github.com/DejavuMoe/Pail), where Actions tests the code and publishes release assets. Do not commit or merge source changes on GitHub. The default branch is `master` on both hosts.

Woodpecker deploys only the static website. Its live acceptance is separate from the application release gates; see [site.md](site.md). GitHub Actions never deploys the website.

## Locked tools and dependencies

`mise.toml` pins Go 1.27.2, Node.js 24.19.0 and PowerShell 7.6.7. Run `mise trust` and `mise install go node powershell`; use `mise exec -- <command>` without shell activation. `GOTOOLCHAIN=local` prevents implicit Go upgrades and `GOFLAGS=-mod=readonly` prevents implicit module changes. `go.mod` / `go.sum` pin application dependencies. Node runs small standard-library CI scripts only; the website and application need no npm packages.

Actions uses `setup-go` with `go-version-file: go.mod` and its module/build cache. A regression check keeps its version equal to mise. Jobs needing Node or PowerShell install only those tools with the pinned mise action. Third-party actions are pinned to full commit SHAs, and website containers to image digests. Changing the Go pin also requires updating the website builder image.

Windows owns all source edits and Git writes. For Linux validation use `linux-task.ps1 -Mode build -Project <repo> -Command '...'`; build mirrors are disposable and contain no `.git`. Linux CI runs the same commands directly. Pass workflow paths explicitly to actionlint, which otherwise relies on Git discovery.

## Workflows

| Workflow | Trigger | Required jobs |
| --- | --- | --- |
| `CI` | `master` push or manual | Six native test jobs; Quality; Race; MinIO |
| `CodeQL` | `master` push or manual | Go on Linux, Windows and macOS; site JavaScript; Actions |
| `Release` | `v*` tag push | Exact-commit preflight; three parallel packaging jobs; verified publication |

Tests and CodeQL run in parallel. A later branch push cancels obsolete validation; release runs are not cancelled. Go analysis uses native OS builds to include OS-specific source. JavaScript analysis excludes `.agents/` and `designs/`, which are authoring tools/prototypes rather than the shipped site.

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

## Release sequence

1. Prepare both changelogs and download documentation on `master`. Commit and push to Forgejo; verify that GitHub has the identical SHA.
2. Wait for every required CI and CodeQL job for that SHA. Review CodeQL alerts; a successful analysis upload alone is not a claim that the code has no findings.
3. Run `node scripts/check-release.mjs DejavuMoe/Pail <sha>`. The check rejects missing, skipped, failed, pending, wrong-commit, wrong-branch and changed-attempt results. The same check runs again inside Release.
4. On Windows, create a signed annotated tag, for example `git tag -s v0.1.0 -m "Pail v0.1.0"`, and push only that tag to Forgejo: `git push origin v0.1.0`.
5. Use local `gh run list --repo DejavuMoe/Pail --commit <sha>` and `gh run watch <run-id> --repo DejavuMoe/Pail --exit-status` to monitor the mirrored workflow. Release verifies that the tag is a semantic version and its commit belongs to `master`, then extracts matching English and Chinese changelog sections.
6. Package all six targets, verify the archives, upload them to a draft Release, then publish only after every upload succeeds. A published Release is not overwritten; failed draft uploads can be rerun. Do not move an existing release tag.
7. Download the published archives and `SHA256SUMS`, verify them again, and run native version checks where available. Trigger the `master` website workflow separately after the release is public.

The release workflow needs `actions: read` for preflight and `contents: write` only for publication. No Forgejo write token or website deployment secret is stored in GitHub. Tags originate in Forgejo; GitHub's write permission is for Release metadata/assets.

## Building and checking release archives

```powershell
pwsh ./scripts/release.ps1 -Version 0.1.0
# Optional: -Targets linux/amd64,linux/arm64 -OutDir <directory> -CliOnly
go run ./tools/verify-release -dir build/release/0.1.0 -version 0.1.0
```

`-Version` accepts SemVer without the `v` prefix. `-Targets` accepts only the six supported targets; a comma-separated string works with `pwsh -File`. The default output is `<repo>/build/release/<version>/`; an explicit relative `-OutDir` resolves from the caller's directory. The per-version output and staging folders are replaced, with resolved-path and symlink checks. Run only one packaging script at a time in a checkout. A failed target fails the script; partial output is never publishable.

| File | Contents |
| --- | --- |
| `Pail-v<V>-windows-<arch>.zip` | `pail-desktop-v<V>-windows-<arch>.exe`, `pail-v<V>-windows-<arch>.exe`, license and READMEs |
| `Pail-v<V>-linux-<arch>.tar.gz` | Versioned desktop/CLI binaries, `pail.desktop`, `pail.png`, license and READMEs |
| `Pail-v<V>-darwin-<arch>.zip` | `Pail.app/Contents/MacOS/pail-desktop-v<V>-darwin-<arch>`, matching `CFBundleExecutable`, versioned CLI, license and READMEs |
| `SHA256SUMS` | Exactly one SHA256 for every archive |

Each archive has a top-level directory matching its filename without the extension. `tools/pack` preserves executable mode 0755 even when packaging on Windows. `tools/verify-release` rejects missing/unexpected files, incorrect checksums, unsafe archive entries, wrong executable permissions, wrong GOOS/GOARCH, cgo-enabled binaries, incorrect executable packages and broken platform launch entries. For a subset build, pass the same `-targets` list to the verifier. `-CliOnly` is a local convenience and is not accepted as a complete release by the verifier.

The scripts restore process environment and working directory. Generated icons and Windows resources are committed; regenerate them with `go run ./tools/genicons`. Archives are cross-compiled with `CGO_ENABLED=0`, `-trimpath`, `-s -w` and `-X main.version=<version>`. Windows GUI adds `-H windowsgui`. Binaries have no developer signature; macOS is not notarized. Byte-for-byte reproducible builds are not claimed.

## Integration and manual-only checks

`bash scripts/integration-minio.sh` builds the pinned MinIO fixture, binds it only to localhost, uses generated credentials and tmpfs data, and removes only its owned container. `PAIL_IT_MINIO_IMAGE=local:<image>` uses a previously loaded local image and fails if missing; the CI Buildx step uses this to avoid rebuilding or pulling the fixture twice. `-MinioImage latest` in the PowerShell wrapper remains an explicit compatibility check, not a moving CI dependency. See [integration/README.md](../integration/README.md) for all inputs.

Plain `go test ./...` skips integration tests unless their endpoint variables are present. Never provide `PAIL_EXT_*` to ordinary CI: doing so enables the real external suite. `scripts/test-external.ps1` is manual only, scoped to the authorized endpoint and a unique `pail-e2e/` prefix. It must never run with fork credentials or automatically after a push.

`pail-desktop --demo --smoke-exit 5s --smoke-screenshot out.png` opens an actual window and needs a desktop session. Snapshot, API, protocol, native-runtime and real-server results are recorded separately in [status.md](status.md).
