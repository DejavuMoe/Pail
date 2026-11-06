# Pail command line (`pail`)

English | [简体中文](cli.zh-CN.md)

`pail` works with the same accounts, saved sync jobs and secrets as the desktop app. It shares the desktop app's configuration directory and secret store (the OS keychain or the encrypted vault). Accounts, the encrypted vault and the settings can be managed from the command line as well as in the desktop app. Requests go through the proxy saved in the desktop app's **Settings › Network**; without one, `pail` follows the `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY` environment variables.

Release archives use versioned filenames, for example `pail-v<version>-windows-amd64.exe`. The examples below assume you installed the program as `pail` (`pail.exe` on Windows); you can also run the full filename.

```text
pail accounts
pail ls s3://my-bucket/photos/ -r
```

`pail help` lists the commands. `pail help COMMAND` prints one command's usage and flags.

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

Usage:

```text
pail accounts                                       # list accounts
pail account ls|show|add|edit|test|rm ...           # manage accounts, see Accounts
pail vault status|init|unlock|lock|passwd|backend|reset
                                                    # secret backend and vault, see Encrypted vault
pail bucket alias s3://bucket [NAME | --clear]      # display name of a bucket
pail buckets                                        # list buckets with their region
pail ls [s3://bucket/prefix/] [-r]                  # list objects and folders
pail cp SRC DST [-r] [--conflict POLICY] [--decode gzip|zip|auto]
                                                    # upload, download, or copy within S3
pail mv s3://SRC s3://DST [-r] [--conflict POLICY]  # move within S3
pail rm s3://bucket/key [-r] [--versions]           # delete objects, folders or versions
pail mb s3://bucket [--region REGION] [--object-lock]
pail rb s3://bucket                                 # delete an empty bucket
pail presign s3://bucket/key [--expires 1h] [--method GET|PUT] [--version-id V]
pail url s3://bucket/key [--kind cdn|public]        # CDN or public address, no request
pail versions s3://bucket/prefix/                   # versions and delete markers
pail restore s3://bucket/key --version-id V         # make an old version current
pail restore s3://bucket/prefix/ --deleted [-r]     # undelete keys
pail stat s3://bucket/key [--version-id V]          # properties of an object
pail cat s3://bucket/key [--version-id V]           # write an object to stdout
pail mkdir s3://bucket/prefix/                      # create a folder
pail du [s3://bucket/prefix/]                       # object count and size below a prefix
pail tags get|set s3://bucket/key ...               # object tags, see Objects
pail acl get|set s3://bucket/key ...                # object ACL
pail props get|set s3://bucket/key ...              # headers and metadata
pail storage-class s3://bucket/key CLASS [-r]       # change the storage class
pail multipart ls|abort s3://bucket[/prefix/] ...   # unfinished multipart uploads
pail cloudfront ls|invalidate ...                   # CloudFront, AWS accounts only
pail settings get [KEY] | set KEY VALUE             # settings shared with the desktop app
pail sync LOCAL s3://bucket/prefix/ [SYNC FLAGS]    # one-way sync, upload
pail sync s3://bucket/prefix/ LOCAL [SYNC FLAGS]    # one-way sync, download
pail sync --job NAME [--dry-run]
pail sync job ls|add|rm ...                         # saved sync jobs
pail version
```

### Command details

- `-r` (also `--recursive`) works the same way in `ls`, `cp`, `mv`, `rm` and `restore`.
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
- `sync --job NAME` runs a saved sync job, using that job's account unless `--account` is given. `sync job ls`, `sync job add` and `sync job rm` manage the saved jobs, see Administration.

JSON shapes of `vault status` and `url` with `--json`:

```text
{"type":"vault","backend":...,"vaultExists":...,"autoLockMinutes":...}
{"type":"url","kind":"cdn","url":...}
```

`kind` is `cdn` or `public`.

With `--json`, each of these commands prints one object per row, then the summary. The `type` of the rows:

