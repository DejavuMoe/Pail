# Browse and preview

The sidebar lists the accounts and buckets you can open, the object table shows the folder you are in, and the inspector on the right shows details and a preview of what you select. This page covers moving between folders, selecting objects and reading them without downloading.

## The window

From left to right, the main window has:

- the **sidebar**, with the account menu at the top, the bucket list, the **TOOLS** group and **Settings** at the foot;
- the toolbar and the path bar above the object table;
- the **inspector**, which you show or hide with **Toggle inspector**.

The status bar runs along the bottom. The transfers strip appears above it with the first upload or download; see [Transfers](transfers.md).

## Sidebar

### Accounts

The account menu at the top of the sidebar shows the current account. Open it to switch to another account or to choose **Add account…**. Adding and editing accounts is described in [Connect](connect.md).

When the secret access key of an account is missing, the sidebar says **Secret missing**. Choose **Enter secret…** to type it again.

### Buckets

**BUCKETS** lists the buckets of the account, with their number beside the heading. Type in **Filter buckets** to narrow the list. A bucket shows its region only when it differs from the account's region; point at the bucket to see its region.

- A bucket with an alias shows the alias in the sidebar, the path bar and the command palette. Dialogs that act on the bucket, links and the command line keep the real name. Aliases are set with **Set alias…**, see [Buckets](buckets.md).
- If the account names its buckets, the sidebar shows only those, and each opens in the folder named for it. When the service refuses to list the buckets, the sidebar offers **Name buckets…**.
- The command palette (`Ctrl+K`, `⌘K` on macOS) lists the buckets under **Go to bucket**.

Click the **TOOLS** heading to fold the group. Pail remembers the choice, and the tool that is open stays visible. **Settings** keeps its own row below the tools.

## Folders and the path bar

S3 has no real folders: a folder is the shared prefix of the keys below it, such as `photos/2026/`. Pail shows these prefixes as folders.

- Double-click a folder, or select it and press Enter, to open it.
- The path bar shows the bucket and each folder on the way to the current one. Click a part of the path to go back to it.
- **Up one level** goes to the parent folder.
- **Refresh** lists the folder again.

For a bucket that the account names with a folder, such as `my-bucket/reports/`, the path bar starts at that folder.

## The object table

### Columns and sorting

The table has the columns **Name**, **Size**, **Type**, **Modified** and **Storage class**. Click a column heading to sort by it, and click it again to reverse the order.

