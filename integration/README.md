# Integration tests

Two suites run the storage and application layers against a real S3 server:

- **MinIO** in an isolated container (repeatable protocol reference), run by `scripts/integration-minio.sh` (directly or through `scripts/test-integration.ps1`).
- **An external S3-compatible endpoint** you own, run by `scripts/test-external.ps1`.

Plain `go test ./...` skips both, so it stays offline. A skipped suite is not an integration pass: read the actual test output.

## MinIO suite

The suite needs Linux with `docker`, `curl` and `go` on `PATH`. The test logic lives in [scripts/integration-minio.sh](../scripts/integration-minio.sh), which changes to the repository root itself and can be run from any checkout location:

```bash
bash scripts/integration-minio.sh                                  # pinned MinIO build
PAIL_IT_MINIO_IMAGE=latest bash scripts/integration-minio.sh       # newest upstream release, built from source
PAIL_IT_PORT=19001 PAIL_IT_GOTEST_ARGS='-run TestApp' bash scripts/integration-minio.sh
```

| Variable | Default | Meaning |
| --- | --- | --- |
| `PAIL_IT_PORT` | `19000` | Host port, bound on `127.0.0.1` only (1024–65535) |
| `PAIL_IT_MINIO_IMAGE` | `pinned` | `pinned`, `latest`, or a full image reference |
| `PAIL_IT_MINIO_TAG` | `RELEASE.2025-10-15T17-29-55Z` | Upstream tag built for `latest` |
| `PAIL_IT_GOTEST_ARGS` | empty | Extra `go test` arguments, split on whitespace |

Every value is checked against a whitelist before Docker is touched; an invalid value exits with code 2.

`scripts/test-integration.ps1` (PowerShell 7.3+) wraps the shell script with the same options as parameters and an optional log file:

```powershell
./scripts/test-integration.ps1                      # pinned MinIO build
./scripts/test-integration.ps1 -MinioImage latest   # newest upstream release, built from source
./scripts/test-integration.ps1 -Port 19001          # host port override
./scripts/test-integration.ps1 -LogPath build/minio.log -GoTestArgs '-run TestApp'
```

On Linux it runs the shell script directly with `bash`. On Windows it needs the local WSL wrapper `linux-task.ps1` on `PATH` and calls `linux-task.ps1 -Mode build` once for synchronization and the whole test lifecycle; do not run another build-mode job against the same project concurrently. Without that wrapper the Windows run stops with an error; use a Linux host instead.

- **Image.** Upstream MinIO is distributed as source only, so the script builds a local image from [Dockerfile.minio](Dockerfile.minio) (official MinIO commit `0d7408fc9969caf07de6a8c3a84f9fbb10a6739e`, release `RELEASE.2025-04-22T22-12-26Z`, built with `golang:1.24.2-bookworm` and automatic toolchain switching disabled). `-MinioImage latest` builds the newest release tag (`-MinioLatestTag`) from [Dockerfile.minio-latest](Dockerfile.minio-latest). Any other `-MinioImage` value is used as a full image reference and pulled by Docker. The first run compiles MinIO; later runs reuse the Docker build cache. This is an isolated test fixture, not a recommended production server.
- **Container.** The runtime image holds only the compiled binary. Data lives on an ephemeral tmpfs limited to 2 GiB, the HTTP port is bound to `127.0.0.1` only, the console port is not exposed and no user files are mounted. Credentials exist only for this container; no AWS profile or real-cloud credentials are needed.
- **Cleanup.** The container has a unique `pail-integration-*` name and a `com.pail.integration` label. The exit trap checks the label and removes the container on success or failure. A forcibly terminated Docker, WSL or CI host can prevent the trap from running; the name and label identify a leftover container.
- **Server version** is printed and exported as `PAIL_TEST_SERVER_VERSION`; `-LogPath` tees the full `go test -v` output with a final exit-code line.

For an already-running isolated local test server, skip the scripts and set these variables for `go test ./integration` directly:

```text
PAIL_TEST_ENDPOINT=http://127.0.0.1:19000
PAIL_TEST_ACCESS_KEY=<explicit local test access key>
PAIL_TEST_SECRET_KEY=<explicit local test secret>
```

Without `PAIL_TEST_ENDPOINT` the suite skips. With an endpoint but without explicit credentials it fails. Only plain-HTTP loopback endpoints are accepted. The suite creates unique test-owned buckets and registers cleanup right after each creation; it never deletes a bucket supplied by a user.

### Storage layer (`s3_test.go`, against `internal/storage`)

