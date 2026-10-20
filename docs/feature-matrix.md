# Feature matrix: CS Browser Pro

This compares Pail with the "Free Edition vs Pro Edition" feature list of CS Browser (formerly S3 Browser), published at https://s3browser.com/s3-browser-free-vs-pro.aspx (fetched 2026-10-19). That page lists 12 differences, reproduced below, some split into sub-rows where Pail has separate entry points. The vendor notes that some features depend on the provider and the account permissions. Pail is an independent implementation and shares no code or assets with that product.

Per-area verification (including everything outside the Pro comparison) is recorded in [status.md](status.md).

Column meanings:

- **API**: an exported `internal/app` function exists.
- **UI**: reachable from `cmd/pail-desktop` (command, menu or tool view found in code). Only the headless UI tests and native smoke screenshots back this; a button existing is not proof that it was clicked against a server.
- **Tests**: a named test against `internal/s3test` (in-memory S3 driven by the real SDK) or `httptest`.
- **Real E2E**: covered by `integration/app_*_test.go` against MinIO, and by the prefix-scoped `integration/ext_*_test.go` suite against a self-hosted VersityGW behind Cloudflare. VersityGW ignores storage class and SSE (Pail reports those items as failed) and has versioning disabled, so those rows gain no evidence from it. AWS has never been tested.

Marks: ✅ evidence exists, ⚠️ partial, ❌ none, n/a not applicable.

| Pro feature | Pail equivalent | API | UI | Tests | Real E2E (MinIO / VersityGW) | Notes |
| --- | --- | --- | --- | --- | --- | --- |
| Concurrent uploads, downloads and other tasks (no limit) | Settings: separate upload, download and other limits, 1-64 each | ✅ `SaveSettings`, `Settings` | ✅ Settings view | ✅ `TestQueuesAreIndependentAndLimitsLive`, `TestSaveSettingsAppliesLimitsToQueue` | ⚠️ the queue ran every E2E transfer; limit values not asserted | Upper bound 64 only prevents typos |
| Amazon S3 compatible accounts (no limit) | Account dialog; `pail accounts` lists them | ✅ `SaveAccount`, `Accounts`, `DeleteAccount`, `TestAccount` | ✅ account form, sidebar | ✅ `TestSaveAccountStoresSecretsInKeyringOnly`, account and connection-test tests | ⚠️ one account per server via `SaveAccount`; no multi-account test | No account cap in code |
| Copy or move an entire bucket | Bucket context menu: "Copy contents to…", "Move contents to…"; `pail cp/mv -r s3://b` | ✅ `Copy`, `Rename` (whole-bucket scope) | ✅ bucket copy dialog | ✅ `TestCopyAcrossBucketsRelativeToSelectionPrefix`, `TestMoveAcrossBuckets` | ✅ `TestAppCopyMoveRename` (same and cross bucket, versioned source) / ✅ `TestExtCopyMoveRename` | Copy and move work inside one account only. The whole-bucket scope through the UI is not covered by E2E |
| Download an entire bucket | Bucket context menu: "Download bucket…"; `pail cp s3://b DIR -r` | ✅ `Download` | ✅ bucket download command | ✅ `download_test.go` | ✅ `TestAppUploadDownloadRoundTrip` (folder and whole bucket) / ✅ `TestExtUploadDownloadRoundTrip` | |
| Apply a storage class to an entire bucket | Bucket menu "Change storage class…"; object menu for selections | ✅ `SetStorageClass` | ✅ rewrite dialog, bucket menu | ✅ `TestStorageClassRewritePreservesEverything`, `TestStorageClassVersionedBucketCreatesNewVersion` | ⚠️ `TestAppRewrites` (MinIO storage class support is limited); `TestExtPropertiesAndRewrites` (VersityGW ignores the class, reported as failed) | Rewrites by copy in place; objects over 5 GiB not verified |
| Apply server-side encryption to an entire bucket | Bucket menu "Change encryption…"; object menu | ✅ `SetEncryption` | ✅ rewrite dialog, bucket menu | ✅ `TestEncryptionRewrite`, `TestEncryptionValidation` | ⚠️ `TestAppRewrites` ran, but SSE-S3 fails on MinIO without a KMS; VersityGW ignores SSE | SSE-S3, KMS and SSE-C success paths not verified on a real provider |
| Automatic upload rules (no per-type limit) | Tools > Upload rules (with draft test strip) | ✅ `UploadRules`, `SaveUploadRules`, `PreviewUploadRules` | ✅ upload rules tool | ✅ `TestUploadWithRules`, `upload_rules_test.go` | ✅ `TestAppCompressionRulesRoundTrip` / ✅ `TestExtCompressionRules` | Rules set storage class, encryption and compression; no rule count limit in code |
| Restore object versions | Versions page: restore; "Show deleted" with Restore and Restore all; `pail restore` | ✅ `RestoreVersion`, `RestoreDeleted` | ✅ versions view, commands `restore` and `restoreall` | ✅ `TestRestoreVersionCreatesNewCurrentAndKeepsHistory`, `TestRestoreDeletedSkipsLiveKeys`, `TestRestoreDeletedLosesToConcurrentUpload` | ✅ `TestAppVersionedLifecycle` (MinIO) / skipped on VersityGW (versioning off) | History is kept; restore copies the old version to a new current one |
| Download multiple object versions at once | Versions page: "Download chosen versions" | ✅ `Download` with versioned refs | ✅ versions view | ✅ `TestDownloadVersions` | ⚠️ versions download covered inside `TestAppUploadDownloadRoundTrip`; multi-version selection not asserted | |
| Delete multiple object versions at once | Versions page: delete chosen versions; `pail rm --versions` (one key) | ✅ `DeleteVersions` | ✅ delete-versions dialog | ✅ `TestDeleteVersionsIsExact`, `TestDeleteVersionsNullVersionAndValidation` | ✅ `TestAppVersionedLifecycle` (a version and a marker) / skipped on VersityGW | |
| CloudFront invalidation: multiple files, entire buckets, distributions | Tools > CloudFront; bucket menu "Invalidate CloudFront…"; object menu "Invalidate…" | ✅ `Distributions`, `DistributionsForBucket`, `Invalidate`, `Invalidations` | ✅ CloudFront tool (AWS accounts only) | ✅ `TestCloudFrontDistributionsAndInvalidation`, `TestCloudFrontInvalidateRejectsBadInputBeforeNetwork` (httptest) | ❌ neither MinIO nor VersityGW has CloudFront | Never run against real CloudFront or AWS |
| Compression level (custom levels) | Upload rule: GZIP, ZIP or AES-256 ZIP with level -2..9 | ✅ rule `Compression.Level` via `SaveUploadRules` | ✅ level choice in the rules tool | ✅ `TestGZIPLevels`, `TestZIPLevels`, `TestAES256ZIPLevelsConcurrent` (`internal/rules`) | ⚠️ the compression round trips cover the formats; levels are not varied | Levels are verified at the codec level only |
| Commercial, business, government and military use | Licensing term, not a feature | n/a | n/a | n/a | n/a | Pail is MIT licensed with no use restriction |
| Priority technical support | Vendor service | n/a | n/a | n/a | n/a | Not applicable to an open-source project |

