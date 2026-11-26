# Sync jobs and upload rules

A sync job keeps a local folder and a bucket prefix in step in one direction, and shows you its plan before it changes anything. Upload rules set headers, storage class, encryption and compression on the objects you upload, according to the bucket and the key.

## Sync jobs

Open **Sync jobs** in the **TOOLS** group of the sidebar, from the command palette, or with `pail-desktop --tool sync`. Jobs are saved in Pail's configuration, so you can run them again from the app or from the command line.

### Create a job

1. Choose **New job**, or **New** when jobs already exist.
2. Type a **Job name**. Left empty, the job is named after the local folder.
3. Choose the **Direction**, the **Local folder** with **Choose…**, the **Account**, the **Bucket** and, optionally, a **Prefix** such as `photos/`.
4. Choose the **Mode** and how to **Compare** files, and add **Excludes** if you need them.
5. Choose **Save**.

**Delete job** removes only the saved job. Local files and objects stay where they are.

### Direction and mode

A sync goes one way. **Upload** makes the bucket prefix follow the local folder; **Download** makes the local folder follow the bucket prefix. The source is never changed.

| Mode | What it does |
| --- | --- |
| **New only** | Copies only files that are missing at the destination. |
| **New and changed** | Also replaces files that differ, according to the compare mode. |
| **Mirror** | Also deletes files at the destination that are missing from the source. |

> **Mirror** deletes extra files at the destination. Preview the job first and check the deletions in the plan.

### Compare modes

| Compare | A file counts as changed when |
| --- | --- |
| **Size and time** | Its size or modification time differs. A download replaces a local file only with a newer object. |
| **Size** | Its size differs. |
| **Checksum** | Its SHA-256 differs from the one an earlier sync saved on the object. |

Each mode has a limit:

- **Size and time** can miss an edit that keeps the file size.
- **Size** misses any edit that keeps the file size.
- **Checksum** needs the SHA-256 that Pail saves on the objects it syncs. Objects without it, such as files uploaded by other tools, count as changed and are transferred again.

### Exclude patterns

**Excludes** takes one pattern per line, such as `*.tmp`. A pattern is matched against the whole path relative to the job's folder, written with `/`:

- `*` matches any characters, including `/`;
- `?` matches exactly one character;
- a pattern ending in `/`, such as `cache/`, excludes that folder and everything in it.

Excluded files are skipped on both sides, so a mirror never deletes them.

Symbolic links in the local folder are not followed. They appear in the plan as skipped.

### Preview and run

**Preview** reads both sides and lists what the sync would do. It changes nothing.

- Counters above the list show the uploads, downloads, deletions and skipped files.
- The filter switches between **All**, **Changes**, **Deletes** and **Skipped**.
- Each row shows the **Path**, the **Reason** and the size.

**Run sync** then performs exactly the actions in the preview. A file that changed after the preview is not overwritten. Editing the job clears the preview.

When there is nothing to do, Pail says so. A sync without deletions starts at once. A sync with deletions first asks **Run sync?** and shows the job, the local folder, the remote prefix, the number of uploads, downloads and deletions, and the size. When a run ends, a message gives the result, and the run is listed in **Recent operations**.

### Deletions and the trash

An upload mirror deletes objects in the bucket. A download mirror moves local files to the Recycle Bin on Windows or the Trash on macOS and Linux instead of deleting them.

Keep a copy of important files before a download mirror.

### Run saved jobs from the command line

`pail sync` runs a sync directly or runs a saved job. The direction follows the order of the arguments: a local folder first uploads, a bucket address first downloads.

```sh
pail sync ./photos s3://my-bucket/photos/ --dry-run
pail sync ./photos s3://my-bucket/photos/ --mode mirror --exclude "*.tmp"
pail sync s3://my-bucket/photos/ ./photos --compare checksum
pail sync --job Photos --dry-run
pail sync --job Photos
```

| Flag | Values |
| --- | --- |
| `--mode` | `new`, `new_changed` (default) or `mirror`. |
| `--compare` | `size_time` (default), `size` or `checksum`. |
| `--exclude` | A pattern. Repeat it for several patterns. |
| `--no-rules` | Uploads without applying upload rules. |
| `--dry-run` | Prints the plan, changes nothing, asks nothing and exits with code 0. |

