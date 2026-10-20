# Pail command line (`pail`)

English | [简体中文](cli.zh-CN.md)

`pail` is a thin front end over `internal/app`. It shares the configuration directory and the secret store (OS keyring or the encrypted vault) with the desktop app, so accounts and saved sync jobs created there are available here. Accounts cannot be added or changed from the CLI, and no command ever prints credentials.

```powershell
go build -o bin/pail.exe ./cmd/pail
pail accounts
pail ls s3://my-bucket/photos/ -r
```

S3 paths are written `s3://bucket/key-or-prefix/`; any other argument is a local path.

## Global flags

Accepted before or after the command name.

| Flag | Meaning |
| --- | --- |
| `--account NAME\|ID` | Account to use. Default: the only configured account; with several, the command fails (exit 2) and lists them. |
| `--config-dir DIR` | Configuration directory (default: the desktop app's, `<user config dir>/Pail`). |
| `--json` | Newline-delimited JSON on stdout: one object per listing row or item result and a final `{"type":"summary",...}` object. Failures end with `{"type":"error",...}`. |
| `--quiet` | No per-item lines or progress on stderr. |
| `--yes` | Assume yes for confirmations. |
| `--timeout D` | Abort after D (for example `10m`); running tasks are cancelled. 0 = no limit. |
| `--vault-password-stdin` | Read the encrypted vault's master password from the first line of stdin (see below). |

## Commands

| Command | Notes |
| --- | --- |
| `accounts` | Name, ID, provider, endpoint, region. |
| `vault status [--vault-password-stdin]` | Secret backend (`keyring` or `vault`), whether `vault.json` exists and the desktop auto-lock setting. With `--vault-password-stdin` it also checks the master password. `--json` emits `{"type":"vault","backend":...,"vaultExists":...,"autoLockMinutes":...}`. Never prints secrets. |
| `buckets` | Buckets with region. |
| `ls [s3://b/prefix/] [-r]` | Folders as `PRE name/`; sizes human-readable (raw bytes with `--json`). Without a path: buckets. A key without trailing `/` that is not a folder lists that single object. |
| `cp SRC DST [-r] [--conflict P] [--decode gzip\|zip\|auto]` | local to S3 (Upload), S3 to local (Download), S3 to S3 (Copy within the account). |
| `mv SRC DST [-r] [--conflict P]` | S3 to S3 only. Copy, verify, then delete the source. |
| `rm s3://b/key [-r] [--versions]` | Delete. `-r` treats the key as a folder (`s3://b` with `-r` = whole bucket). `--versions` permanently deletes every version and delete marker of one key. |
| `mb s3://b [--region R] [--object-lock]`, `rb s3://b` | Create / delete (empty) bucket. |
| `presign s3://b/key [--expires 1h] [--method GET\|PUT] [--version-id V]` | Prints the URL. |
| `url s3://b/key [--kind cdn\|public]` | Prints the object's address without any request. Default: the CDN link when the account has a CDN URL for that bucket (set in the desktop app's account dialog; see below), else the public S3 address. `--kind cdn` exits 1 when none is configured; `--kind public` always prints the S3 address. `--json` emits `{"type":"url","kind":"cdn"\|"public","url":...}` and the summary. A prefix is a usage error (exit 2). |
| `versions s3://b/prefix/` | One folder level; includes delete markers. |
| `restore s3://b/key --version-id V` / `restore s3://b/prefix/ --deleted [-r]` | Make an old version current / undelete keys whose latest entry is a delete marker. History is never deleted. |
| `sync LOCAL s3://b/prefix/` / `sync s3://b/prefix/ LOCAL` | `--mode new\|new_changed\|mirror`, `--compare size_time\|size\|checksum`, repeatable `--exclude GLOB`, `--no-rules`, `--dry-run`. |
| `sync --job NAME [--dry-run]` | Run a job saved in the desktop app (uses that job's account unless `--account` is given). |
| `version` | Prints `pail <version>`. |

`--conflict` is `skip`, `overwrite`, `rename`, `newer` or `different_size`. When omitted, `cp` and `mv` both use the default saved in the desktop settings (initially `rename`).

Destination rules: the destination of an upload or S3 copy is always a folder (a missing trailing `/` is added), and the source name is kept below it, like `cp -r src dst/`. The destination of a download is always a local directory (created if missing).

## CDN URLs

An account can have a CDN base URL, for example `https://s3-cdn.example.com`, and optionally a list of buckets it serves (default: all). Without placeholders the link is `<base>/<bucket>/<key>`, so `s3://assets/logo.svg` becomes `https://s3-cdn.example.com/assets/logo.svg`. A template with `{key}` (and optionally `{bucket}`), such as `https://{bucket}.cdn.example.com/{key}`, is filled in instead. The key is percent-encoded like the public URL, keeping `/`. The setting is stored in `accounts.json` as `cdnUrl` / `cdnBuckets` and is edited in the desktop app; the CLI only reads it.

## Encrypted vault

When the desktop app stores secrets in its encrypted vault (`vault.json`, protected by a master password) instead of the OS keychain, every CLI run starts with the vault locked. A command that needs credentials (an account with static keys; `cp` and `sync` when secret references such as ZIP passwords exist) unlocks it first:

- `--vault-password-stdin`: the password is the first line of stdin, for example `$pw | pail --vault-password-stdin buckets`. stdin is then not available for the `Proceed? [y/N]` prompt, so destructive commands also need `--yes`.
- On a terminal, a no-echo prompt `Vault master password:` (three attempts).
- Without either, the command exits 2 with a message saying so. Nothing is asked for accounts that use profiles, default credentials or anonymous access, nor for `accounts`.

The password is never accepted as a flag value or environment variable (both are visible to other processes), is not logged and is not echoed. The unlock lasts for one run; the CLI never writes to the vault, creates it, changes its password or resets it: use the desktop app.

## Progress and output

One line per finished item goes to stderr (`done`, `skipped`, `FAILED`, `UNKNOWN`, `unprocessed`), then a summary line. On a terminal a live line shows items, bytes and speed. Machine-readable results use `--json`; stdout otherwise carries only listings, URLs and the sync plan.

## Confirmation

`rm`, `rb`, `mv` and `sync` with deletes print the scope first: account, bucket/prefix, objects and bytes (counted with `Estimate`), and for sync the number of deletes (local deletes of a download-direction mirror go to the OS trash). With a terminal on stdin they ask `Proceed? [y/N]`; without one they exit 2 unless `--yes` is given. `cp` is never confirmed, including `--conflict overwrite`. `sync --dry-run` prints the plan and exits 0 without changing anything and without asking.

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | Success (skipped items count as success). |
| 1 | Failure: error, failed task, every item failed, or confirmation declined. |
| 2 | Usage or invalid input, confirmation required but no terminal and no `--yes`, or the encrypted vault is locked and there is no way to ask for the password (no terminal, no `--vault-password-stdin`). |
| 3 | Partial failure: some items succeeded, some failed, were unknown (outcome uncertain, never replayed) or unprocessed. |
| 130 | Interrupted (Ctrl+C): running tasks are cancelled and given up to 10 s to stop. Downloads are written to temporary files that are removed, so no partial file is left. |

Errors print the kind in words: not found, access denied, not supported by this provider, conflict, network error, canceled, invalid input, locked. A wrong vault password is "access denied" (exit 1).

## Limits

- Destinations cannot be renamed on the fly; there is no glob expansion of S3 paths; `versions` lists one folder level; `rm --versions` takes a single key.
- Copy and move work inside one account.
- Verification: the commands are covered by in-process tests against `internal/s3test` (protocol level). The application layer underneath is also tested against MinIO and a self-hosted VersityGW (see [status.md](status.md)); the CLI front end itself has not been run against a real server.