| Command | Type |
| --- | --- |
| `accounts`, `account ls`, `account show` | `account` |
| `stat`, `props get` | `object` |
| `du` | `du` |
| `tags get` | `tag` |
| `acl get` | `acl` |
| `multipart ls` | `upload` |
| `sync job ls` | `job` |
| `cloudfront ls` | `distribution` |
| `cloudfront invalidate` | `invalidation` |
| `settings get` | `setting` |
| `bucket alias` | `alias` |

`cat` writes the raw object and has no JSON form.

## CDN URLs

An account can have a CDN base URL, for example `https://s3-cdn.example.com`, and optionally a list of the buckets it serves (default: all). Without placeholders the link is `<base>/<bucket>/<key>`, so `s3://assets/logo.svg` becomes:

```text
https://s3-cdn.example.com/assets/logo.svg
```

A template with `{key}` (and optionally `{bucket}`) is filled in instead, for example:

```text
https://{bucket}.cdn.example.com/{key}
```

The key is percent-encoded like the public URL, keeping `/`. The CDN URL is set in the desktop app's account dialog or with `pail account edit --cdn-url`. `pail url` only reads it.

## Accounts

`pail account` manages the accounts that the desktop app uses too.

```text
pail account ls
pail account show [NAME]
pail account add NAME --endpoint URL --access-key-id ID [--region R] [--path-style] [--secret-stdin]
pail account edit [NAME] [--region R] [--set-secret [--secret-stdin]]
pail account test [NAME]
pail account rm [NAME]
```

`NAME` is an account name or ID. Without it, the command uses `--account`, or the only account. `account ls` is the same as `accounts`.

- `add` takes the new account's name. Before saving it lists the buckets once to test the connection. A failed test saves nothing. `--no-test` saves without testing; anonymous accounts cannot list buckets, so they need it.
- `edit` changes only the flags you give. `--rename NEW` renames the account. A switch is turned off with `--path-style=false`. An empty value, such as `--cdn-url ""`, clears a field.
- `show` prints the settings without secrets. For a static account it says whether the secret key is stored or missing.
- `test` lists the buckets of a saved account.
- `rm` asks for confirmation, then deletes the account and its saved secret. Buckets and objects are not touched. Saved sync jobs of the account stay and stop working.

The account flags are the fields of the desktop app's account dialog:

| Flag | Meaning |
| --- | --- |
| `--provider` | `aws` or `s3`. Default: `s3` with `--endpoint`, otherwise `aws`. |
| `--endpoint` | Endpoint URL of an S3-compatible service. |
| `--region` | Region. |
| `--path-style` | Address buckets as `endpoint/bucket`. |
| `--credentials` | `static`, `profile`, `default` or `anonymous`. Default: `static`, or `profile` with `--profile`. |
| `--access-key-id` | Access key ID for static credentials. It is not secret. |
| `--profile` | Profile name for the `profile` credentials. |
| `--role-arn` | Role to assume. |
| `--external-id` | External ID of the assumed role. |
| `--role-session-name` | Session name of the assumed role. |
| `--insecure-skip-verify` | Do not verify the server's TLS certificate. |
| `--checksum-mode` | `auto`, `when_required` or `when_supported`. |
| `--cdn-url` | CDN base URL or template, see CDN URLs. |
| `--cdn-bucket` | A bucket the CDN serves. Repeat it for several buckets. |

The secret access key and the session token are never read from a flag or an environment variable, and they are not printed, not even with `--json`. `add` and `edit --set-secret` ask for the secret key on the terminal without echo. With `--secret-stdin` they read it from one line of stdin instead. `--ask-session-token` also reads a session token, from a second prompt or the next line of stdin. `edit` replaces the stored secret only with `--set-secret`. If the secret backend is the encrypted vault, its master password is asked first, so with `--vault-password-stdin` it is the first line.

