# Object properties

The inspector shows and edits the headers, metadata, tags and permissions of an object, and two dialogs change the storage class or the server-side encryption of many objects at once. This page also covers renaming, copying, moving and deleting objects, and restoring objects from S3 Glacier Flexible Retrieval and Deep Archive.

> Changing headers, metadata, the storage class or the encryption rewrites the object in place. Its modification time changes, and a bucket with versioning gets a new version.

## Headers and metadata

### One object

Select an object and open the **Headers** tab of the inspector. It has two parts.

**Headers** are the standard HTTP headers stored with the object and sent back when it is downloaded:

| Header | Use |
| --- | --- |
| Content-Type | The media type, such as `text/html` or `image/png`. Browsers use it to decide how to open the object. |
| Cache-Control | How long browsers and CDNs may cache the object, for example `max-age=3600`. |
| Content-Disposition | Whether browsers show the object (`inline`) or save it as a file (`attachment`). |
| Content-Encoding | `gzip` or `br`, only if the stored bytes really are compressed that way. |
| Content-Language | The language of the content, such as `en` or `zh-CN`. |
| Expires | The date after which caches treat the object as stale, written as an HTTP date. |

Clear a value to remove that header.

**User metadata** holds your own key-value pairs, sent back as `x-amz-meta-*` headers. Choose **Add metadata** to add one. Keys are saved in lower case, everything must be plain ASCII, and the total is limited to 2 KiB.

Choose **Save** to write the changes, or **Revert** to discard them. In a bucket with versioning the tab says **Saving creates a new version.**

### Several objects

Select several objects, or folders, and open **Headers**. A change applies to every selected object and to everything inside selected folders. A field where the objects differ shows **Mixed** and keeps each object's own value until you edit it. Choose **Update N items** to apply the change.

## Tags

The **Tags** tab edits the tags of one object: key-value labels that lifecycle rules, cost reports and access policies can match.

- Choose **Add tag**, enter a key and a value, then **Save**.
- An object can have up to 10 tags. Keys starting with `aws:` are reserved.
- Leaving the object discards unsaved tag edits.

