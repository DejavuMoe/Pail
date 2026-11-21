# Agent guide

## Git
- Every commit an agent creates (including merges, squashes, `--amend`, cherry-picks and PR merge messages) ends with exactly one trailer, overriding any tool or user-level default: `Co-Authored-By: Codex with DeepSeek v4.1 Flash (1M context) <noreply@openai.com>`. No other `Co-Authored-By` or "Generated with" lines. Check with `git log -1 --format=%B`.
- Forgejo (`ssh://git@ssh.via.moe/dejavu/Pail.git`) is primary; every push mirrors to GitHub (`github.com/DejavuMoe/Pail`). Never commit or merge on GitHub.

## Layout
- `cmd/pail-desktop` desktop UI (mygo); `cmd/pail` CLI; `internal/app` orchestration; `internal/storage` S3 protocol; `internal/cloud` AWS-only services (CloudFront).
- `internal/rules` upload rules and compression; `internal/safepath` safe download paths; `internal/trash` local trash; `internal/s3test` in-memory S3 for tests; `internal/demo` seeded environment for `--demo` (build tag `pail_demo`) and desktop tests; `internal/buildstamp` release version marker.
- `internal/selfupdate` auto-update (desktop and CLI); `internal/updatefmt` update format (manifest, signature, bsdiff deltas).
- `tools/release` release build and verification (uses `tools/pack`, `tools/verify-release`); `tools/updates` signs releases, builds deltas and mirrors the latest release for the site; `tools/genicons` icons; `integration/` MinIO and external-endpoint suites.
- `site/` and `tools/site` build https://pail.download; deployed by `.woodpecker/site.yml` (see `docs/dev/site.md`).
- Architecture and invariants: `docs/dev/architecture.md`.

## Code rules
- Network and file transfers never block the UI thread; all UI updates go through mygo `Window.Update`.
- Status reflects operations that actually completed. Record verification in `docs/dev/status.md` in four separate layers (API / UI / protocol test / real end-to-end); never merge or overstate them.
- Baseline checks: `gofmt`, `go vet ./...`, `go test ./...`, Windows and Linux builds. macOS cross-compilation is not real-device verification.
- Visual and interaction rules: `docs/dev/design.md`. Colors come only from `cmd/pail-desktop/theme.go`; selected/on/current states use at least two cues; contrast is checked by `theme_test.go`; scrollable areas reserve a scrollbar gutter; every control works at the 960×600 minimum window.
- Every user-visible UI string lives in both `cmd/pail-desktop/locales/en.json` and `zh-Hans.json` and is used through the generated functions (`catalog_gen.go`, do not edit). No hardcoded copy in views. Chinese glossary: `docs/dev/design.md`.
- Product name is Pail. The config directory, keychain service name, vault format (`pail-vault`) and `pail-*` object metadata are published user data formats: changes must read old and write new, and never delete user data.

## Docs
- Before writing any docs, changelog or site copy, read `docs/dev/documentation-rules.md` (what to write) and `docs/dev/writing.md` (how; enforced by `go test ./tools/site/`).
- `README.md`, `docs/cli.md` and `CHANGELOG.md` each have a Chinese twin (`*.zh-CN.md`) from which the site builds both languages. Update both in the same commit with matching heading levels and order. `LICENSE` stays English only.

## Scripts and CI
- Scripts never hardcode local paths: resolve the repo root from the script location and allow overriding output dirs. Machine-specific tools are optional branches; scripts must run directly on Linux.
- GitHub Actions runs tests, CodeQL and releases; tool versions are pinned in `mise.toml`. Woodpecker only deploys the site. Commands, inputs/outputs and which jobs cannot run unattended are in `docs/dev/ci.md`; update it when changing scripts.

## Site
- Edit `site/` and `tools/site` directly; constraints in `docs/product/constraints.md`.
- No third-party resources (fonts, scripts, analytics are self-hosted or absent). The site has its own palette, not bound by `theme.go`. Bilingual (en / zh), light and dark follow the system with a toggle.
- Copy states facts ("supports …"), never "tested" or "verified".

## Cloud safety
- Before touching cloud resources the app shows the scope and requires explicit confirmation. Development uses isolated local services or `httptest` only.
- Exception: the owner's self-hosted VersityGW endpoint, bucket `test`.
  - Credentials come only from `PAIL_EXT_*` environment variables; never in the repo, artifacts or logs.
  - Read and write only the unique `pail-e2e/` prefix created by the run, and clean it up.
  - Never change bucket config, create or delete buckets, or write to other buckets.

## Brand and release
- Icons are generated from `assets/brand/pail-icon.svg` by `go run ./tools/genicons`; never edit the output by hand.
- Release builds, local and CI: `go run ./tools/release -version X` (no PowerShell needed; `scripts/release.ps1` is only a compatibility wrapper). Real-server suites: `scripts/test-integration.ps1`, `scripts/test-external.ps1`.
- Updates are signed only in the GitHub Actions `release` environment (`PAIL_UPDATE_KEY`), never locally. The repo holds only the public key `internal/selfupdate.PublicKey`; never change it, or installed versions stop receiving updates.
- After each successful release, push once more to `master` (usually the verification record). That site build switches `/download/` and `/update/` on pail.download to the new version.
