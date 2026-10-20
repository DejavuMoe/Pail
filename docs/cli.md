# Pail command line (`pail`)

English | [简体中文](cli.zh-CN.md)

`pail` works with the same accounts, saved sync jobs and secrets as the desktop app. It shares the desktop app's configuration directory and secret store (the OS keychain or the encrypted vault). Accounts cannot be added or changed from the command line; do that in the desktop app.

Release archives use versioned filenames, for example `pail-v<version>-windows-amd64.exe`. The examples below assume you installed the program as `pail` (`pail.exe` on Windows); you can also run the full filename.

```text
pail accounts
pail ls s3://my-bucket/photos/ -r
```

S3 paths are written `s3://bucket/key-or-prefix/`. Any other argument is a local path.

Flags accept one or two leading dashes: `-json` and `--json` are the same. This page uses two. `pail version` prints `pail <version>`. The command line has no `--version` flag; the desktop app does.

## Global flags

Global flags can be placed before or after the command name.

| Flag | Meaning |
| --- | --- |
| `--account` | Account name or ID. Default: the only configured account. With several accounts the command fails (exit 2) and lists them. |
| `--config-dir` | Configuration directory. Default: the desktop app's, `<user config dir>/Pail`. |
| `--json` | Newline-delimited JSON on stdout: one object per listing row or item result, then a final object with `"type":"summary"`. A failure ends with an object with `"type":"error"`. |
| `--quiet` | No per-item lines or progress on stderr. |
| `--yes` | Answer yes to every confirmation. |
| `--timeout` | Abort after this long, for example `10m`. Running tasks are cancelled. `0` means no limit. |
| `--vault-password-stdin` | Read the encrypted vault's master password from the first line of stdin. See Encrypted vault. |

## Commands

| Command | What it does |
| --- | --- |
| `accounts` | List accounts with name, ID, provider, endpoint and region. |
| `vault` | Show the secret backend and the encrypted vault state. |
| `buckets` | List buckets with their region. |
| `ls` | List objects and folders. |
| `cp` | Copy from local to S3 (upload), from S3 to local (download), or within one account on S3. |
| `mv` | Move objects within S3. |
| `rm` | Delete objects, folders or all versions of a key. |
| `mb` | Create a bucket. |
| `rb` | Delete an empty bucket. |
| `presign` | Create a presigned URL. |
| `url` | Print an object's CDN or public address without sending a request. |
| `versions` | List versions and delete markers. |
| `restore` | Make an old version current, or undelete keys. |
| `sync` | Synchronise a local folder and an S3 prefix in one direction. |
| `version` | Print the version. |

Usage:

```text
pail accounts
pail vault status [--vault-password-stdin]
pail buckets
pail ls [s3://bucket/prefix/] [-r]
pail cp SRC DST [-r] [--conflict POLICY] [--decode gzip|zip|auto]
pail mv s3://SRC s3://DST [-r] [--conflict POLICY]
pail rm s3://bucket/key [-r] [--versions]
pail mb s3://bucket [--region REGION] [--object-lock]
pail rb s3://bucket
pail presign s3://bucket/key [--expires 1h] [--method GET|PUT] [--version-id V]
pail url s3://bucket/key [--kind cdn|public]
pail versions s3://bucket/prefix/
pail restore s3://bucket/key --version-id V
pail restore s3://bucket/prefix/ --deleted [-r]
pail sync LOCAL s3://bucket/prefix/ [SYNC FLAGS]
pail sync s3://bucket/prefix/ LOCAL [SYNC FLAGS]
pail sync --job NAME [--dry-run]
pail version
```

### Command details

