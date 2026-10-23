# Changelog

English | [简体中文](CHANGELOG.zh-CN.md)

All notable changes to this project are documented in this file. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Changed

- Sorting large object lists uses less temporary memory for names containing ASCII uppercase letters.

### Fixed

- Completed tasks no longer retain their data after being evicted from the task history.

## [0.3.0] - 2026-10-21

### Added

- While tasks are queued or running, the taskbar button on Windows (the Dock icon on macOS) shows their combined progress.

### Changed

- Each notch of the mouse wheel now scrolls smoothly over a short moment instead of jumping. Touchpad scrolling is unchanged, and with the system "reduce motion" setting on, the wheel scrolls at once.

## [0.2.0] - 2026-10-20

### Added

- Dialogs, the command palette, menus and toasts now animate in and out. Buttons and list rows fade on hover, the transfers panel opens and closes smoothly, and progress bars and counts change gradually.
- With the system "reduce motion" setting on, nothing moves or scales. Only colours and opacity fade.

### Changed

- Release archives contain only the executables, the platform launch files and the license. The READMEs stay in the repository and the online documentation.
- On Windows and Linux, the context menus of objects and buckets and the menus of the Upload, account and "⋯" buttons are drawn inside the window instead of as system menus. They show command icons and shortcuts, and open submenus on hover or with the arrow keys. They can be used entirely from the keyboard, and Shift+F10 or the menu key opens a context menu below the current row. macOS keeps the system menus.
- Dialogs, the command palette, select popups and toasts share one look. The current choice in a list is marked with a check and bold text instead of a blue fill, and shortcuts are drawn as key caps.
- "Select all" uses a double-check icon so it is not confused with the check that marks the current choice.

### Fixed

- Inspector: the standard header labels (Content-Type, Cache-Control and so on) showed only an ellipsis next to the "?" icon. They now show the full name unless the panel is too narrow.
- Resizing the window no longer makes the search box, toolbar labels, path bar or object table columns lag behind or flicker.
- The sidebar and the inspector keep the width you set when the window is narrower than 1100 px.

## [0.1.0] - 2026-10-20

First release of Pail.

### Added

- **Desktop app** (`pail-desktop`) for Windows, Linux and macOS: sidebar with account switcher and bucket list, breadcrumbs, a sortable multi-select object table with automatic paging, search, flat view, inspector, transfers panel, status bar, command palette (`Ctrl/⌘+K`), context menus and shortcuts, light and dark themes, English and 简体中文, and a `--demo` mode that runs on an in-memory S3 server.
- **Accounts**: any number of AWS and S3-compatible accounts (custom endpoint, region, path-style, access keys, AWS profile, default credential chain, anonymous, AssumeRole), a connection test and a checksum mode per account. Tested by hand on Amazon S3, Cloudflare R2, Wasabi, MinIO and a self-hosted VersityGW.
- **Browsing and preview**: bucket and folder navigation, JSON, CSV, XML, text, image and hex preview, and copying the key, URI, URL or presigned URL.
- **Transfers**: upload and download files, folders, versions and whole buckets, with drag and drop. Uploads, downloads and other tasks run in separate queues with configurable limits, and can be cancelled and retried. There are five conflict policies, large files are transferred in parts, and a download appears at its final path only after it has been verified.
- **Object management**: copy, move and rename within a bucket or across buckets; bulk delete with a result for each item; edit headers, metadata, tags, ACL, storage class and server-side encryption (SSE-S3, SSE-KMS, SSE-C), then check the result; a MIME type table.
- **Versions**: list versions and delete markers, restore a version or deleted keys, download and permanently delete several versions.
- **Buckets**: create, delete and empty; copy, move or download a whole bucket; 14 bucket settings as forms or JSON; list and abort incomplete multipart uploads.
- **Sync jobs**: upload, download and mirror, with a preview, three compare modes and exclude globs. Local deletions go to the OS trash.
- **Upload rules**: headers, storage class, encryption and compression (GZIP, ZIP, AES-256 ZIP with levels), decoded again on download.
- **CDN domain per account** (a base URL or a `{bucket}`/`{key}` template, with an optional bucket list), a presigned URL dialog, and CloudFront distributions and invalidations for AWS accounts.
- **Command line** (`pail`): `accounts`, `buckets`, `ls`, `cp`, `mv`, `rm`, `mb`, `rb`, `presign`, `url`, `versions`, `restore`, `sync` and `vault status`, with JSON output and fixed exit codes. It shares accounts and sync jobs with the app. See [docs/cli.md](docs/cli.md).
- **Compatibility with openS3**: an existing `openS3` configuration folder and keychain entries are migrated automatically, and older vaults and `opens3-*` object metadata are still read.

### Security

- Secrets are stored in the OS keychain or in an optional encrypted vault with idle auto-lock, not in configuration files.
- The diagnostic log is off until you turn it on. It redacts URL query strings, Authorization headers, signatures and known secret values.
- Every destructive or bulk action shows its scope and asks for confirmation.

### Known limitations

- The macOS app was run by hand on Apple silicon and Intel Macs. It has no developer signature and is not notarized, so before the first launch run `xattr -dr com.apple.quarantine` on the downloaded app.
- AWS AssumeRole and CloudFront have not been tested against a real AWS account.
- SSE-S3, SSE-KMS and SSE-C uploads, and moving local files to the real OS trash during a sync, have not been checked end to end.
- A proxy that decompresses gzip responses makes the size of a gzip-encoded object differ from what the server reports. Pail refuses such a download and writes no file.
- `size_time` sync can miss an edit that keeps the file size. Checksum sync transfers objects again when they have no `pail-sha256` metadata.
- Task titles and the command line are not translated.