Folders show a dash in **Size**, **Modified** and **Storage class**, because S3 stores none of these for a folder. After you calculate folder sizes, sorting by **Size** orders folders by their calculated size; see [Folder sizes](#folder-sizes).

### Large folders

A folder with many objects is listed page by page. The table fills as the pages arrive, and you can work with the rows already shown. While the list is loading, the status bar says **Loading…**, and totals in the inspector carry the note "Still loading — totals cover what is listed so far". If a later page fails, the rows already loaded stay and a message says what went wrong.

When a folder has room below its rows, the empty space shows **Drop files here to upload**.

### Selecting objects

| Action | Result |
| --- | --- |
| Click | Selects one row. |
| `Ctrl`+click | Adds or removes a row (`⌘`+click on macOS). |
| `Shift`+click | Selects the range from the row you last clicked. |
| Check box | Adds or removes that row only. |
| Header box | Selects every row, or clears the selection. |
| **Select all** | Selects every row (`Ctrl+A`, `⌘A` on macOS). |
| `Esc` | Clears the selection. |

You can also select by dragging:

- Drag down the check boxes to add every row you pass.
- Drag from the blank space below the rows to draw a rectangle. Hold `Ctrl` (`⌘` on macOS) while drawing to keep the current selection.
- Click the blank space to clear the selection.

When the rows fill the whole view there is no blank space to start a rectangle; drag down the check boxes instead.

With two or more items selected, a bar under the table shows how many are selected and their size, and offers **Download**, **Copy**, **Cut**, **Delete** and **Clear selection**. With one item selected, the status bar shows it instead.

Right-click a row for the commands that apply to the selection. Right-click the blank space below the rows for **Upload files…**, **New folder**, **Refresh** and **Select all**.

## Filter and flat view

**Filter folder** in the path bar narrows the table to the names in the current folder that contain what you type. Press `Ctrl+F` to go to it. When nothing matches, the table says **No matches**; choose **Clear filter** to see every row again. In a narrow window the field folds into a search button.

**Flat** lists every object below the current folder, including those in sub-folders, as one list. Folders are not shown in this view. It is not available while **Show deleted** is on.

## Inspector

The inspector shows what is selected:

- nothing: the current folder, with its number of items, folders and objects, their size, the region and the account;
- one folder: its prefix and URI, and its size once calculated;
- one object: six tabs, described below;
- several items: their number and size.

When folders are among the selected items, or in the current folder, the sum is labelled **Size of objects**. It counts only the objects, because S3 stores no size for a folder.

The tabs of one object are **Overview**, **Headers**, **Tags**, **Permissions**, **Versions** and **Preview**. Headers, tags and permissions are described in [Object properties](objects.md), versions in [Versions](versions.md).

### Overview

**Overview** shows the key, size, modification time, content type, ETag, storage class and encryption of the object, and its version ID in a bucket with versioning. When they apply, it also shows the object lock, the legal hold, and the state of a restore from an archive. Buttons beside the fields copy the key, the URI, the URL, the CDN URL and the version ID.

### Preview

**Preview** reads the start of the object and shows it without saving a file.

| Kind | Shown as |
| --- | --- |
| JSON | Indented text. |
| CSV, TSV | A table of the first 200 rows. |
| XML, text | Text. Markdown, YAML, logs and source files count as text. |
| Image | PNG, JPEG, GIF or WebP, up to 8 MiB. |
| Other | A hex dump of the first 4 KiB. |

Pail decides by the file extension and the content type. A file without an extension whose first bytes read as text is shown as text. Text previews stop after the first 1 MiB; the note under the preview says when the object goes on.

To look closer at an image, zoom up to 8 times:

- pinch on a touchpad;
- hold `Ctrl` and turn the mouse wheel;
- use **Zoom in** and **Zoom out**.

Drag or scroll a zoomed image to move it. Double-click it to fit the panel again.

## Copying keys and links

**Copy link** in the object menu holds:

| Command | Copies |
| --- | --- |
| **Copy key** | The object key, such as `photos/cat.jpg`. |
| **Copy URI** | The S3 URI, such as `s3://my-bucket/photos/cat.jpg`. |
| **Copy URL** | The public S3 address of the object. |
| **Copy CDN URL** | The address on the account's CDN, when the account has one. |
| **Copy signed URL…** | A presigned URL with an expiry, see [Links](links.md). |

A public URL opens only if the object is readable without credentials. CDN URLs and presigned URLs are described in [Links](links.md).

**Copy** and **Cut** in the same menu copy the objects themselves, not text; see [Transfers](transfers.md#copy-and-move).

## New folders

Choose **New folder**, enter a **Folder name**, and confirm. Pail creates an empty object whose key ends with `/`, which S3 tools show as a folder. The name cannot contain `/`. To create nested folders, open the parent first.

## Folder sizes

S3 stores no size for a folder, so **Size** shows "–" until you calculate it.

- Right-click one or more folders and choose **Calculate size**.
- Choose **Calculate folder sizes** in the toolbar to size every folder shown.

The size counts the current objects under the folder. Older versions, delete markers and unfinished multipart uploads are not counted. A large folder takes a while, because every key below it is listed. You can calculate a folder again after its content changes, and retry a calculation that failed. The inspector shows the object count and size of a selected folder.

## Status bar and recent operations

The status bar shows the number of items in the folder, or "N items of M" while a filter is on. With one item selected it also says so, with the size of an object.

It also shows how long the last successful operation took: loading the bucket list, opening a folder, an upload, a download or a delete. The upload and download counts open the transfers panel.

Click the time to open **Recent operations**. Each line shows an operation, whether it **Succeeded**, **Failed** or was **Canceled**, how long it took and when it ended. **Clear** empties the list. The list is kept in memory and is empty again after you quit Pail.

**Settings › Diagnostics** has two settings for it:

| Setting | Effect |
| --- | --- |
| **Recent operations to keep** | 1 to 1000; 100 by default. |
| **Keep on account switch** | Off by default: the list is cleared when you choose another account. On: it keeps every account's operations and names the account of each. |

The command line has no list of recent operations.

## Tray icon

Pail puts an icon in the notification area on Windows, the menu bar on macOS and the system tray on Linux. Closing the window hides it, and Pail keeps running so that transfers can finish. The icon's menu has:

- **Open Pail**;
- the transfer progress;
- **Show transfers**;
- **Upload files…**;
- **Quit Pail**.

While the window is hidden, messages about finished tasks arrive as system notifications.

**Settings › Appearance** has two switches:

- **Show in notification area** (**Show in menu bar** on macOS) turns the icon off. Closing the window then quits Pail.
- **Quit when closed and idle** makes closing the window quit Pail when no transfer is running. While a transfer runs, Pail still stays in the tray. It is off by default.

Starting Pail a second time, or clicking its Dock icon on macOS, brings the open window back instead of opening another copy. On Linux the icon needs `libayatana-appindicator3`; without it, closing the window quits Pail.
