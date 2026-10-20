# Changelog

English | [简体中文](CHANGELOG.zh-CN.md)

All notable changes to this project are documented in this file. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Changed

- Overlays now share one look: dialogs, the bucket settings dialog, the command palette, select popups and toasts take their radius (6 controls, 8 popups, 10 dialogs), shadow and dark-theme outline from the design tokens instead of per-view values, and no colour is written outside `theme.go` any more.
- Command palette and select popups use one list row: 28 px high, with the command icon, shortcuts drawn as key caps (also `Esc`), and a neutral highlight that follows both the keyboard and the pointer. The current value is marked by a check and a heavier weight instead of the blue selection fill. The "Unavailable here" group and the command groups are section labels.
- The bucket settings dialog reuses the standard dialog frame; its dividers show only while the list or the form scrolls.
- Tooltips and toasts take their colours from the theme tokens, and toasts follow the overlay radius and shadow. The tooltip radius and shadow are still the library's.
- "Select all" uses a double check icon, so it cannot be mistaken for the check that marks a current choice.
- On Windows and Linux, the context menus of objects and buckets and the menus of the Upload, account and "⋯" buttons are drawn in the window in the overlay style instead of as system menus: command icons, shortcuts as key caps, a check and a heavier weight for the current choice, submenus that open on hover or with the arrow keys, and full keyboard control (arrows, Home and End, first letter, Enter, Escape). Context menus open at the pointer, or below the current row with Shift+F10 or the menu key, and stay inside the window. A click outside a menu only closes it. macOS keeps the system menus.

### Fixed

- Inspector: the standard header labels (Content-Type, Cache-Control and so on) showed only an ellipsis next to the "?" icon. The label no longer takes a zero-width flex basis; it shrinks and truncates only when the panel is really too narrow, and its tooltip shows the full name.
- Resizing the window no longer lays the search box and the object table columns out by the previous frame's size, which made them lag one step behind the drag.
- Toolbar labels, the compact path bar and the object table columns now switch with a 12 px hysteresis, so dragging across a width threshold no longer makes them flicker back and forth.
- The sidebar and the inspector no longer shrink by themselves when the window gets narrower than 1100 px; they keep the width you gave them.
- The divider between panels turns a neutral grey when hovered or dragged instead of the accent blue.

## [0.1.0] - 2026-10-20

First release of Pail. Verification per feature is recorded in [docs/status.md](docs/status.md).

### Added

- **Desktop app** (`pail-desktop`, Go + mygo, Windows, Linux and macOS): sidebar with account switcher and bucket list, breadcrumbs, virtualised sortable multi-select object table with automatic paging, search, flat view, inspector, transfer panel, status bar, command palette (`Ctrl/⌘+K`), context menus and shortcuts, light and dark themes, English and 简体中文, `--demo` mode on an in-memory S3 server.
- **Accounts**: any number of AWS and S3-compatible accounts (custom endpoint, region, path-style, access keys, AWS profile, default chain, anonymous, AssumeRole); connection test; per-account checksum mode.
- **Browsing and preview**: bucket and folder navigation, JSON, CSV, XML, text, image and hex preview, copy key, URI, URL and presigned URL.
- **Transfers**: upload and download of files, folders, versions and whole buckets with drag and drop; separate upload, download and other queues with configurable limits, cancel and retry; five conflict policies; multipart transfers; create-only conditional writes with a fallback for providers that lack them; downloads published atomically after verification.
- **Object management**: copy, move and rename (same and cross bucket); bulk delete with per-item results; edit headers, metadata, tags, ACL, storage class and server-side encryption (SSE-S3, SSE-KMS, SSE-C), verified afterwards; MIME type table.
- **Versions**: list versions and delete markers, restore a version or deleted keys, download and permanently delete several versions.
- **Buckets**: create, delete and empty; copy, move or download a whole bucket; 14 bucket settings as forms or JSON; list and abort incomplete multipart uploads.
- **Sync jobs**: upload, download and mirror with preview, three compare modes and exclude globs; execution re-checks each previewed action; local deletions go to the OS trash.
- **Upload rules**: headers, storage class, encryption and compression (GZIP, ZIP, AES-256 ZIP with levels); decoding on download.
- **CDN domain per account** (base URL or `{bucket}`/`{key}` template, optional bucket list), presigned URL dialog, CloudFront distributions and invalidations (AWS accounts).
- **Command line** (`pail`): `accounts`, `buckets`, `ls`, `cp`, `mv`, `rm`, `mb`, `rb`, `presign`, `url`, `versions`, `restore`, `sync`, `vault status`; JSON output and stable exit codes; shares accounts and sync jobs with the app. See [docs/cli.md](docs/cli.md).
- **Security**: secrets in the OS keychain or an optional encrypted vault (Argon2id, XChaCha20-Poly1305) with idle auto-lock; opt-in redacted diagnostic log; every destructive or bulk action shows its scope and asks for confirmation.
- **Compatibility with openS3**: an existing `openS3` configuration folder and keychain entries are migrated automatically; older vaults and `opens3-*` object metadata are still read.
- **Tooling**: `internal/s3test` in-memory S3 server; MinIO integration suite (`scripts/test-integration.ps1`); prefix-scoped external-endpoint suite (`scripts/test-external.ps1`, `PAIL_EXT_*`); release script with archives for Windows, Linux and macOS (amd64, arm64) and `SHA256SUMS`; icon generator (`go run ./tools/genicons`).

- **CI and releases**: Forgejo-first `master` and tag mirroring; six native test targets, race, isolated MinIO, vulnerability checks and CodeQL; exact-commit release gates; versioned archive and executable names; tool versions pinned by mise. Woodpecker website deployment targets `netcup-vps500` with atomic activation and two retained releases; live deployment acceptance is separate.

### Security

- The website's Goldmark dependency is pinned to v1.7.17, fixing [GO-2026-5320](https://pkg.go.dev/vuln/GO-2026-5320).
- Secrets are never written to configuration files; they live in the OS keychain or the encrypted vault.
- Logs redact URL query strings, Authorization headers, signatures and known secret values.
- Custom endpoints default to the "when required" checksum mode; a request whose outcome is unknown is never replayed.

### Known limitations

- Not tested against real AWS (STS AssumeRole and CloudFront only on `httptest`). Real servers tested: MinIO (two releases) and a self-hosted VersityGW behind Cloudflare.
- Interactive GUI acceptance on macOS and ARM64 is outstanding. CI requires native tests and executable version checks for all six targets; this is separate from manual desktop validation. The macOS app has no developer signature and is not notarized.
- Behind Cloudflare, gzip-encoded objects are decompressed by the proxy, so their downloads are refused (sizes do not match); nothing wrong is written.
- Not verified: versioning on VersityGW (disabled there); SSE-S3, SSE-KMS and SSE-C success paths; objects larger than 5 GiB through in-place rewrites; local mirror deletion into the real OS trash.
- `size_time` sync can miss same-size edits; checksum sync repeats transfers for objects without `pail-sha256` metadata.
- A write-only account under the rename conflict policy cannot detect a replay-induced 412; case-colliding key numbering is not remembered across scans.
- Hand-typed ZIP passwords and SSE-C keys are redacted from logs only by label or field name.
- Task titles from the application layer and the command line are not localized.
- Right-click popups were not visually reviewed; the object list header cannot match the design spec exactly (mygo limitation).
