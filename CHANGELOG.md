# Changelog

English | [简体中文](CHANGELOG.zh-CN.md)

All notable changes to this project are documented in this file. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Fixed

- Switching buckets while editing object tags could apply the old tags to an object with the same key in the new bucket. Leaving the object now clears the unsaved tags.
- Saving accounts in two open instances could discard another instance's changes or restore an obsolete credential reference. Account writes now preserve the latest saved accounts and credentials.
- Moving onto an object with identical contents but different properties could delete the source and lose its metadata or tags. The source is now kept when the destination's properties differ.
- Invalid or overflowing day values for `multipart abort --older-than` could abort recent uploads. These values are now rejected before any upload is aborted.
- An upload conflict could be reported as a successful retry when another object had the same size and metadata but different contents. The stored contents are now checked before reporting success.
- Changing a setting back while its previous save was pending could leave a different value on disk. Saves now follow the order of changes.
- Reopening Multipart uploads or CloudFront could show results from the previous account or bucket. Old requests no longer replace the current view.
- Finishing an earlier bucket creation, account save or CloudFront invalidation could close or change a newly opened dialog. The new dialog now keeps its state.
- Finishing a tag save could discard edits to another object or edits made after pressing Save. Those drafts are now preserved.
- Finishing a header save, rename, storage class change or version restore or deletion could discard unsaved header or tag edits to another object, or edits made while it ran. A finished rename also moved the selection away from the object you had picked meanwhile. The other object's edits and selection are now kept, and the changed object shows its new values without losing your edits.
- Saving MIME types or a sync job could erase changes typed while the save was pending. Those changes now remain available for the next save.
- Retrying a failed transfer could finish without refreshing the file list or reporting its result. Retried transfers now update the list and recent operations.
- Failed, partially failed or canceled multipart aborts could be reported as fully successful. The notification now reports the actual result.
- Slow CloudFront responses could be discarded by every subsequent poll, leaving invalidations stuck in progress. Polling now waits for the current request to finish.
- The tray's Upload command could remain disabled after returning to a bucket. Its availability now follows navigation.
- A download that failed to create a folder could not be retried after removing the obstruction. The failed folder creation can now be retried.
- Checksum sync repeatedly downloaded unchanged compressed objects. Download comparisons now use the checksum of the stored contents, including objects uploaded by earlier versions.
- On Windows, an upload mirror could preview deletion of remote objects with names such as `CON`, then refuse to run. Remote-only deletion now accepts those object names.
- Public links could misinterpret escaped characters in an endpoint's path. Links now preserve the path's escaping.
- JSON output failures could leave the command line reporting success. The command now exits with an error when it cannot write the output.
- Saving vault secrets in two open instances could lose entries that were reported as saved. Vault writes now keep every saved entry, and an instance whose master password was changed elsewhere asks to unlock again.
- Saving accounts or vault secrets could wait forever while another Pail window or command was stuck saving. The save now gives up after a short wait and says another Pail process is saving settings.
- On macOS, moving two files with the same name to the Trash at the same time could overwrite the first one in the Trash. Each file now keeps its own entry.
- While the window was hidden in the tray, finished tasks such as emptying a bucket or renaming did not send their notification or refresh the list until the window was shown again. They now do so while the window is hidden.

## [0.6.0] - 2026-11-10

### Changed

- The bucket settings dialog is one sheet in one colour: the section list sits on the dialog's own background, separated from the form by a line, and its names line up with the title.
- Selected items in the sidebar, the bucket settings list, the version list and the sync job and upload rule lists no longer have a coloured bar on their left edge.
- The interface no longer animates hovers, selections, switches, check boxes, panels and counts; they change at once. Dialogs, menus and notifications only fade in where they appear, and close at once.
- The context menu of objects is shorter. It lists only what applies to the selected items, groups the copy commands for keys, URIs, URLs and signed URLs under **Copy link**, and shows **Upload files…**, **New folder**, **Refresh** and **Select all** only when you right-click the empty space below the rows.
- Fewer questions before acting. Dragging items onto a folder moves them at once, as cut and paste do. **Restore all in this folder** restores at once. Saving headers for several objects happens when you press **Update N items**. A sync whose preview deletes nothing runs when you press **Run sync**; a sync that deletes files still asks first.
- The divider above the transfers panel is grey while you point at it or drag it, like the other dividers, instead of blue.

### Fixed