- Bucket creation and cleanup (versions and delete markers included), `ListBuckets` with creation date, `BucketRegion`.
- Exact Unicode, space, plus, percent, question-mark and hash characters in keys; same-bucket copy of each; delimiter listing.
- 1005 objects paginated across the 1000-key boundary, then removed with `DeleteObjects` (chunked, results in input order).
- A 12 MiB multipart upload with progress, a three-part ETag, ranged multipart download to a `safepath` temporary file, `Publish` without overwrite, SHA-256 round trip.
- Multipart server-side copy (a create-only condition forces `UploadPartCopy`) with SHA-256 check, and the zero-byte case.
- Versioning: distinct version IDs, historical reads, copy from a version, delete marker, `DeleteObjects` of a version, the marker, an unknown version and a duplicate.
- Presigned GET (current and pinned version), HEAD and PUT; context cancellation.
- Every checksum mode (`""`, auto, when_required, when_supported) with empty, small and multipart payloads.
- `ProbeConditionalWrites` (the outcome is logged, not asserted: it depends on the provider).

### Application layer (`app_*_test.go`)

`TestApp*` drives `internal/app` (Service, task queue, item results) against the same MinIO; an independent SDK client seeds and verifies state. Covered: upload (single, 11 MiB multipart, folder, empty folder), download (object, folder, whole bucket, versions), upload and download conflict policies, a real create-only race (a second client writes the key while the multipart upload is in flight), copy, move and rename (same bucket, cross bucket, versioned source keeps history), delete of 1105 keys, versions (restore, restore deleted, delete versions), in-place rewrites (storage class, properties, metadata and tags preservation, SSE), bucket management (versioning, policy, CORS, lifecycle, tagging, empty, delete), multipart list and abort, sync (upload, download, mirror, checksum), GZIP, ZIP and AES-ZIP rules with automatic decode, and every checksum mode through a recording proxy.

Known server limits (the tests skip or log; they are not app bugs): MinIO does not implement PutBucketCors (Pail reports Unsupported); SSE-S3 (`AES256`) needs a KMS (the rewrite fails per item with NotImplemented); `ListMultipartUploads` with a prefix returns nothing for folder prefixes (Pail lists the whole bucket and filters locally); the local mirror delete is not executed (the trash hook is unexported).

A passing MinIO run establishes these behaviours against that server only. It does not establish AWS-specific features, interoperability with every S3-compatible vendor or desktop UI wiring.

## External endpoint suite (`ext_*_test.go`, `TestExt*`)

`TestExt*` runs the application layer against a real S3-compatible server. It is skipped unless `PAIL_EXT_ENDPOINT` is set.

```powershell
# Set these in the current session only (never in a file); the values are never echoed or logged.
$env:PAIL_EXT_ENDPOINT = 'https://<gateway>'
$env:PAIL_EXT_ACCESS_KEY = '<access key>'
$env:PAIL_EXT_SECRET_KEY = '<secret key>'
$env:PAIL_EXT_BUCKET = '<existing bucket>'   # default: test
$env:PAIL_EXT_REGION = 'us-east-1'           # default
$env:PAIL_EXT_PATH_STYLE = 'true'            # default
$env:PAIL_EXT_CDN = 'https://<cdn host>'     # optional: enables TestExtCDN
./scripts/test-external.ps1 -LogPath build/ext.log
./scripts/test-external.ps1 -CleanupOnly -Prefix pail-e2e/<run>/   # remove a stray run prefix
```

Safety rules the suite enforces, and that the script checks afterwards:

- Every write goes below one unique run prefix `pail-e2e/<UTC yyyymmdd-hhmmss>-<random6>/`, with one sub-prefix per test. Every delete path refuses keys outside it.
- No bucket creation or deletion and no bucket-level write (versioning, policy, lifecycle, CORS, tagging, ACL, ...). Bucket-level reads (`GetBucketConfig` for every kind) are fine; object ACLs are only read.
- Cleanup runs per test and once more in `TestMain`: all versions, unfinished multipart uploads, then a verification listing. Leftovers fail the run and name the `-CleanupOnly` command. `TestMain` also compares the keys outside the run prefix with a snapshot taken before the first write and warns about differences.
- At most 300 MiB of data per run (uploads and downloads counted), largest object 64 MiB, default app concurrency. Local mirror deletes are previews only (no real OS trash).
- CDN checks fetch only unique keys of the run prefix plus one read-only GET of the public reference object. A 403 or 404 from the CDN is recorded as "not public via CDN" and skips that sub-check.
- Credentials are read from the environment only. After a run the script fails if the log contains the secret key or the access key (raw or URL-escaped).

A passing external run establishes behaviour against that one server and CDN only. Server limits (no versioning, no lifecycle, ignored storage class or SSE, ...) are recorded in the log as capabilities, not as failures. Results are summarized in [docs/dev/status.md](../docs/dev/status.md).

CI can pass `PAIL_IT_MINIO_IMAGE=local:pail-test-minio:ci` after loading the fixed-source Buildx image. This mode requires the image to exist locally and never pulls or rebuilds it.
