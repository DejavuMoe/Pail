# Contributing to Pail

Issues are tracked on GitHub; the GitHub repository is a read-only mirror of the main repository on Forgejo.

## Prerequisites

- Go 1.27.3. [mise](https://mise.jdx.dev) pins it in `mise.toml`: run `mise trust` and `mise install go node`, then prefix commands with `mise exec --` if your shell does not activate mise.
- Node.js 24.19.0 is used only by the CI scripts. Building and testing the application needs only Go.
- No cgo and no C compiler, except for race tests (`go test -race ./...`).
- PowerShell 7.3+ is needed only for the optional wrappers in `scripts/*.ps1`.

## Build

```sh
go build -o build/ ./cmd/pail-desktop ./cmd/pail
```

## Release packaging

```sh
go run ./tools/release -version 0.2.0
go run ./tools/verify-release -dir build/release/0.2.0 -version 0.2.0
```

`tools/release` builds and verifies all six targets (Windows, Linux and macOS, amd64 and arm64) and writes the archives and `SHA256SUMS` to `build/release/<version>/`. `-targets` selects a subset. It uses the icons in `assets/brand/generated`, which are committed; after changing `assets/brand/pail-icon.svg`, regenerate them with `go run ./tools/genicons`. `tools/verify-release` rechecks archives, checksums, binary targets and executable permissions.

## Tests

```sh
gofmt -l .
go vet ./...
go test ./...
```

`gofmt -l .` should print nothing. These tests use an in-memory S3 server (`internal/s3test`) and `httptest`, so they cover the protocol but not a real provider. Tests that need a real server are skipped unless their environment variables are set.

MinIO in Docker, on Linux:

```sh
bash scripts/integration-minio.sh
```

On Windows, `./scripts/test-integration.ps1` runs the same suite through the optional `linux-task.ps1` runner. Options and the list of covered behaviours are in [integration/README.md](integration/README.md).

An S3-compatible endpoint you own:

```powershell
./scripts/test-external.ps1
```

Pass credentials only through the `PAIL_EXT_*` environment variables (`PAIL_EXT_ENDPOINT`, `PAIL_EXT_ACCESS_KEY`, `PAIL_EXT_SECRET_KEY`; optional `PAIL_EXT_BUCKET`, `PAIL_EXT_REGION`, `PAIL_EXT_PATH_STYLE`, `PAIL_EXT_CDN`). The suite writes only under `pail-e2e/<run>/`, removes it afterwards, creates and deletes no buckets and changes no bucket settings. Never set these variables in CI.

## Website

```sh
go run ./tools/site -out build/site -version 0.2.0
```

The site in `site/` and `tools/site/` is built from the repository's own Markdown. See [docs/dev/site.md](docs/dev/site.md).

## Documentation

Write documentation as described in [docs/dev/writing.md](docs/dev/writing.md); `go test ./tools/site/` checks the rules that can be checked automatically.

`README.md`, `docs/cli.md` and `CHANGELOG.md` have Chinese counterparts (`README.zh-CN.md`, `docs/cli.zh-CN.md`, `CHANGELOG.zh-CN.md`). Change both in the same commit and keep the headings in the same order and at the same levels.

## Project layout

- `cmd/pail-desktop`: desktop application (mygo).
- `cmd/pail`: command line.
- `internal/app`: application logic shared by both.
- `internal/storage`: S3 protocol layer.
- `internal/cloud`: AWS-specific services (CloudFront).
- `internal/rules`: upload rules and compression.
- `internal/safepath`: safe download paths.
- `internal/trash`: local recycle bin.
- `internal/s3test`: in-memory S3 server for tests and `--demo`.
- `tools/`: icon generation, release packaging and verification, website generator.
- `integration/`: tests against MinIO and external endpoints.
- `site/`: templates, strings and static files of https://pail.download.
- `designs/`: website prototypes.

Architecture, the application API, design rules, CI and verification status are in [docs/dev/](docs/dev/).
