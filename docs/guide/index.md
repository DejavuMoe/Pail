# Overview

Pail is a desktop app and a command-line tool for Amazon S3 and other S3-compatible storage, such as Cloudflare R2, Wasabi and MinIO. It runs on Windows, macOS and Linux and is free and open source under the MIT License.

## What you download

Each release archive holds two programs that share the same accounts, saved sync jobs and settings:

- **The desktop app** (`pail-desktop`) browses buckets, uploads and downloads, edits object properties, versions and bucket settings, and runs sync jobs and upload rules. On macOS it is `Pail.app`.
- **The command-line tool** (`pail`) does the same work from a terminal or a script, with JSON output and fixed exit codes. See the [command-line reference](../cli.md).

The archive also holds the license, and on Linux a `.desktop` entry and an icon. There is no installer: unpack the archive and start the program. [Install](install.md) has the details.

## Systems

| System | Builds |
| --- | --- |
| Windows | x64 and Arm |
| macOS | Apple silicon and Intel, macOS 11 or later |
| Linux | x64 and Arm |

There are no 32-bit builds.

## Supported services

Pail supports Amazon S3, Cloudflare R2, Wasabi, MinIO, VersityGW and other services that offer the S3 API. Add any of them except Amazon S3 as an S3-compatible account with its endpoint.

Known limitations are listed in the [changelog](../../CHANGELOG.md).

## The main window

- **Sidebar**: the account menu at the top, the bucket list, the tools (**Sync jobs**, **CloudFront**, **Upload rules**, **MIME types** and **Multipart uploads**) and **Settings** at the foot.
- **Toolbar**: upload, new folder, folder sizes, refresh, and the commands for the selected items.
- **Path bar**: the folder you are in, as breadcrumbs, and a filter for the folder.
- **Object table**: the folders and objects of the current folder, sortable, with a check box on each row.
- **Inspector**: the selected item in tabs: **Overview**, **Preview**, **Headers**, **Tags**, **Permissions** and **Versions**.
- **Transfers panel**: queued, running and paused uploads and downloads, with progress and speed.
- **Status bar**: the item count and how long the last operation took. Click the time for **Recent operations**.

The command palette (`Ctrl+K`, `⌘K` on macOS) finds commands, tools and buckets by name.

## Where to go next

- [Install](install.md): choose and check the archive, first launch on each system.
- [Connect storage](connect.md): add an account for Amazon S3 or another service.
- [Browse](browse.md): buckets, folders, the object table, preview and the inspector.
- [Transfers](transfers.md): uploads, downloads, queues, conflicts and speed limits.
- [Objects](objects.md): headers, metadata, tags, ACLs, storage classes and encryption.
- [Versions](versions.md): older versions, delete markers and restoring.
- [Buckets](buckets.md): creating, emptying and deleting buckets, and bucket settings.
- [Links](links.md): presigned URLs, CDN links and CloudFront.
- [Sync](sync.md): sync jobs and upload rules.
- [Shortcuts](shortcuts.md): keyboard shortcuts and the command palette.
- [Data and security](privacy.md): where Pail keeps its data and what it sends.
- [Command-line reference](../cli.md): every `pail` command and flag.