- Retrying a move or rename whose copy succeeded but whose source could not be deleted now finishes it. Before, the source stayed, a "name (1)" copy appeared, or the rename failed with "already exists".
- The Permissions and Versions tabs could show the previous bucket's data for an object of the same name, and Versions did not show the version a header save had just made.
- Leaving Sync jobs or Upload rules by the sidebar, the command palette or an account switch threw away unsaved edits. They are now kept until you come back.
- Changing only the proxy password took effect only after a restart.
- With the encrypted vault, a long transfer could let the vault lock itself, and the transfers queued behind it failed with "the encrypted vault is locked". The vault no longer locks while transfers run or wait.
- CloudFront invalidation from the browser now escapes object keys, so keys with `%`, `?`, `#` or `*` invalidate the right object instead of another one or an error.
- In Chinese, moving a bucket's contents said "copied", and a failed move said "copy failed".
- A signed URL generated just before its dialog was closed could show up in the dialog opened next, for another object.
- With every selected row hidden by the folder filter, Delete, Copy, Cut and Download looked enabled and did nothing.
- In the account dialog, Test connection showed no progress or result after a failed save, and its result stayed after the tested settings were changed.
- After switching accounts, a bucket with the same name opened showing deleted items.
- An account that lost its secret kept its old bucket list beside the "enter the secret" message.
- An object whose name starts or ends with a space was renamed without that space when its name field was left unchanged.
- A failed switch of the secret storage could leave the vault unlocked with no automatic lock.
- Moving local files with long Chinese or other non-ASCII paths to the Recycle Bin was refused as too long.
- Error messages written to the log are redacted before they are shortened, so a secret at the cut can no longer leak in part.
- ZIP files made by upload compression rules carry the files' own dates instead of 1979-11-30.
- `pail sync --job NAME --no-rules` ignored `--no-rules`.
- Listing unfinished multipart uploads of an empty folder failed with "access denied" for a key limited to that folder.
- `pail versions s3://bucket/key` printed nothing; it now lists the versions of that key.
- With stdin redirected from NUL or `/dev/null`, a command that needs confirmation exited 1 after printing a question instead of exiting 2.
- `pail sync` into a local folder that does not exist created it before the plan was shown, and `--dry-run` failed on it.
- A run whose items were all skipped or failed exited 1; skipped items count as success, so it now exits 3.
- The usage of `pail du` showed the path as optional; it needs a bucket.
- The Windows desktop app showed version 0.1.0.0 in its file properties.
## [0.5.3] - 2026-11-10

### Added