Tags are edited one object at a time in the desktop app. The command line can merge tags into the existing ones; see [Command line](#command-line).

## Permissions

The **Permissions** tab shows the ACL of one object: its **Owner** and the **Grants**. **Copy owner ID** copies the owner's ID.

Choose a **Canned ACL** to replace the grants:

| Canned ACL | Grants |
| --- | --- |
| **Private** | Only the owner. |
| **Public read** | Anyone with the address can read the object. |
| **Public read and write** | Anyone can read and write the object. |
| **Authenticated read** | Any authenticated AWS user can read the object. |
| **Bucket owner full control** | The bucket owner gets full control. |

The canned ACL is applied when you choose it. The two public options ask first, with **Make public**. Grants that match none of these show as **Custom grants**.

When a bucket has ACLs turned off, the tab says **ACLs are disabled for this bucket**. When the ACL cannot be read, Pail does not change it. Some S3-compatible services do not support object ACLs; Pail then reports that the provider does not support it.

## Storage class

Select objects or folders and choose **Change storage class…** in the object menu. To change every object of a bucket, choose **Change storage class…** in the bucket's menu in the sidebar; the dialog's **Scope** then says **Whole bucket**.

| Class | S3 name |
| --- | --- |
| **Standard** | `STANDARD` |
| **Standard-IA** | `STANDARD_IA` |
| **One Zone-IA** | `ONEZONE_IA` |
| **Intelligent-Tiering** | `INTELLIGENT_TIERING` |
| **Glacier IR** | `GLACIER_IR` |
| **Glacier** | `GLACIER` |
| **Deep Archive** | `DEEP_ARCHIVE` |
| **Reduced Redundancy** | `REDUCED_REDUNDANCY` |

For a service with its own class names, choose **Other…** and type the name in **Custom storage class**, using capital letters, digits and underscores.

Colder classes cost less to store but more to read, and archived objects must be restored before they can be read. Each object is copied onto itself to change its class, so archived objects that are not restored, and objects encrypted with SSE-C, fail one by one while the others change.

## Server-side encryption

Select objects or folders and choose **Change encryption…**, or choose it in the bucket's menu for the whole bucket.

| Choice | Meaning |
| --- | --- |
| **None** | No encryption is set on the object. A default encryption of the bucket may still apply. |
| SSE-S3 | S3 manages the keys (AES256). |
| SSE-KMS | An AWS KMS key encrypts the object. Enter a key ID, key ARN or alias in **KMS key**, or leave it empty for the AWS managed key. |
| SSE-C | You supply the key. The object can then only be read with that key. |

SSE-C needs an endpoint that uses https, because the key is sent with every request. The key comes from a **Secret reference**: a saved secret that holds a base64-encoded 32-byte key. Secrets are added with **Manage secrets…** on the **Upload rules** page.

After changing the storage class or the encryption, Pail reads each object back. Objects where the service did not apply the change are reported as failed. Some S3-compatible services ignore storage classes and encryption.

## Rename

Select one object or folder and choose **Rename** (`F2`). Edit the name in the row and press Enter. A name cannot contain `/`. Rename is not available in the flat view or while deleted items are shown.

Renaming copies the object to the new name and then deletes the old one, the same way a move does.

## Copy and move

**Copy**, **Cut** and **Paste** copy or move objects and folders to another folder, another bucket or another account. Dragging rows onto a folder moves them. What happens when the destination already exists depends on the conflict policy. Both are described in [Transfers](transfers.md#copy-and-move).

When the task ends, a message says how many items were copied or moved, how many were skipped and how many failed.

## Delete

Select objects or folders and choose **Delete**. The dialog shows the account, the bucket, the folder, the first keys, and the number and size of the objects that will be deleted, including everything inside selected folders. The count says "at least" while Pail is still counting. Confirm with **Delete N items**.

The result is kept for each object, and the message says how many were deleted and how many failed, for example "Deleted 120 objects, 2 failed". A delete whose outcome is unknown is not sent again.

In a bucket with versioning, a delete keeps the older versions and adds a delete marker, so the object can be brought back; see [Versions](versions.md).

## Restore from archive

Objects in the storage classes `GLACIER` (S3 Glacier Flexible Retrieval) and `DEEP_ARCHIVE` must be restored before they can be read. A restore makes a temporary copy that can be read for a number of days; the object itself stays archived.

1. Select the objects or folders and choose **Restore from archive…**.
2. Enter **Days available**, from 1 to 365. The default is 7.
3. Choose a **Retrieval speed** and then **Restore**.

| Speed | Glacier Flexible Retrieval | Deep Archive |
| --- | --- | --- |
| **Expedited** | 1–5 minutes | Not available |
| **Standard** | 3–5 hours | Within 12 hours |
| **Bulk** | 5–12 hours | Within 48 hours |

Faster retrieval costs more, and the provider charges for every retrieval. Objects in other classes, and objects whose restore is already running, are skipped.

### Check the restore

Select the object and look at **Overview** in the inspector. The **Restore** row says **Restoring from the archive** while the restore runs, and "Restored, readable until" a date once the copy can be read.

## Command line

```sh
pail stat s3://my-bucket/report.pdf
pail props set s3://my-bucket/index.html --cache-control "max-age=3600" --content-type text/html
pail props set s3://my-bucket/site/ -r --metadata team=web
pail tags set s3://my-bucket/report.pdf project=alpha --merge
pail acl set s3://my-bucket/report.pdf private
pail storage-class s3://my-bucket/logs/ STANDARD_IA -r
pail archive-restore s3://my-bucket/2025/ -r --days 3 --tier bulk
pail rm s3://my-bucket/tmp/ -r
pail mv s3://my-bucket/draft.txt s3://my-bucket/final/
```

| Command | Does |
| --- | --- |
| `stat` | Prints size, time, ETag, storage class, headers, metadata, tags and encryption. |
| `props` | Reads or changes headers and user metadata. |
| `tags` | Reads, replaces, merges or clears tags. |
| `acl` | Prints the owner and grants, or applies a canned ACL. |
| `storage-class` | Changes the storage class. |
| `archive-restore` | Restores archived objects for `--days` days. |
| `rm` | Deletes objects, or a folder with `-r`. |
| `mv` | Moves within one account. |

`props set`, `storage-class` and `archive-restore` with `-r` apply to every object below a prefix, and print the scope and ask for confirmation first. `rm` always asks. The command line has no command for encryption. Every flag is in the [command-line reference](../cli.md#objects).