Summary of the 12 feature rows: five have evidence in every column (copy/move bucket, download bucket, restore versions, delete versions, upload rules); six have at least one partial column (concurrency, accounts, storage class, encryption, download versions, compression level); one has no real E2E (CloudFront; API, UI and tests are in place). No feature has any AWS evidence.

## Beyond the Pro comparison

These exist in Pail and are covered by the tests named in [status.md](status.md), but are not part of the vendor's Pro/Free table: bucket settings (14 items; `TestBucketConfigRoundTrip`, `TestAppBucketManagement`), object headers, tags and ACL editing, sync jobs (`TestAppSyncUploadAndMirror`, `TestAppSyncDownload`, `TestAppSyncChecksumMode`), multipart upload listing and abort, MIME table, per-account CDN domain, encrypted vault, redacted logging, and the CLI.

## Not implemented / differences

- No evidence against AWS S3 for any feature. CloudFront and AssumeRole were tested only on `httptest`.
- Real servers tested: MinIO (two releases) and VersityGW behind Cloudflare. CORS configuration is unsupported on MinIO (reported as Unsupported); SSE-S3 needs a KMS there; the SSE-C success path is unverified.
- Rewriting objects larger than 5 GiB (storage class, encryption, properties) is unverified.
- Local mirror deletion into the OS trash was only previewed against real servers; the Windows and Linux trash implementations have their own tests but no end-to-end run.
- Sync in checksum compare mode treats objects written by other tools (no sha256 metadata) as always changed. `size_time` mode can miss same-size edits.
- Copy and move work within one account; the CLI cannot add or edit accounts.
- A write-only account under the rename conflict policy cannot recognise replay-induced 412 responses; case-colliding names are not numbered consistently across scans.
- Passwords and SSE-C keys typed by hand (not saved as secret references) are redacted from logs only by label or field name.
- macOS binaries are cross-compiled and untested on real hardware; native Windows and Linux under WSLg are the only desktop targets that were run.
- The table header of the object list cannot match the design spec exactly (mygo limitation).
- Features of the vendor's product outside the Pro comparison (for example its free-edition tools) are not tracked here; absence from this table does not mean they exist.
