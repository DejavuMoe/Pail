# Transfers

Uploads, downloads, copies and moves run as tasks in the transfers panel, where you follow their progress, pause them and retry what failed. This page also covers what happens when a file already exists, how large files are sent in parts and how downloads are checked.

## Upload

### Files and folders

Open a folder of a bucket, then choose **Upload** in the toolbar:

- **Upload files…** picks one or more files;
- **Upload folder…** picks a folder and uploads it, with everything in it, into the current folder.

**Upload files…** is also in the menu of the tray icon.

### Drag and drop

Drag files or folders from your file manager into the object table. While you drag, the table says where they will go, for example "Drop to upload to my-bucket/photos/". Release to upload into the folder that is open.

Dragging rows of the object table onto a folder in the table moves them; see [Copy and move](#copy-and-move).

When the account has a CDN URL, the message that an upload finished offers **Copy CDN link**. CDN URLs are described in [Links](links.md).

## Download

### Objects and folders

Select objects or folders and choose **Download**. A folder is downloaded with everything below it.

Pail saves into **Settings › Downloads › Default download folder**. When it is empty (**Ask every time**), Pail asks for a folder each time.

Objects that an upload rule compressed are decoded again when they are downloaded.

### Versions and whole buckets

- An older version of an object is downloaded from the **Versions** tab of the inspector; see [Versions](versions.md).
- A whole bucket is downloaded with **Download bucket…** in the bucket's menu in the sidebar; see [Buckets](buckets.md). For a bucket that the account names with a folder, it covers only that folder.

### How downloads are checked

Pail writes each download to a temporary file and moves it to its final name only after it has been verified. An existing file is replaced only at that point, so a failed or canceled download leaves the old file as it was.

- A download whose size does not match what the server reported is refused, and no file is written. This happens behind a proxy that decompresses gzip responses, which changes the size of a gzip-encoded object.
- Some servers, such as some NAS devices, ignore requests for part of a file. Pail then downloads that file over a single connection.

## Copy and move

### Within an account

Select objects or folders, choose **Copy** or **Cut**, open the destination folder, and choose **Paste**. The destination can be another folder or another bucket of the same account.

- Dragging selected rows onto a folder moves them at once, as **Cut** and **Paste** do.
- **Rename** changes the name of one object or folder; see [Object properties](objects.md#rename).
- A move copies each object, checks the copy, and only then deletes the source. If an object with the same contents but different headers, metadata, storage class or tags already exists at the destination, the source is kept.

To copy or move everything in a bucket, use **Copy contents to…** or **Move contents to…** in the bucket's menu; see [Buckets](buckets.md).

### Between accounts

Copy or cut objects in one account, switch to another account in the sidebar, open a folder there, and choose **Paste**. This works between different providers too.

The objects pass through your computer, with their headers, metadata, storage class and tags. A move deletes the source only after the copy is checked.

The command line copies and moves only within one account.

## When a file already exists

**Settings › Transfers › When a file already exists** decides what happens when an upload, a download or a paste meets an item of the same name. It is also the default of **Copy contents to…** and **Move contents to…**, whose dialogs let you change it for that run.

| Policy | What happens |
| --- | --- |
| **Keep both (rename)** | The new item gets a numbered name, such as `report (1).pdf`. Nothing is replaced. This is the default. |
| **Skip existing** | The existing item is left alone and the new one is skipped. |
| **Overwrite** | The existing item is always replaced. |
| **Overwrite if newer** | The existing item is replaced only when the new one was modified more recently. |
| **Overwrite if size differs** | The existing item is replaced only when the sizes differ. |

Skipped items are reported as skipped, not as uploaded or downloaded.

## The transfers panel

The transfers strip appears at the bottom of the window with the first transfer. The upload and download counts in the status bar open the panel, and so does **Toggle transfers**.

The panel has three tabs:

| Tab | Shows |
| --- | --- |
| **Active** | Running, paused and queued transfers, in that order, with progress, bytes done and speed. |
| **Completed** | Finished tasks and their size, newest first. |
| **Failed** | Failed and canceled tasks, with the reason, newest first. |

**Clear finished** removes the completed and failed tasks from the list.

An upload or download of many files is one batch. **Active** shows it as one row with the files done, the bytes done, the speed and the time left; while Pail is still scanning a folder, the number of files ends in "+". Below the row are the files being transferred and one line that counts the files still queued. When the batch ends, a message says how many items succeeded and how many failed, for example "Upload finished with errors: 98 uploaded, 2 failed".

A running file says what it is doing while no data moves: **Preparing…**, **Compressing…**, **Verifying…**, **Decoding…** or **Finishing…**.

When the connection of a transfer moves no data for 60 seconds, Pail stops that request and sends it again. A transfer that keeps stalling fails and can be retried.

### Queues and limits

Uploads, downloads and other tasks, such as copying, moving or deleting objects, wait in separate queues. **Settings › Transfers** sets how many run at once:

| Setting | Meaning |
| --- | --- |
| **Concurrent uploads** | Upload tasks running at the same time, 1 to 64. |
| **Concurrent downloads** | Download tasks running at the same time, 1 to 64. |
| **Concurrent other tasks** | Copies, moves, deletes and other tasks, 1 to 64. |
| **Bulk workers per task** | Objects one bulk task handles in parallel, 1 to 64. |

The rest wait in the queue. Lowering a limit does not interrupt a running task.

### Pause, resume, cancel and retry

| Button | Effect |
| --- | --- |
| **Pause** | Pauses one upload or download. When resumed, the file starts again from the beginning. |
| **Pause this batch** | Pauses every file of a batch, and the scan of its folder. |
| **Resume** | Continues a paused file. |
| **Resume this batch** | Continues a paused batch. The scan continues where it stopped. |
| **Pause all** | Pauses every running and queued upload and download, and the scans of their folders. |
| **Resume all** | Continues every paused task. |
| **Cancel** | Stops a task. |
| **Cancel this batch** | Stops every file of a batch. Files already transferred stay. |
| **Retry** | Runs a failed or canceled task again. |

Retrying a move or rename whose copy succeeded but whose source could not be deleted finishes the move.

### After a restart

Transfers that are still queued, running or paused when Pail quits come back paused at the next start, and a message says how many. A folder that was still being scanned continues where the scan stopped, so the files it had not reached are transferred too. Choose **Resume** or **Resume all** to continue them. Sync runs are not saved.

> A paused file starts again from the beginning when it resumes, including after a restart.

While the tray icon is shown, closing the window keeps Pail running so that transfers finish; see [Tray icon](browse.md#tray-icon).

## Speed limits

**Settings › Transfers** has **Upload speed limit** and **Download speed limit**, in KiB per second. They apply to all accounts together, and 0 means no limit. They limit uploads and downloads of objects, not listings or other requests. A change takes effect at once.

## Large files and checksums

Large files are uploaded and downloaded in parts. **Settings › Transfers › Part size** sets the size of each part, from 5 to 5120 MiB. Larger parts mean fewer requests per file.

Each account has a **Checksum mode** under **Advanced** in its account dialog. It controls the checksums added to uploads and checked on downloads:

| Mode | Behaviour |
| --- | --- |
| **Auto** | The default. On AWS, the SDK default; on S3-compatible services, only the checksums the service requires. |
| **When required** | Only the checksums the service requires. Choose it if a service rejects uploads. |
| **When supported** | Always sends checksums. |

A multipart upload that is interrupted can leave parts on the server. **Multipart uploads** under **TOOLS** lists such uploads and aborts them; see [Buckets](buckets.md).

## Progress outside the window

While tasks are queued or running, the taskbar button on Windows and the Dock icon on macOS show their combined progress. On macOS the menu bar icon shows a percentage beside it, and transfers keep their full speed while the window is hidden.

## Command line

`pail cp` uploads, downloads and copies within S3. `pail mv` moves within S3: it copies, verifies, then deletes the source.

```sh
pail cp ./photos s3://my-bucket/photos/ -r
pail cp s3://my-bucket/photos/ ./downloads -r
pail cp s3://my-bucket/report.pdf s3://backup-bucket/2026/
pail mv s3://my-bucket/inbox/ s3://my-bucket/done/ -r --conflict skip
```

- The destination of an upload or an S3 copy is always a folder, and the source name is kept below it.
- The destination of a download is a local directory, created if missing.
- `--conflict` takes `skip`, `overwrite`, `rename`, `newer` or `different_size`. Without it, the policy saved in the desktop app's settings applies.
- `--decode gzip`, `zip` or `auto` decodes objects while downloading.
- `cp` does not ask for confirmation, even with `--conflict overwrite`. `mv` shows its scope and asks first.

The transfer settings are shared with the desktop app:

```sh
pail settings set upload-kibps 2048
pail settings set download-kibps 0
pail settings set part-size-mib 64
pail settings set conflict newer
```

`pail multipart ls` and `pail multipart abort` list and abort unfinished multipart uploads. Every flag is in the [command-line reference](../cli.md#command-details).
