# CI and release process

## Repository ownership

Forgejo (`ssh://git@ssh.via.moe/dejavu/Pail.git`) is the primary repository. Push commits and tags there from Windows. Forgejo mirrors them to [GitHub](https://github.com/DejavuMoe/Pail), where Actions tests the code and publishes release assets. Do not commit or merge source changes on GitHub. The default branch is `master` on both hosts.

Woodpecker deploys only the static website. Its live acceptance is separate from the application release gates; see [site.md](site.md). GitHub Actions never deploys the website.

## Locked tools and dependencies

`mise.toml` pins Go 1.27.4 and Node.js 26.13.1 (LTS). Run `mise trust` and `mise install go node`; use `mise exec -- <command>` without shell activation. `GOTOOLCHAIN=local` prevents implicit Go upgrades and `GOFLAGS=-mod=readonly` prevents implicit module changes. `go.mod` / `go.sum` pin application dependencies. Node runs small standard-library CI scripts only; the website and application need no npm packages.

Actions uses `setup-go` with `go-version-file: go.mod` and its module/build cache. A regression check keeps its version equal to mise. Jobs needing Node install it with the pinned mise action. Application builds, tests and release packaging use Go directly on every host; no CI job installs or invokes PowerShell. `scripts/release.ps1` is an optional compatibility wrapper that forwards old parameters to the Go release tool. Third-party actions are pinned to full commit SHAs with the release tag in a comment, the mise action to a fixed mise release, `go run` tools to a module version, and website containers to image digests. When updating a pin, check that the SHA or digest belongs to the tag (`git ls-remote https://github.com/<owner>/<repo> refs/tags/<tag>`). Changing the Go pin also requires updating the website builder image.

Native tests, native shards and race shards use separate cache scopes (`native`, `native-<part>`, `race-<part>`), in addition to the runner OS/architecture and Go version that `setup-go` includes. Their keys include module files and the ignored `.ci-go-cache-scope` file, which identifies the build mode without marking release sources dirty. Go checks cached build inputs and rebuilds changed packages; tests still run with `-count=1`. An exact cache hit is immutable, so changed application packages may need recompilation on later runs. Source hashes are omitted to avoid uploading a new set of module/build caches on each source change; the measured critical path is UI test execution. GitHub evicts caches unused for 7 days, so the first CI run after a quiet week compiles from scratch.

Release packaging runs without a Go cache. A cache saved by a tag run is visible only to that tag, and `master` never builds the release cross-compilation mode, so a packaging cache would be uploaded on every release and never restored.

Windows owns all source edits and Git writes. For Linux validation use `linux-task.ps1 -Mode build -Project <repo> -Command '...'`; build mirrors are disposable and contain no `.git`. Linux CI runs the same commands directly. Pass workflow paths explicitly to actionlint, which otherwise relies on Git discovery.

## Shared local and CI commands

Run these from the repository root in PowerShell, Bash or another shell; the commands themselves do not depend on the shell.

| Purpose | Command | Runtime requirements |
| --- | --- | --- |
| Static checks | `go vet ./...` | Go on the current host |
| Staticcheck | see below | Go; downloads the pinned staticcheck |
| Native tests | `go test ./...` | Go on the current host; protocol fixtures are in-process |
| Native build | `go build -trimpath -ldflags "-s -w" -o build/ ./cmd/pail ./cmd/pail-desktop` | Go; application builds do not require cgo |
| All release archives | `go run ./tools/release -version 0.6.4` | Go; includes archive verification |
| One target | `go run ./tools/release -version 0.6.4 -targets linux/amd64` | Same tool and rules as a full release |
| Downloaded archive verification | `go run ./tools/verify-release -dir build/release/0.6.4 -version 0.6.4` | Go |
| Race detection | `go test -race ./...` | Go, cgo and a C compiler; Linux in CI |
| MinIO integration | `bash scripts/integration-minio.sh` | Linux, Bash, Docker, curl and Go; Windows uses the optional WSL wrapper |

Staticcheck analyses the Linux, Windows and macOS builds and the `pail_demo` / `pail_pprof` tags in one run, so code used by only one build is not reported as unused. Quality runs it with `CGO_ENABLED=0`, as release builds do:

```sh
go run honnef.co/go/tools/cmd/staticcheck@v0.8.2 -matrix ./... < .github/staticcheck.matrix
```

In PowerShell, pipe the file instead: `Get-Content .github/staticcheck.matrix | go run ...`. Suppress a finding only with a `//lint:ignore <check> <reason>` comment on the line.

CI tests all six native OS/architecture targets because cross-compiling a binary does not test platform-specific runtime behavior. Packaging uses three Linux jobs, one per target OS with both architectures, sharing the same Go tool. This avoids six separate packaging implementations and repeated native runner setup. Node is used for CI/release metadata checks, not application builds. Website deployment remains a Linux shell operation. Real external endpoints and native GUI interaction remain manual-only checks.

## Desktop test performance

The desktop tests render the real UI in memory, including animation frames, minimum-window layouts and translated dialogs. Keep these tests in the full race run. They share process-wide locale and native-menu settings, so adding `t.Parallel()` to the UI suites would break their isolation.

Demo environments share only the nine immutable encoded image bodies, initialized with `sync.OnceValue`. Each environment still creates its own service, account, buckets and objects; the test server copies image bytes on insertion. `internal/demo` `TestImagesMatchAndStayIsolated` compares every seeded image with the original generator, checks image dimensions and formats, and checks that replacing an image in one environment leaves the other environment and cache unchanged. Ordinary local tests and CI use this same path.

Race runs in three jobs: `core` covers every package except `cmd/pail-desktop`, and `desktop-1` / `desktop-2` split that package's top-level tests, examples and fuzz seeds. The native tests on the two slowest runners, windows/arm64 and darwin/amd64, use the same three parts; the other four targets run every package in one job. `node scripts/test-shard.mjs <race|native> <part>` discovers the inventory and runs each shard: `race` with `-race -shuffle=on -count=1 -timeout 15m`, `native` with the unsplit native flags `-vet=all -shuffle=on -count=1 -timeout 10m`. A selector test checks that the workflow uses the same flags and parts. Empty or duplicate desktop inventories fail. The selector tests check that every discovered name belongs to exactly one shard. Test names are discovered on each run; adding a test needs no workflow edit. The race shards of the desktop package add `-parallel 1`: mygo keeps package-level state that parallel headless windows write at once, which the race detector reports although the app, with one UI thread, never does. A local full run is `go test -race -parallel 1 -count=1 -timeout 15m ./...`.

The desktop split balances measured durations: `scripts/test-shard-weights.json` lists the tests that take at least 0.2 s and an average for all others, and the script assigns the slowest remaining test to the shard with less total weight. The weights affect only balance. A test missing from the file gets the average, and a name that no longer exists is ignored. A few long dialog and transfer tests dominate the package: a split by position alone can make one shard twice as long as the other, and adding or removing one test moves the long tests to the other side. Race and native shards use the same weights. Refresh them when one desktop shard is clearly longer than the other:

```sh
go test -count=1 -timeout 15m -json ./cmd/pail-desktop > build/test-perf/desktop-weights.json
node scripts/test-shard.mjs weights build/test-perf/desktop-weights.json > scripts/test-shard-weights.json
```

The measurement runs without `-race`; only relative durations matter. Create `build/test-perf` first.

To measure desktop execution separately from compilation and other packages, create `build/test-perf`, then run:

```sh
go test -race -count=1 -timeout 15m -json -cpuprofile build/test-perf/desktop.cpu -o build/test-perf/desktop.test ./cmd/pail-desktop > build/test-perf/desktop.json
go tool pprof -top -cum build/test-perf/desktop.test build/test-perf/desktop.cpu
```

The JSON `pass` events contain per-test elapsed seconds. Compare top-level tests separately from their subtests to avoid double counting. Run before/after measurements on the same host, without another build or test workload in parallel. CPU profiles include race-detector overhead; their cumulative CPU times are not wall-clock durations. A local speedup does not establish the duration on a hosted CI runner.

## Workflows

| Workflow | Trigger | Required jobs |
| --- | --- | --- |
| `CI` | `master` push or manual | Ten native test jobs (six targets); Quality; three Race shards; MinIO |
| `CodeQL` | `master` push, manual, or Wednesday 03:23 UTC | Go on Linux, Windows and macOS; site JavaScript; Actions |
| `Release` | `v*` tag push | Exact-commit preflight; three parallel packaging jobs; verified publication |

Tests and CodeQL run in parallel. A later branch push cancels obsolete validation; release runs are not cancelled. Go analysis uses native OS builds to include OS-specific source.

The weekly CodeQL scan picks up new analysis rules without a source push. Push and manual runs share a concurrency group so a manual rerun replaces an obsolete source scan. Scheduled runs have a separate group and do not replace the push/manual runs required for release. GitHub may delay scheduled runs and disables schedules in public repositories after 60 days without repository activity; check Actions when resuming an inactive project.

| Target | Native test runner | Release archive |
| --- | --- | --- |
| windows/amd64 | windows-2025 | ZIP |
| windows/arm64 | windows-11-arm | ZIP |
| linux/amd64 | ubuntu-24.04 | tar.gz |
| linux/arm64 | ubuntu-24.04-arm | tar.gz |
| darwin/amd64 | macos-15-intel | ZIP with Pail.app |
| darwin/arm64 | macos-15 | ZIP with Pail.app |

Each native job checks its actual GOOS/GOARCH and runs `go test -vet=all -shuffle=on -count=1 -timeout 10m ./...`, or its part of it on a split target. The unsplit job, or the `core` part, then builds both commands without cgo and executes their version commands. `-vet=all` runs the full vet checks in the test build instead of running a separate vet build followed by the default test vet subset. `-shuffle=on` runs each package's tests in a new random order, so a test that depends on state left by an earlier one fails. The log prints the order as `-test.shuffle <seed>`; rerun with `-shuffle=<seed>` to reproduce a failure. The smoke build strips symbols and stamps the version, as release builds do. UI unit tests are headless. They do not prove an interactive GUI, GPU rendering or working system keychain on a user's desktop.

Quality checks formatting, workflow syntax, release-gate and shard-selector tests, deployment fixtures, Staticcheck and known Go vulnerabilities. The release-gate test compares the required job names in `scripts/check-release.mjs` with the names the CI matrix produces, so a renamed or added job fails Quality until the gate lists it. It also vets the desktop package with the `pail_demo` and `pail_pprof` build tags, which no test or release build compiles. Race uses cgo and a C compiler on Linux. MinIO uses the existing isolated integration suite, loading a fixed-source image built with the GitHub Actions Buildx cache. No real endpoint credentials are supplied to CI.

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
4. On Windows, create a signed annotated tag, for example `git tag -s v0.6.4 -m "Pail v0.6.4"`, and push only that tag to Forgejo: `git push origin v0.6.4`.
5. Use local `gh run list --repo DejavuMoe/Pail --commit <sha>` and `gh run watch <run-id> --repo DejavuMoe/Pail --exit-status` to monitor the mirrored workflow. Release verifies that the tag is a semantic version and its commit belongs to `master`, then extracts matching English and Chinese changelog sections.
6. Package all six targets, verify the archives, upload them to a draft Release, then publish only after every upload succeeds. A published Release is not overwritten; failed draft uploads can be rerun. Do not move an existing release tag.
7. Download the published archives and `SHA256SUMS`, verify them again, and run native version checks where available. Commit and push the validation record to `master`. This push after a successful release is required: the website build that it starts publishes the new version on pail.download and replaces the mirror of the release files under `/download/` and `/update/` (see [site.md](site.md)). Until it runs, the mirror serves the previous release, and installations that update from pail.download do not see the new one. The old mirror at pan.zsh.moe no longer receives releases.

The release workflow needs `actions: read` for preflight and `contents: write` only for publication. No Forgejo write token or website deployment secret is stored in GitHub. Tags originate in Forgejo; GitHub's write permission is for Release metadata/assets.

## Update signing

The publish job runs in the GitHub environment `release`, which admits only `v*` tags and holds the secret `PAIL_UPDATE_KEY`: the base64 Ed25519 private key whose public half is `PublicKey` in `internal/selfupdate`. Its only other copy is the maintainer's encrypted offline backup. Nothing is signed locally, and the key is never printed, written to the repository or passed on a command line.

After `verify-release`, the job runs:

```sh
go run ./tools/updates sign -dir dist -version "$VERSION"
go run ./tools/updates verify -dir dist -version "$VERSION"
```

`sign` reads the key from the environment and refuses one that does not match `PublicKey`. For each target it signs the update archive (the SHA-256 of the file, as the mygo updater format specifies), reads the manifest that GitHub serves for the latest stable release, and makes delta updates from up to three earlier stable versions listed there: it downloads each base archive, checks its signature, writes the bsdiff delta, applies it again and compares the result with the new archive file by file. A delta that is not smaller than the archive is left out. It then writes `update-<os>-<arch>.json`, whose `previous` lists up to two of those earlier versions for the next release to use as bases. When the feed has no manifest yet (404), as for the first release with updates, there are no deltas; any other error stops the job. `verify` checks every URL, size and signature against `PublicKey` and rejects unknown update files, so a manifest is never uploaded with a file that does not match it.

Changing `PublicKey` strands every installation that has the old one: they no longer accept updates and must be updated by hand once. Rotate the key only when it has leaked, and say so in the changelog.

`tools/updates` also has `mirror`, which the website build uses (see [site.md](site.md)). None of its commands runs in ordinary CI.

History rewrites and re-signing change commit and tag identities. Old successful runs do not satisfy release gates for a rewritten SHA, even when the product source is unchanged. Published assets still belong to the original release run; moving a tag does not rebuild them. Preserve those assets and the original run's evidence, and release future changes under a new version after its own CI and CodeQL pass. Do not bypass the exact-commit gate or overwrite an existing published release to repair history.

## Building and checking release archives

```sh
go run ./tools/release -version 0.6.4
# Optional: -targets linux/amd64,linux/arm64 -out-dir <directory> -cli-only
go run ./tools/verify-release -dir build/release/0.6.4 -version 0.6.4
```

`-version` is required and accepts SemVer without the `v` prefix. `-targets` accepts a nonempty comma-separated subset of the six supported targets without duplicates. The repository root is discovered from the current directory or its parents, without Git metadata. The default output is `<repo>/build/release/<version>/`; an explicit relative `-out-dir` resolves from the caller's directory. Existing release output is replaced only after all selected targets build and pass verification; linked outputs and unrelated files are refused. The replacement must preserve both the archive names and their contained files, so a same-name CLI-only archive cannot replace one containing the desktop app. The old directory is moved aside and deleted only after the new one is in place. Run only one release build for a given output/version at a time.

The optional `scripts/release.ps1` wrapper retains `-Version`, `-Targets`, `-OutDir` and `-CliOnly` for existing callers, and requires PowerShell 7.3+. It has no build, platform or archive logic of its own and is not used by CI.

| File | Contents |
| --- | --- |
| `Pail-v<V>-windows-<arch>.zip` | `pail-desktop.exe`, `pail.exe`, `.mygo-files`, license |
| `Pail-v<V>-linux-<arch>.tar.gz` | `pail-desktop`, `pail`, `install.sh`, `pail.desktop`, `pail.png`, `.mygo-files`, license |
| `Pail-v<V>-darwin-<arch>.zip` | `Pail.app` with `Contents/MacOS/pail-desktop` (its `CFBundleExecutable`) and `Contents/MacOS/pail`, license |
| `pail-update-<V>-<os>-<arch>.tar.gz` | The files an update installs, without a top-level directory: the two executables and `.mygo-files`, or `Pail.app` |
| `SHA256SUMS` | Exactly one SHA256 for every user archive |

The release workflow adds per target `update-<os>-<arch>.json` and up to three `pail-update-<from>-to-<V>-<os>-<arch>.delta` files (see "Update signing"). `SHA256SUMS` lists only the user archives; the update files carry Ed25519 signatures in their manifest instead.

Each user archive has the top-level directory `Pail`, so an unpacked folder keeps its name across versions. `.mygo-files` lists the top-level entries of the installation; an update removes the entries of the old list that the new version no longer has. `install.sh` copies the listed files to `${XDG_DATA_HOME:-~/.local/share}/pail`, links both executables into `~/.local/bin` and writes a `.desktop` entry with absolute paths. `tools/release` invokes the existing `tools/pack` command, which preserves executable mode 0755 even when packaging on Windows. `tools/verify-release` rejects missing/unexpected files, incorrect checksums, unsafe archive entries, wrong executable permissions, wrong GOOS/GOARCH, cgo-enabled binaries, incorrect executable packages and broken platform launch entries. It checks the PE, ELF or Mach-O format and architecture, then reads the release stamp that `internal/buildstamp` stores (`pail-release-version:<version>;`): the stamp must occur exactly once, inside a file-backed section, and carry exactly the version; the programs report the version from the same stamp. A missing, duplicate or malformed stamp, or truncated data, fails verification. The release tool invokes it automatically with the selected targets; when independently verifying a subset, pass the same `-targets` list. `-cli-only` produces local CLI-only archives without desktop launch assets, explicitly skips complete-release verification, and must not be published as a full release.

Generated icons and Windows resources are committed; regenerate them with `go run ./tools/genicons -version X` (from anywhere in the repository; X is recorded in the Windows version resource and defaults to `0.0.0`). A release build with Windows targets runs it with its own version (a pre-release suffix dropped), so the `.exe` reports that version, and puts the committed `.syso` files back afterwards. Archives are cross-compiled with `CGO_ENABLED=0`, `-trimpath`, `-s -w` and the stamp from `buildstamp.LDFlag(<version>)` (`-X github.com/DejavuMoe/Pail/internal/buildstamp.stamp=pail-release-version:<version>;`). The stamp does not need a symbol table, so release binaries are stripped. The desktop app also gets `-X github.com/egoist/mygo.production=1` and the build tags `mygo_noinspector,mygo_nodownload`, which leave mygo's development inspector and its own download code out; `verify-release` rejects a desktop binary built without `mygo_noinspector`. Windows GUI adds `-H windowsgui`. Binaries have no developer signature; macOS is not notarized.

## Integration and manual-only checks

`bash scripts/integration-minio.sh` builds the pinned MinIO fixture, binds it only to localhost, uses generated credentials and tmpfs data, and removes only its owned container. `PAIL_IT_MINIO_IMAGE=local:<image>` uses a previously loaded local image and fails if missing; the CI Buildx step uses this to avoid rebuilding or pulling the fixture twice. `-MinioImage latest` in the PowerShell wrapper remains an explicit compatibility check, not a moving CI dependency. See [integration/README.md](../../integration/README.md) for all inputs.

Plain `go test ./...` skips integration tests unless their endpoint variables are present. Never provide `PAIL_EXT_*` to ordinary CI: doing so enables the real external suite. `scripts/test-external.ps1` is manual only, scoped to the authorized endpoint and a unique `pail-e2e/` prefix. It must never run with fork credentials or automatically after a push.

External cleanup may fall back from version listing only when the endpoint explicitly reports that the API is unsupported. Denied or failed object, version or multipart listings remain cleanup errors, including errors during final verification; an unreadable list is not evidence of an empty prefix. Local HTTP regression fixtures cover these failures without external credentials.

The terminal examples on the website come from `site/cli-demo.json`. `go test ./cmd/pail -run TestSiteCLIDemo` compares it with what the CLI prints; run it with `PAIL_UPDATE_GOLDEN=1` to rewrite the file.

The headless UI tests do not open a window. To check a real one, run the following in a desktop session; it opens the demo window, saves a screenshot and closes after 5 s:

```sh
go run -tags pail_demo ./cmd/pail-desktop --demo --smoke-exit 5s --smoke-screenshot out.png
```

Snapshot, API, protocol, native-runtime and real-server results are recorded separately in [status.md](status.md).
