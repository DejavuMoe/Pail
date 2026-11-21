# Data and security

Pail keeps its settings in one folder on your computer, its secrets in the system keychain or in an encrypted vault, and connects only to the services you set up. This page lists what is stored where and what leaves your computer.

## Configuration folder

The desktop app and the pail command share one configuration folder, the `Pail` folder in your user configuration directory:

| System | Folder |
| --- | --- |
| Windows | `%AppData%\Pail` |
| macOS | `~/Library/Application Support/Pail` |
| Linux | `~/.config/Pail` |

Both programs accept `--config-dir` to use another folder instead.

### What the folder holds

| File | Contents |
| --- | --- |
| `settings.json` | Settings, including the proxy without its password. |
| `accounts.json` | Accounts: endpoint, region, access key ID and the other fields, without secrets. |
| `sync-jobs.json` | Saved sync jobs. |
| `upload-rules.json` | Upload rules. |
| `mime-types.json` | The MIME type table. |
| `transfers.json` | Transfers left unfinished at the last quit. |
| `secret-refs.json` | The names of stored secrets for upload rules, not their values. |
| `vault.json` | The encrypted vault, only when you use it. |
| `logs` | Diagnostic logs, only when logging is on. |

Removing an account deletes its entry and its stored credentials. The list of recent operations behind the status bar is kept in memory only and is empty again after you quit.

## Secrets

Secrets are the values that give access: secret access keys, session tokens, the proxy password, and the ZIP passwords and SSE-C keys that upload rules refer to. They are not written to the configuration files. They go to one of two places, chosen in **Settings › Security** under **Credential storage**:

- **System keychain** (the default), under the name `Pail`: Windows Credential Manager, macOS Keychain, or Secret Service on Linux.
- **Encrypted vault**: a file in the configuration folder, protected by a master password you choose.

The command line follows the same choice. It never reads a secret from a flag or an environment variable, and never prints one, not even with `--json`.

If a secret goes missing, for example after a vault reset, the account shows **Secret missing** in the sidebar. Choose **Enter secret…** to type it again.

## Encrypted vault

Use the vault when the system has no keychain, which Pail reports as "No system keychain is available on this computer", or when you want your secrets behind a master password of their own. If saving an account fails because there is no keychain, the account dialog offers **Set up encrypted vault…**.

The vault is `vault.json` in the configuration folder. **Settings › Security** shows the encryption as Argon2id and XChaCha20-Poly1305.

> A forgotten master password cannot be recovered. Resetting the vault deletes every secret in it.

### Create the vault

1. In **Settings › Security**, set **Credential storage** to **Encrypted vault**.
2. In **Create master password**, enter a password of at least 8 characters (at most 1024 bytes) twice.
3. Choose **Create vault**. Your stored credentials move into the vault.

On the command line, `pail vault init` does the same.

### Lock and unlock

- **Auto-lock** locks the vault after a number of minutes without using the stored credentials: **Never**, 5, 15, 30 or 60 minutes. The default is 15 minutes. The vault does not lock while transfers run or wait.
- **Lock now** locks it at once. **Unlock…** asks for the master password.
- The status bar shows a lock while the vault is in use. Click it to lock or unlock.

The command line keeps its own lock. Every run starts locked and asks for the master password when it needs a secret: on the terminal without echo, or from the first line of stdin with `--vault-password-stdin`. Accounts that use an AWS profile, the default chain or anonymous access are not asked. The details are in the [command-line reference](../cli.md#encrypted-vault).

### Change the password or leave the vault

- **Change master password…** asks for the current password and the new one twice. On the command line: `pail vault passwd`.
- Setting **Credential storage** back to **System keychain** moves the credentials to the keychain and deletes the vault file. On the command line: `pail vault backend keychain`.

### Forgotten master password

**Reset vault…** deletes the vault and every secret stored in it. Before it does, it lists the accounts that need their secret again, the secret references that lose their value and the settings that lose their secret, such as the proxy password. Type `RESET` to confirm. Afterwards the secrets are stored in the system keychain, and you enter the missing ones again. Your buckets and objects are not affected.

On the command line this is `pail vault reset`.

## Logs

The diagnostic log is off by default. Turn on **Write diagnostic log** in **Settings › Diagnostics**, or run `pail settings set log-to-file on`.

- The log is `logs/pail.log` in the configuration folder, kept as at most 3 files of up to 5 MiB each. **Open log folder** shows them.
- It holds errors and task results. File and bucket names can appear in it.
- Secret keys, session tokens, passwords, signatures, Authorization headers and URL query strings are replaced with `[REDACTED]`.
- Passwords typed by hand are recognised only by their label, so read a log before you share it.

## Network

Apart from update checks (below), Pail connects only to the endpoints of your accounts. For AWS accounts that includes AWS STS for AssumeRole and the CloudFront API. With **Default chain** credentials, the AWS SDK can also ask for the instance or container role of the machine.

Requests go through the account's own proxy, the proxy in **Settings › Network**, or the `HTTP_PROXY` and `HTTPS_PROXY` environment variables; see [Proxy](connect.md#proxy).

Pail has no telemetry.

Pail checks for updates only when you click **Check now** in **Settings › Updates**, run `pail update`, or choose a daily or weekly check there. A check reads a small file from GitHub or pail.download that names the latest version; downloading the update waits for your click. These requests go through the proxy in **Settings › Network** and send nothing about your accounts.

## Clipboard

Presigned URLs are copied as confidential clipboard content, so Windows clipboard history, macOS Universal Clipboard and clipboard managers that honour the mark do not keep them.
