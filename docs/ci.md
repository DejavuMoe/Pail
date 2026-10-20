# CI handoff

Facts and constraints for whoever designs Pail's CI. This is not a finished pipeline: it lists what exists, what each command needs and produces, and what must never run unattended.

## 1. Repository topology

| Place | Role |
| --- | --- |
| Forgejo `ssh://git@ssh.via.moe/dejavu/Pail.git` | Primary repository. All pushes, branches and tags originate here. |
| GitHub `github.com/DejavuMoe/Pail` | Push mirror of Forgejo. Treat it as read-only: anything committed or merged only on GitHub is not in the primary and can be overwritten by the next mirror push. |
| GitHub Actions | Planned home for test and release CI (not written yet; no `.github/workflows/` exists). Triggers arrive through the mirror's pushes, so check once that branch and tag pushes from the mirror start workflows as expected. |
| Woodpecker on Forgejo (`.woodpecker/`) | Deploys only the landing site under `site/`. It does not build, test or release the application, and GitHub Actions should not deploy `site/`. |

`.gitattributes` stores text with LF and checks out `*.go`, `*.sh`, `*.ps1`, `*.md`, `*.json`, `*.yml` and similar with LF on every OS, so Windows runners get the same bytes as Linux runners. Images, fonts, `.syso` and the zip fixtures under `internal/rules/internal/zipaes/testdata/` are binary.

## 2. Prerequisites

- **Go**: the version in `go.mod` (`go 1.27.1`, no `toolchain` line). `actions/setup-go` with `go-version-file: go.mod` matches it.
- **cgo**: not needed. mygo is pure Go (purego) on Windows, Linux and macOS; release builds set `CGO_ENABLED=0`. `go test -race` is the exception: the race detector needs cgo and a C compiler.
- **PowerShell 7.3+** (`pwsh`) for the `scripts/*.ps1` files (`#Requires -Version 7.3`). The scripts use `/`-joined paths and `$IsWindows`/`$IsLinux`, and do not depend on the working directory: each resolves the repository root from its own location.
- **bash, Docker, curl** for the MinIO suite, on Linux only. A Linux Docker daemon is required; Windows runners only run Windows containers.
- **Network**: module downloads; the MinIO suite also pulls `golang:1.24.2-bookworm` (`golang:1.24.8-bookworm` for `latest`) from Docker Hub and fetches MinIO source from `codeload.github.com`.
- `.agents/`, `designs/` and `site/` are not Go code. `go ... ./...` already skips `.agents/` (leading dot), but `gofmt -l .` does not, so exclude it there.

## 3. Commands

### Checks (any OS)

| Command | Passes when |
| --- | --- |
| `gofmt -l .` (ignore lines under `.agents/`) | no output |
| `go vet ./...` | exit 0 |
| `go test ./...` | exit 0 |
| `go build ./cmd/pail ./cmd/pail-desktop` with `CGO_ENABLED=0` | exit 0 |

`go test ./...` is offline: it uses `internal/s3test` (in-memory S3) and `httptest`. The `integration` package compiles but skips every test unless `PAIL_TEST_ENDPOINT` (MinIO suite) or `PAIL_EXT_ENDPOINT` (external suite) is set. **A job that runs `go test ./...` must never see `PAIL_EXT_*`**: with them present, that plain command runs the external suite against the real endpoint.

`cmd/pail-desktop` tests are headless (no window). With `PAIL_UI_SNAPSHOT_DIR=<dir>` they additionally write light/dark, English/Chinese PNGs for human review; without it those tests skip. Verified so far on native Windows and on WSL Debian (with WSLg); not yet on a GitHub-hosted runner without a display server.

The native smoke flags `pail-desktop --smoke-exit 5s [--smoke-screenshot out.png]` open a real window and need a desktop session. They have only been run by hand.

### Release build — `scripts/release.ps1`

```powershell
pwsh ./scripts/release.ps1 -Version 1.2.3 [-CliOnly] [-Targets linux/amd64,windows/arm64] [-OutDir <dir>]
```

| Input | Default | Notes |
| --- | --- | --- |
| `-Version` | `0.0.0-dev` | Must match `^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$` (no leading `v`). Injected as `-X main.version=` into `cmd/pail` and `cmd/pail-desktop`. |
| `-Targets` | all six: `windows`, `linux`, `darwin` × `amd64`, `arm64` | `os/arch` list; a single comma-separated string also works, which is what `pwsh -File` passes from bash. Cross-compiled from any host with `CGO_ENABLED=0`, `-trimpath`, `-ldflags "-s -w"`; Windows GUI adds `-H windowsgui`. |
| `-CliOnly` | off | Builds only `pail`; no macOS `.app`. |
| `-OutDir` | `<repo>/build/release` | Relative paths resolve against the caller's location. `<OutDir>/<Version>/` is deleted and recreated. |

It requires the committed generated icons (`assets/brand/generated/pail.icns`, `pail-256.png`) and the committed Windows resources `cmd/pail-desktop/rsrc_windows_*.syso`; regenerate them with `go run ./tools/genicons` from the repository root (the tool writes paths relative to the working directory). Byte-for-byte reproducibility of the generator and of the release archives has not been checked.

The script stages in `<repo>/build/stage/<Version>/` (removed on success), restores `GOOS`, `GOARCH` and `CGO_ENABLED` and the working directory on exit, and ends by printing `output directory: <path>`.