- Accounts can name their buckets. When an access key may not list the buckets of the account, or may only use one folder of a bucket, enter the buckets in **Buckets** of the account dialog, as `my-bucket` or `my-bucket/reports/`, or pass `--bucket` to `pail account add`. The connection test then lists those folders instead of the buckets, the sidebar and `pail buckets` show the named buckets, and each opens in its folder. The path bar starts at that folder, and **Download bucket…**, **Copy contents to…** and the other whole-bucket actions of the sidebar cover only that folder. When the service refuses to list the buckets, the sidebar offers **Name buckets…**. Thanks to @xjetry for contributing this in [#7](https://github.com/DejavuMoe/Pail/pull/7).

## [0.5.2] - 2026-11-09

### Changed

- The window stays more responsive while many objects are selected, such as every object in a large folder.

### Fixed

- Closing the New Folder dialog while a folder was still being created, then opening it again, could close the new dialog or show the earlier error in it. The reopened dialog now stays as you left it, and the earlier result shows as a notification.

## [0.5.1] - 2026-11-08

### Added

- Quit when closed and idle. With the tray icon showing, turn on **Settings › Appearance › Quit when closed and idle** (or `pail settings quit-when-idle on`) and closing the window quits Pail when no transfer is running. While a transfer runs, closing the window still keeps Pail in the tray. It is off by default.
- A drop hint under short folder listings. When a folder's items leave empty space below the list, "Drop files here to upload" shows there.

### Changed

- In a narrow window, the folder filter folds into a search button so that the folder path stays readable. Click it or press `Ctrl+F` to open it; it folds again when you leave it empty.
- In a narrow window, the object list shows Name, Size and Modified in full without scrolling sideways.
- The toolbar shows Download, Copy, Cut, Rename and Delete only while something is selected, and Paste only while there is something to paste. These buttons now come after Refresh.
- The transfers strip at the bottom of the window appears with the first transfer. The upload and download counts in the status bar open the transfers panel.
- The status bar shows the item count and the selection. The account name stays in the sidebar and the region in the inspector.
- The bucket list shows a bucket's region only when it differs from the account's region. Hover a bucket to see its region.
- The TOOLS group in the sidebar can be folded by clicking its heading. Pail remembers the choice; the tool that is open stays visible.
- Sync jobs and upload rules show one empty page with a single button to create the first item.
- Upload rules hide Save and Manage secrets while no rule is selected and nothing is left to save. The object key field of the rule test fills the width of the window.
- Folders show a dash in the Modified and Storage class columns, as they already did in Size.

## [0.5.0] - 2026-11-06

### Added

- Recent operations. Click the time in the status bar to open a list of the latest operations: loading the bucket list, opening a folder, uploads, downloads and deletes. Each line shows whether it succeeded, failed or was canceled, how long it took and when it ended. The status bar itself still shows the last successful one. The list is kept in memory and is empty again after you quit Pail. **Settings › Diagnostics** sets how many operations it keeps (100 by default, up to 1000) and whether it is kept when you switch account (by default it is cleared).
- Accounts and the encrypted vault on the command line. `pail account` adds, edits, shows, tests and removes accounts. It asks for the secret access key without echo, or reads it from stdin with `--secret-stdin`, and never prints it. `pail vault` creates the encrypted vault, changes its master password, moves the secrets between the vault and the system keychain, and resets a vault whose password is forgotten.
- Object commands on the command line. `pail stat` shows an object's properties, `pail cat` writes its bytes to stdout, `pail mkdir` creates a folder and `pail du` totals the size below a prefix. `pail tags`, `pail acl`, `pail props` and `pail storage-class` read and change tags, ACLs, headers, metadata and storage classes.
- More administration on the command line. `pail bucket alias` sets a bucket's display name, `pail multipart` lists and aborts unfinished multipart uploads, `pail sync job` lists, adds and removes saved sync jobs, `pail cloudfront` lists distributions and invalidates paths, and `pail settings` reads and changes the desktop app's settings. `pail help COMMAND` prints the usage of one command.

### Changed

- Selecting every item of a folder with thousands of objects, and loading such a folder, no longer make the window stutter: the questions Pail asks about the selection on every redraw are answered without sorting it, and while a large folder is still loading the list is rebuilt a few times a second instead of after every page.

### Removed

- Automatic migration of data from Pail's name before its first release, `openS3`: the `openS3` configuration folder, the `openS3` keychain entries, `opens3-vault` vault files and `opens3-*` object metadata are no longer read. Releases of Pail never used that name.
- `pail-desktop --demo` is no longer part of the release builds, which also makes the programs smaller. Developers still get it by building from source with `-tags pail_demo`.

### Fixed

- The counts at the bottom of the window no longer jitter. While a folder is loading, its item count changes at once instead of rolling with every page, digits no longer spill above the status bar, and the collapsed transfers strip rolls each of its three counts separately.
- Downloads from servers that ignore range requests, such as some NAS devices, no longer fail with "EOF" once the file is larger than one part. Pail now downloads such a file over a single connection.
- Canceling a download from such a server shows "canceled" again, not the error "the service ignored the Range header".

## [0.4.2] - 2026-11-05

### Added

- Bucket aliases. Right-click a bucket and choose Set alias… to show a name you recognise in the sidebar, the path bar and the command palette. The bucket is not renamed: the dialogs that act on a bucket, links and the command line keep its real name.
- Proxy server. **Settings › Network** sends the requests of all accounts through a proxy: choose HTTP, HTTPS or SOCKS5, enter the host and port, and optionally a user name and password. The password is kept in the system keychain or the encrypted vault, with your other secrets. Addresses on your own computer connect directly. Leave it on From environment to use the `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY` environment variables as before. The command line uses the same setting.
- The status bar shows how long the last operation took: loading the bucket list, opening a folder, an upload, a download or a delete.

## [0.4.1] - 2026-11-04

### Fixed

- The counts at the bottom of the window no longer jitter. While a folder is loading, its item count changes at once instead of rolling with every page, digits no longer spill above the status bar, and the collapsed transfers strip rolls each of its three counts separately.
- Folder sizes: Calculate size and Calculate folder sizes were greyed out once a size had been calculated, so you could not measure a folder again after its content changed. They now stay available and measure the folder again.

## [0.4.0] - 2026-11-02

### Added

- Folder sizes. S3 stores no size for a folder, so the Size column shows "–" until you calculate it. Right-click one or more folders and choose Calculate size, or use the Calculate folder sizes button in the toolbar to size every folder shown. The size counts the current objects under the folder; older versions, delete markers and incomplete multipart uploads are not counted. Very large folders take a while, and a failed calculation can be retried. The inspector shows the object count and size of a selected folder. Sorting by Size orders folders by their calculated size.
- A tray icon: the notification area on Windows, the menu bar on macOS and the system tray on Linux. Closing the window hides it and Pail keeps running so transfers can finish; choose Quit Pail from the icon's menu to quit. The menu shows the transfer progress and offers Open Pail, Show transfers and Upload files. While the window is hidden, completion messages arrive as system notifications. Turn the icon off under Settings › Appearance to quit on close as before. On Linux the icon needs libayatana-appindicator3; without it, closing the window quits.
- Starting Pail a second time, or clicking its Dock icon on macOS, brings the existing window back instead of opening another copy.

### Changed

- The window opens with its content already drawn on every platform.
- The toolbar shows only icons when the window is narrow, including at the smallest window size.
- macOS: lower CPU and power use while idle.
- Windows 11: menu labels are no longer cut off at high display scaling.

### Fixed

- The counts at the bottom of the window no longer jitter. While a folder is loading, its item count changes at once instead of rolling with every page, digits no longer spill above the status bar, and the collapsed transfers strip rolls each of its three counts separately.
- macOS: the app icon is no longer larger than other apps' icons in the Dock and Finder.

## [0.3.3] - 2026-10-31

### Changed

- Screen readers announce the Theme, Language and account choices in menus as a group of options with one selected, instead of as separate check boxes.

### Fixed

- The counts at the bottom of the window no longer jitter. While a folder is loading, its item count changes at once instead of rolling with every page, digits no longer spill above the status bar, and the collapsed transfers strip rolls each of its three counts separately.
- Windows: while you resize the window, its content no longer shows enlarged and cut off at the right and bottom, and newly exposed edges show the background colour instead of black.
- Windows: with "Show accent colour on title bars and window borders" on, no accent strip appears at the top of the window.
- Windows: short Chinese labels are no longer cut off with an ellipsis.
- macOS: ⌘C, ⌘X, ⌘V and ⌘A copy, cut, paste and select objects when no text field has the focus.
- macOS: the window no longer shakes while you drag it by the toolbar or the sidebar header.
- Linux: closing the window with its own close button no longer crashes the app, and a quick touchpad tap no longer leaves the pointer stuck pressed.

## [0.3.2] - 2026-10-26

### Added

- The object table has a check box on each row and a select-all box in its header. With two or more items selected, a bar under the table shows their count and size and offers Download, Copy, Cut, Delete and Clear selection. Esc clears the selection.
- Drag down the check boxes to add the rows you pass to the selection, or drag from the blank space below the rows to select them with a rectangle. Hold Ctrl (⌘ on macOS) while drawing the rectangle to keep the current selection. A click in the blank space clears the selection.

### Fixed

- The counts at the bottom of the window no longer jitter. While a folder is loading, its item count changes at once instead of rolling with every page, digits no longer spill above the status bar, and the collapsed transfers strip rolls each of its three counts separately.
- Large upload and download batches now report their full result even after older tasks leave the history, so an early failure is no longer hidden.
- Skipped files no longer count as uploaded or downloaded in completion notifications.
- Command-line summaries no longer undercount bulk operations with more than 10,000 results.
- Saved sync jobs select their own account when several accounts are configured.
- Upload mirrors preserve remote objects below skipped local directory links, allowing the remaining sync actions to run.
- Skipping an existing compressed upload no longer compresses the source or asks for its ZIP password.
- The command line respects `--` before paths beginning with a dash.

## [0.3.1] - 2026-10-23

### Added

- Image previews in the inspector can be zoomed up to 8 times with a touchpad pinch, Ctrl and the mouse wheel, or the zoom buttons. A zoomed image pans by dragging or scrolling; double-click it to fit the panel again.

### Changed

- Sorting large object lists uses less temporary memory for names containing ASCII uppercase letters.

### Fixed

- The counts at the bottom of the window no longer jitter. While a folder is loading, its item count changes at once instead of rolling with every page, digits no longer spill above the status bar, and the collapsed transfers strip rolls each of its three counts separately.
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

- The counts at the bottom of the window no longer jitter. While a folder is loading, its item count changes at once instead of rolling with every page, digits no longer spill above the status bar, and the collapsed transfers strip rolls each of its three counts separately.
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
