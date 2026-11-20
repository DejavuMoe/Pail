# Versions

In a bucket with versioning, Pail lists every version and delete marker of an object, downloads or restores older versions, brings deleted objects back, and permanently deletes only the versions you choose.

## Before you start

Versions exist only in a bucket that has versioning turned on. Versioning is turned on in **Bucket settings…**, or when a bucket is created with Object Lock; see [Buckets](buckets.md).

With versioning on:

- uploading an object with an existing key keeps the previous contents as an older version;
- deleting an object keeps its versions and adds a delete marker as the newest entry;
- changing headers, metadata, the storage class or the encryption creates a new version.

Suspending versioning stops new versions from being kept; the versions already stored remain.

## Versions of an object

Select an object and open the **Versions** tab of the inspector. It lists the versions of that object. Each row shows the size, or **Delete marker**, the modification time and the start of the version ID. The current version carries the **Latest** badge.

The buttons on a row:

| Button | Effect |
| --- | --- |
| **Download this version** | Saves that version as a file. |
| **Signed URL for this version** | Creates a presigned URL for that version; see [Links](links.md). |
| **Restore this version** | Makes that version the current one. Not shown on the latest version. |
| **Delete this version permanently** | Deletes that version or delete marker. |

Delete markers have only the delete button.

### Choose several versions

Click rows to select them, and click a selected row again to deselect it. With versions selected, the tab says how many and offers:

- **Download chosen versions**, for the selected versions that are not delete markers;
- **Delete chosen versions permanently**.

### Download a version

Downloaded versions are saved into **Settings › Downloads › Default download folder**, or into a folder Pail asks for when that setting is empty. They run in the transfers panel like any other download; see [Transfers](transfers.md).

### Restore a version

**Restore this version** makes the older version the current one. The other versions are kept, so you can go back again.

### Delete versions permanently

**Delete this version permanently** and **Delete chosen versions permanently** ask first and show the object and the versions concerned. When some versions cannot be deleted, the message says how many failed.

> A version or delete marker deleted permanently cannot be brought back.

## Deleted objects

### Show deleted

**Show deleted** in the path bar lists the deleted items of the current folder: objects whose latest version is a delete marker. It is available only in a bucket with versioning; point at it while it is unavailable to see why. While it is on, **Flat** is not available.

When nothing in the folder has a delete marker, the table says **No deleted items**. Selecting a deleted item shows in the inspector when it was deleted, its size and its key.

### Restore deleted objects

- Select deleted items and choose **Restore**.
- Choose **Restore all in this folder** to restore every deleted item shown.

Both restore at once, without asking. A restored object comes back with the contents it had before it was deleted, and its version history is kept. The message says how many items were restored and how many failed.

## Command line

```sh
pail versions s3://my-bucket/photos/
pail versions s3://my-bucket/photos/cat.jpg
pail restore s3://my-bucket/photos/cat.jpg --version-id 3HL4kqtJlcpXroDTDmJ
pail restore s3://my-bucket/photos/cat.jpg --deleted
pail restore s3://my-bucket/photos/ --deleted -r
pail rm s3://my-bucket/photos/cat.jpg --versions
```

| Command | Does |
| --- | --- |
| `versions` | Lists versions and delete markers of one folder level, or of one key. |
| `restore --version-id` | Makes an old version the current one. |
| `restore --deleted` | Undeletes a key whose latest entry is a delete marker. |
| `restore --deleted -r` | Undeletes every such key below a prefix. |
| `rm --versions` | Permanently deletes every version and delete marker of one key. |

`stat`, `cat`, `tags`, `acl` and `presign` take `--version-id` to work on one version:

```sh
pail cat s3://my-bucket/notes.txt --version-id 3HL4kqtJlcpXroDTDmJ > notes-old.txt
```

`restore` keeps the version history. `rm --versions` shows its scope and asks for confirmation first. See the [command-line reference](../cli.md#command-details).

## Limits

- `pail versions` lists one folder level, and `pail rm --versions` takes a single key.
- The command line has no command to delete a single chosen version.
- Older versions and delete markers are not counted in folder sizes or in `pail du`.