**A target that fails to build does not fail the script.** It prints a warning, skips the target and lists it in `NOT-BUILT.txt`. CI must treat the presence of `NOT-BUILT.txt` (or a missing expected archive) as a failure.

Output layout in `<OutDir>/<Version>/`:

| File | Contents (all under a top-level `Pail-<Version>-<os>-<arch>/` directory) |
| --- | --- |
| `Pail-<V>-windows-<arch>.zip` | `pail-desktop.exe`, `pail.exe`, `LICENSE`, `README.md`, `README.zh-CN.md` |
| `Pail-<V>-linux-<arch>.tar.gz` | `pail-desktop`, `pail`, `pail.desktop`, `pail.png`, `LICENSE`, READMEs |
| `Pail-<V>-darwin-<arch>.zip` | `Pail.app/Contents/{MacOS/pail-desktop, Resources/pail.icns, Info.plist}`, `pail`, `LICENSE`, READMEs |
| `SHA256SUMS` | One line per archive, `<lowercase sha256>  <file name>`; checks with `sha256sum -c SHA256SUMS` |
| `NOT-BUILT.txt` | Only when a target failed |

Archives are written by `tools/pack`, which sets Unix modes explicitly (directories and executables 0755, other files 0644), so archives built on Windows still carry executable bits. Nothing is code-signed. macOS archives are cross-compiled only: not signed, not notarized, never run on real hardware. Windows binaries are unsigned too.

### MinIO integration — `scripts/integration-minio.sh` / `scripts/test-integration.ps1`

Safe for unattended CI on a Linux runner with Docker. Details in [integration/README.md](../integration/README.md).

```bash
bash scripts/integration-minio.sh
# or
pwsh ./scripts/test-integration.ps1 [-Port 19000] [-MinioImage pinned|latest|<ref>] [-MinioLatestTag RELEASE.…Z] [-GoTestArgs '-run TestApp'] [-LogPath <file>]
```

| Variable (sh) | Parameter (ps1) | Default |
| --- | --- | --- |
| `PAIL_IT_PORT` | `-Port` | `19000` (bound on `127.0.0.1`) |
| `PAIL_IT_MINIO_IMAGE` | `-MinioImage` | `pinned` |
| `PAIL_IT_MINIO_TAG` | `-MinioLatestTag` | `RELEASE.2025-10-15T17-29-55Z` |
| `PAIL_IT_GOTEST_ARGS` | `-GoTestArgs` | empty; whitelist `[A-Za-z0-9_ .\|^$()=/-]` (no `?`, `*`, quotes) |

- The script builds the MinIO image from source (several minutes on a cold Docker cache; GitHub-hosted runners start cold unless a cache is configured), starts one container with a random per-run secret, tmpfs data and an ownership label, waits up to 60 s for readiness, prints the server version, runs `go test ./integration -count=1 -v -timeout 20m`, and removes the container on exit only if the label matches.
- Exit code is `go test`'s; 2 means an input was rejected. `-LogPath` tees the output and appends an exit-code line. The `TestExt*` tests show as SKIP in this run; that is expected.
- One run per host at a time, or give each a different port. No secrets are needed or used.
- On Windows, the ps1 needs the maintainer's local WSL wrapper `linux-task.ps1`; it stops with an error elsewhere. In CI, use a Linux runner.

### External endpoint — `scripts/test-external.ps1`

**Never unattended.** It reads and writes a real, user-owned S3-compatible server.

- Inputs: `PAIL_EXT_ENDPOINT` (https only), `PAIL_EXT_ACCESS_KEY`, `PAIL_EXT_SECRET_KEY` (required); `PAIL_EXT_BUCKET` (default `test`), `PAIL_EXT_REGION` (default `us-east-1`), `PAIL_EXT_PATH_STYLE` (default `true`), `PAIL_EXT_CDN` (optional). Parameters `-LogPath` (default `build/ext-e2e.log`, relative paths resolve against the repository root), `-Run` (default `^TestExt`), `-CleanupOnly -Prefix pail-e2e/<run>/`.
- Writes only below a unique `pail-e2e/<UTC>-<random>/` prefix and cleans it up; never touches bucket-level configuration. After the run it fails if the log contains the access key or the secret key.
- If it is automated at all: manual trigger only (`workflow_dispatch`) in a protected environment with required reviewers, one run at a time, from branches of the primary repository. Never on `pull_request` from forks and never via `pull_request_target`. Keep `PAIL_EXT_*` scoped to that one job. Review the log before publishing it as an artifact.
- A failed cleanup is repaired with `-CleanupOnly -Prefix <the prefix named in the log>`.

## 4. Summary: what may run where

| Task | Unattended CI | Runner |
| --- | --- | --- |
| gofmt check, `go vet ./...`, `go test ./...` | yes | Linux, Windows (macOS untested) |
| `CGO_ENABLED=0 go build` of both commands | yes | any |
| `scripts/release.ps1` | yes (check `NOT-BUILT.txt`) | any with pwsh 7.3+; one host cross-compiles all targets |
| MinIO integration | yes | Linux with Docker |
| `go test -race ./...` | yes | needs cgo and a C compiler |
| Native smoke (`--smoke-*`) | no, by hand | desktop session |
| `scripts/test-external.ps1` | **no**: manual, protected, secrets only for that job | any with pwsh |