A run with deletions prints the scope and asks `Proceed? [y/N]`. Without a terminal it exits with code 2 unless you add `--yes`. `--job NAME` uses the job's account unless you give `--account`.

`pail sync job ls` lists the saved jobs. `pail sync job add NAME LOCAL s3://bucket/prefix/` saves an upload job, and `pail sync job add NAME s3://bucket/prefix/ LOCAL` a download job; they take the same flags, and the local folder must exist. `pail sync job rm NAME` removes a job after a confirmation. See [Command details](../cli.md#command-details), [Administration](../cli.md#administration) and [Confirmation](../cli.md#confirmation) in the command-line reference.

## Upload rules

Open **Upload rules** in the **TOOLS** group of the sidebar, from the command palette, or with `pail-desktop --tool rules`. Rules apply to uploads from the app, to `pail cp` and to `pail sync`.

Choose **Add rule** to create a rule. **Duplicate rule**, **Delete rule**, **Move rule up** and **Move rule down** act on the selected rule, and each rule has a switch to turn it off without deleting it. Choose **Save** to keep the changes or **Revert** to drop them.

Rules are checked from top to bottom. For each kind of setting (headers and metadata, storage class, encryption, compression), the first matching rule that sets it wins. Put specific rules above general ones.

### Match a bucket and key

| Field | Matches |
| --- | --- |
| **Name** | Only a label for the list. |
| **Bucket mask** | The bucket name, such as `media-*`. `*` matches any characters and `?` exactly one. Empty matches every bucket. |
| **Key mask** | The whole object key. `*` matches any characters, including `/`, so `images/*.png` also matches `images/a/b.png`. Empty matches every key. |

Both masks are case-sensitive.

### Headers and metadata

Under **HEADERS**, **Add header** sets one of Cache-Control, Content-Disposition, Content-Encoding, Content-Language, Content-Type or Expires. A Content-Type set here replaces the one from the MIME types table, except for ZIP output.

Under **METADATA**, **Add metadata** adds user metadata to the object. Metadata names are stored in lower case.

### Storage class and encryption

**Storage class** under **STORAGE** is **Default**, which leaves the choice to the server (normally STANDARD), one of STANDARD, STANDARD_IA, ONEZONE_IA, INTELLIGENT_TIERING, GLACIER_IR, GLACIER, DEEP_ARCHIVE and REDUCED_REDUNDANCY, or **Other…** for a class name that your provider defines.

**Encryption** under **ENCRYPTION** is one of:

- **None**;
- AES256, with keys managed by S3;
- aws:kms, with an optional **KMS key ID**;
- SSE-C, with a key you hold. Its **Key reference** names a saved secret that holds a base64-encoded 32-byte key, and the account's endpoint must use https.

### Compression

**Compression** under **COMPRESSION** is one of:

- **None**;
- GZIP, which keeps the key and sets `Content-Encoding: gzip`;
- ZIP or AES-256 ZIP, which upload one `.zip` object and add `.zip` to the key.

Do not combine GZIP or ZIP with a Content-Encoding header in the same rule.

**Level** is **Default**, **Huffman only**, **0 (store)** or 1 to 9; 9 compresses most and is slowest. For AES-256 ZIP, **Password** names a saved secret that holds the ZIP password; the rule stores only the name. **Manage secrets…** opens **Secrets**, where you add or change it.

ZIP files keep the files' own modification dates.

A proxy that decompresses gzip responses makes the size of a gzip-encoded object differ from what the server reports. Pail then refuses the download and writes no file.

### Test a rule

Type a **Test bucket** and a **Test key** to see what the rules apply to that upload. The test includes unsaved edits; the result is then labelled **Draft result**.

### Rules on the command line

`pail cp` and `pail sync` apply the same rules as the app. `pail sync --no-rules` uploads without them. When rules use secrets, such as a ZIP password, unlock the encrypted vault first.

`pail cp --decode gzip`, `zip` or `auto` decodes compressed objects while downloading:

```sh
pail cp s3://my-bucket/logs/app.log ./app.log --decode gzip
```

See [Command details](../cli.md#command-details) and [Encrypted vault](../cli.md#encrypted-vault) in the command-line reference.