```text
$secret | pail account add backup --endpoint https://s3.example.com --region us-east-1 --path-style --access-key-id AKIAEXAMPLE --secret-stdin
```

## Encrypted vault

If the secret backend is the encrypted vault instead of the OS keychain, the vault is locked at the start of every command-line run. A command that needs credentials unlocks it first. That means accounts with static keys, and `cp` and `sync` when secret references such as ZIP passwords exist. There are two ways to give the master password:

- `--vault-password-stdin` reads it from the first line of stdin, for example `$pw | pail --vault-password-stdin buckets`. stdin is then not available for the `Proceed? [y/N]` prompt, so destructive commands also need `--yes`.
- On a terminal, a prompt `Vault master password:` asks for it without echo (three attempts).

Without either, the command exits 2 and says why. Accounts that use profiles, default credentials or anonymous access are not asked, and neither is `accounts`.

The password cannot be passed as a flag value or an environment variable. The unlock lasts for one run.

`pail vault` manages the vault itself:

| Command | Effect |
| --- | --- |
| `vault status` | Shows the backend and the vault file. |
| `vault init` | Moves all secrets into a new encrypted vault. |
| `vault backend vault` | The same as `vault init`. |
| `vault backend keychain` | Moves the secrets back to the OS keychain and deletes `vault.json`. |
| `vault unlock` | Checks the master password. |
| `vault lock` | Does nothing that lasts, see below. |
| `vault passwd` | Changes the master password. |
| `vault reset` | Deletes the vault when the password is forgotten. |

- `init` asks for the new master password twice, or reads it from the first line of stdin with `--vault-password-stdin`. It must have at least 8 characters.
- `backend keychain` needs the current master password.
- `unlock` only verifies the password. Every run starts locked and unlocks the vault for that run, so `vault lock` has nothing to lock. The desktop app has its own lock.
- `passwd` asks for the current password, then for the new one twice. With `--vault-password-stdin --new-vault-password-stdin` it reads two lines: the current password, then the new one.
- `reset` deletes `vault.json`, so every secret in it is lost, and the backend becomes the OS keychain. Accounts then need their secret entered again with `account edit NAME --set-secret`. It lists the accounts, then asks you to type `RESET` on a terminal. Without a terminal it needs `--yes`.

## Objects

These commands act on one object. `stat`, `cat`, `tags` and `acl` take `--version-id` to select a version.

- `stat` prints size, modification time, ETag, storage class, headers, metadata, tags and encryption.
- `cat` writes the object's bytes to stdout unchanged and prints nothing else, so the output can be piped or redirected to a file. Errors go to stderr.
- `mkdir` creates a folder: an empty object named `prefix/`.
- `du` adds up the objects below a prefix, or the whole bucket without one. It lists everything below the prefix, so it takes as long as the prefix is large. Older versions, delete markers and unfinished multipart uploads are not counted.
- `tags get` prints the tags as `KEY=VALUE` lines. `tags set s3://bucket/key K=V ...` replaces all tags; `--merge` keeps the existing ones and adds or replaces the given ones. `tags set --clear` removes all tags after confirmation. S3 allows 10 tags per object.
- `acl get` prints the owner and the grants. `acl set s3://bucket/key CANNED` applies a canned ACL: `private`, `public-read`, `public-read-write`, `authenticated-read`, `aws-exec-read`, `bucket-owner-read` or `bucket-owner-full-control`. Buckets with ACLs turned off report that ACLs are not supported.
- `props set` changes headers and user metadata: `--content-type`, `--content-encoding`, `--content-language`, `--content-disposition`, `--cache-control`, `--expires` (an HTTP date), `--metadata KEY=VALUE` (repeatable, lower-case keys), `--delete-header NAME` and `--delete-metadata KEY`. An empty value, such as `--cache-control ""`, removes the header. `props get` is the same as `stat`.
- `storage-class s3://bucket/key CLASS` changes the storage class, for example `STANDARD_IA` or `GLACIER_IR`.