- `-r` (also `-recursive`) works the same way in `ls`, `cp`, `mv`, `rm` and `restore`.
- `vault status` shows the secret backend (`keyring` or `vault`), whether `vault.json` exists and the desktop app's auto-lock setting. With `--vault-password-stdin` it also checks the master password. With `--json` it prints one object (shape below).
- `ls` shows folders as `PRE name/` and sizes in a readable form (raw bytes with `--json`). Without a path it lists buckets. `-r` lists every key below the prefix. A key without a trailing `/` that is not a folder lists that single object.
- `cp` and `mv` take `--conflict` with `skip`, `overwrite`, `rename`, `newer` or `different_size`. When it is omitted, both use the default saved in the desktop app's settings (initially `rename`). `mv` copies, verifies, then deletes the source.
- `cp --decode` (`gzip`, `zip` or `auto`) decodes objects while downloading. It has no effect on uploads and S3 copies.
- Destinations: the destination of an upload or an S3 copy is always a folder (a missing trailing `/` is added) and the source name is kept below it, like `cp -r src dst/`. The destination of a download is a local directory, created if missing.
- `rm -r` treats the key as a folder; `s3://bucket` with `-r` means the whole bucket. `rm --versions` permanently deletes every version and delete marker of one key.
- `mb` creates the bucket in the account's region unless `--region` is given. `--object-lock` also enables versioning.
- `presign` creates a URL valid for `--expires` (default `1h`) for `--method` `GET` (default) or `PUT`.
- `url` prints the CDN link when the account has a CDN URL for that bucket (see CDN URLs), otherwise the public S3 address. `--kind cdn` exits 1 when none is configured; `--kind public` always prints the S3 address. A prefix instead of a key is a usage error (exit 2). With `--json` it prints one object (shape below), then the summary.
- `versions` lists one folder level and includes delete markers.
- `restore --version-id` makes an old version the current one. `restore --deleted` undeletes the keys under a prefix whose latest entry is a delete marker (add `-r` to include subfolders). Version history is kept.
- `sync` copies in one direction. `--mode` is `new`, `new_changed` (default) or `mirror`; `mirror` also deletes. `--compare` is `size_time` (default), `size` or `checksum`. `--exclude GLOB` skips files whose relative path matches the glob and can be repeated. `--no-rules` does not apply upload rules. `--dry-run` prints the plan and exits without changing anything.
- `sync --job NAME` runs a job saved in the desktop app, using that job's account unless `--account` is given.

JSON shapes of `vault status` and `url` with `--json`:

```text
{"type":"vault","backend":...,"vaultExists":...,"autoLockMinutes":...}
{"type":"url","kind":"cdn","url":...}
```

`kind` is `cdn` or `public`.

## CDN URLs

An account can have a CDN base URL, for example `https://s3-cdn.example.com`, and optionally a list of the buckets it serves (default: all). Without placeholders the link is `<base>/<bucket>/<key>`, so `s3://assets/logo.svg` becomes:

```text
https://s3-cdn.example.com/assets/logo.svg
```

A template with `{key}` (and optionally `{bucket}`) is filled in instead, for example:

```text
https://{bucket}.cdn.example.com/{key}
```

The key is percent-encoded like the public URL, keeping `/`. The CDN URL is set in the desktop app's account dialog; the command line only reads it.

## Encrypted vault

If the desktop app keeps its secrets in the encrypted vault instead of the OS keychain, the vault is locked at the start of every command-line run. A command that needs credentials unlocks it first. That means accounts with static keys, and `cp` and `sync` when secret references such as ZIP passwords exist. There are two ways to give the master password:

- `--vault-password-stdin` reads it from the first line of stdin, for example `$pw | pail --vault-password-stdin buckets`. stdin is then not available for the `Proceed? [y/N]` prompt, so destructive commands also need `--yes`.
- On a terminal, a prompt `Vault master password:` asks for it without echo (three attempts).

Without either, the command exits 2 and says why. Accounts that use profiles, default credentials or anonymous access are not asked, and neither is `accounts`.

The password cannot be passed as a flag value or an environment variable. The unlock lasts for one run. The command line only unlocks the vault; creating it, changing the password and resetting it are done in the desktop app.

## Progress and output

One line per finished item goes to stderr (`done`, `skipped`, `FAILED`, `UNKNOWN` or `unprocessed`), followed by a summary line. On a terminal a live line shows items, bytes and speed. For machine-readable results use `--json`; otherwise stdout carries only listings, URLs and the sync plan.

## Confirmation

`rm`, `rb`, `mv` and `sync` with deletions print the scope first: account, bucket and prefix, number of objects and bytes, and for `sync` the number of deletions. Local deletions of a download-direction mirror go to the OS trash. On a terminal they ask `Proceed? [y/N]`. Without a terminal they exit 2 unless `--yes` is given. `cp` does not ask, including with `--conflict overwrite`. `sync --dry-run` prints the plan, changes nothing, asks nothing and exits 0.

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | Success. Skipped items count as success. |
| 1 | Failure: an error, a failed task, every item failed, or the confirmation was declined. |
| 2 | Usage error or invalid input; confirmation required but no terminal and no `--yes`; or the encrypted vault is locked and the password cannot be asked for (no terminal and no `--vault-password-stdin`). |
| 3 | Partial failure: some items succeeded and others failed, have an unknown outcome (not retried) or were not processed. |
| 130 | Interrupted with Ctrl+C. Running tasks are cancelled and get up to 10 s to stop. Unfinished downloads are written to temporary files that are removed. |

Errors name their kind in words: not found, access denied, not supported by this provider, conflict, network error, canceled, invalid input, locked. A wrong vault password is reported as access denied (exit 1).

## Limits

- A destination cannot be renamed on the fly, S3 paths are not expanded with wildcards, `versions` lists one folder level and `rm --versions` takes a single key.
- Copy and move work inside one account.