`props set` and `storage-class` rewrite the object in place, so the object's modification time changes and a versioned bucket gets a new version. With `-r` they apply to every object below a prefix; that prints the scope and asks for confirmation.

## Administration

- `bucket alias s3://bucket NAME` sets the name the desktop app shows for a bucket. An empty `NAME`, or `--clear`, removes it. Without `NAME` the command prints the current alias. The alias exists only in Pail's configuration; nothing changes on the storage service, and commands keep using the real bucket name.
- `multipart ls s3://bucket[/prefix/]` lists unfinished multipart uploads with their start time, storage class, upload ID and key. `multipart abort` with the same path aborts them after confirmation and discards the parts already uploaded. `--older-than D` limits it to uploads started longer ago than `D`, for example `36h` or `7d`.
- `sync job ls` lists the saved sync jobs. `sync job add NAME LOCAL s3://bucket/prefix/` saves an upload job and `sync job add NAME s3://bucket/prefix/ LOCAL` a download job, with the same `--mode`, `--compare`, `--exclude` and `--no-rules` flags as `sync`. The local folder must exist. `sync job rm NAME` removes a saved job after confirmation; local files and objects stay. Run a job with `sync --job NAME`.
- `cloudfront ls` lists the account's CloudFront distributions; `--bucket B` keeps those with an origin in that bucket. `cloudfront invalidate DISTRIBUTION PATH...` prints the distribution and the paths and asks for confirmation before it creates the invalidation. It works with AWS accounts only. CloudFront has not been tested on a real AWS account.
- `settings get` prints every setting as `KEY VALUE`; `settings get KEY` prints only the value. `settings set KEY VALUE` changes one. They are the settings of the desktop app.

| Setting | Values |
| --- | --- |
| `theme` | `system`, `light` or `dark` |
| `upload-limit` | Parallel uploads, 1 to 64 |
| `download-limit` | Parallel downloads, 1 to 64 |
| `other-limit` | Parallel other tasks, 1 to 64 |
| `bulk-workers` | Workers inside one bulk task, 1 to 64 |
| `part-size-mib` | Multipart part size in MiB, 5 to 5120 |
| `download-dir` | Default download folder, empty for none |
| `conflict` | `skip`, `overwrite`, `rename`, `newer` or `different_size` |
| `show-versions` | `on` or `off` |
| `log-to-file` | `on` or `off` |
| `vault-auto-lock-minutes` | Idle minutes before the desktop app locks the vault, 0 for never |
| `hide-tray-icon` | `on` or `off` |
| `recent-ops` | Recent operations kept, 1 to 1000 |
| `keep-recent-ops` | `on` or `off` |
| `secret-backend` | Read only. Change it with `vault init` and `vault backend`. |

The proxy is not part of `settings`; set it in the desktop app.
## Progress and output

One line per finished item goes to stderr (`done`, `skipped`, `FAILED`, `UNKNOWN` or `unprocessed`), followed by a summary line. On a terminal a live line shows items, bytes and speed. For machine-readable results use `--json`; otherwise stdout carries only listings, URLs and the sync plan.

## Confirmation

`rm`, `rb`, `mv` and `sync` with deletions print the scope first: account, bucket and prefix, number of objects and bytes, and for `sync` the number of deletions. Local deletions of a download-direction mirror go to the OS trash. On a terminal they ask `Proceed? [y/N]`. Without a terminal they exit 2 unless `--yes` is given. `cp` does not ask, including with `--conflict overwrite`. `sync --dry-run` prints the plan, changes nothing, asks nothing and exits 0.

These commands ask in the same way: `account rm`, `sync job rm`, `multipart abort` (when something matches), `cloudfront invalidate`, `tags set --clear`, and `props set` and `storage-class` with `-r`. `vault reset` asks you to type `RESET` instead of `y`.

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
