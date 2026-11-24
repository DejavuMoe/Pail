# Connect storage

An account in Pail is one connection to a storage service: where the service is, how requests are signed and which buckets to show. This page explains each field of the account dialog and the matching flags of the pail command.

## Add an account in the app

1. Open the account menu at the top of the sidebar and choose **Add account…**.
2. Set **Provider**: AWS for Amazon S3, **S3-compatible** for any other service.
3. Enter a **Name**. It is how the account appears in the account menu and on the command line.
4. For an S3-compatible service, enter the **Endpoint**, the **Region** and **Path-style addressing**, see [Endpoint, region and path-style](#endpoint-region-and-path-style). For AWS, enter the **Region**.
5. Choose the **Credentials** and fill in their fields, see [Credentials](#credentials).
6. Optionally fill in **Buckets**, **CDN URL** and **CDN buckets**, and open **Advanced** for the other options.
7. Choose **Test connection**. When it shows **Connected**, choose **Save**.

A new account starts with the region `us-east-1`, **Path-style addressing** on, **Access keys** as credentials and **Checksum mode** set to **Auto**.

The same menu has **Edit account…** and **Remove account…**. Removing an account only deletes the saved connection and its stored credentials from this computer. Buckets and objects are not touched. Saved sync jobs of the account stay and stop working.

## Add an account on the command line

`pail account add` takes the account's name and the same fields as flags. It asks for the secret access key on the terminal without echo:

```sh
pail account add backup --endpoint https://s3.example.com --region us-east-1 --path-style --access-key-id AKIAEXAMPLE
```

An AWS account that uses a profile from your AWS configuration (including one created with `aws login --profile`) needs no endpoint and no secret, and uses the profile's region when `--region` is omitted:

```sh
pail account profiles
pail account add work --profile work
```

- `--secret-stdin` reads the secret key from one line of stdin instead of the prompt. `--ask-session-token` also reads a session token.
- The secret key and the session token are never taken from a flag or an environment variable, and never printed.
- `pail account edit NAME --set-secret` replaces a stored secret; other flags of `edit` change only the fields you give.
- `pail account show`, `pail account test` and `pail account rm` show, test and remove an account.

Every flag is listed in the [command-line reference](../cli.md#accounts). If your secrets are in the encrypted vault, the command asks for its master password first, see [Data and security](privacy.md).

## Credentials

| Choice | Flag value | What Pail uses |
| --- | --- | --- |
| **Access keys** | `static` | An access key ID and a secret access key that Pail stores. |
| **AWS profile** | `profile` | A named profile from your AWS config and credentials files (including `aws login --profile` and SSO sessions), for example `default`. |
| **Default chain** | `default` | The standard AWS SDK lookup: environment variables, the shared files, then instance or container roles. |
| **Anonymous** | `anonymous` | Unsigned requests. Works for public buckets only. |

The flag is `--credentials`. With `--profile` it defaults to `profile`, otherwise to `static`.

When local profiles exist in your AWS configuration or credentials files, the **Profile** field lists them with their authentication type and region, plus **Other…** to type any name. When an `aws login` session expires, run `aws login --profile NAME` in a terminal and retry in Pail; Pail reloads updated AWS configuration files automatically.

With **Access keys**, **Session token (optional)** is only needed for temporary credentials, for example from AWS STS. The token is stored with the secret key, so enter the secret key again when you change the token. When editing an account, leave **Secret access key** empty to keep the stored one.

Pail stores the secret key in the system keychain or in the encrypted vault, never in its configuration files. An account whose secret is gone shows **Secret missing** in the sidebar, with **Enter secret…** to type it again.

An anonymous account cannot list buckets. Name its buckets in **Buckets**, see [Name the buckets](#name-the-buckets).

### AssumeRole

Under **Advanced**, **Role ARN** names an IAM role to assume with the credentials above. All requests then use that role. It starts with `arn:` and works with access keys, an AWS profile or the default chain, but not with anonymous access.

- **External ID**: the external ID the role's trust policy requires, if any.
- **Session name**: the name of the assumed-role session, shown in AWS logs such as CloudTrail. Leave it empty for an automatic name.

On the command line these are `--role-arn`, `--external-id` and `--role-session-name`.

## Endpoint, region and path-style

| Service | Provider | Endpoint | Path-style |
| --- | --- | --- | --- |
| Amazon S3 | AWS | None | Not used |
| MinIO, VersityGW | S3-compatible | Your server, such as `http://localhost:9000` | On |
| Other services | S3-compatible | The address from the provider | As the provider says |

- **Endpoint** is the full address of the service, starting with `http://` or `https://`, without credentials or a query string. On the command line, `--endpoint` makes the account S3-compatible.
- **Region** is the region requests are signed for, such as `us-east-1`. With **AWS profile** or **Default chain**, leave it empty to use the profile's region, falling back to `us-east-1`. Otherwise leave the default unless your service asks for a specific region.
- **Path-style addressing** addresses objects as `endpoint/bucket/object`. MinIO and most self-hosted gateways need it on; keep it off for AWS. On the command line it is `--path-style`, and `--path-style=false` turns it off.

### Advanced options

**Advanced** in the account dialog holds the AssumeRole fields and these options:

| Option | Flag | Effect |
| --- | --- | --- |
| **Checksum mode** | `--checksum-mode` | The integrity checksums added to uploads and checked on downloads. |
| **Skip TLS verification** | `--insecure-skip-verify` | Turns off certificate checks for this connection. |
| **Requester pays** | `--requester-pays` | See [Requester pays](#requester-pays). |
| **Proxy** | `--proxy` | See [Proxy of one account](#proxy-of-one-account). |

**Checksum mode** is **Auto** (`auto`), **When required** (`when_required`) or **When supported** (`when_supported`). **Auto** uses the SDK default on AWS and sends only the required checksums to S3-compatible services. Choose **When required** if a service rejects uploads, and **When supported** to always send them.

**Skip TLS verification** lets a fake server read your data and credentials. Use it only for a test server you trust, such as a local MinIO with a self-signed certificate.

## Test the connection

**Test connection** in the account dialog lists the account's buckets once, or, if you named buckets, the folders you named. It shows **Connected** or the error, for example access denied or a network error. The result disappears when you change a field, because it no longer applies.

`pail account add` runs the same test before saving. A failed test saves nothing; `--no-test` saves without testing. `pail account test NAME` tests a saved account.

If the test is denied because the key may not list buckets, the dialog says so: name the buckets under **Buckets**.

## Name the buckets

Some access keys may not list the buckets of the account, or may only use one folder of a bucket. For such a key, enter the buckets in **Buckets**, separated by commas. Write `bucket/folder/` to open a bucket in that folder:

```text
team-data/reports/, public-assets
```

- The sidebar and `pail buckets` show only these buckets, and each opens in its folder.
- The path bar starts at that folder. **Download bucket…**, **Copy contents to…** and the other whole-bucket actions of the sidebar cover only that folder.
- Each bucket can have one folder.
- When the service refuses to list the buckets of an account, the sidebar offers **Name buckets…**, which opens the account.

On the command line, repeat `--bucket` for each bucket:

```sh
pail account add reports --endpoint https://s3.example.com --access-key-id AKIAEXAMPLE --bucket team-data/reports/ --bucket public-assets
```

Leave **Buckets** empty to show every bucket the service lists.

## Proxy

Requests use the account's own proxy when it has one, otherwise the proxy from **Settings › Network**. When that is set to **From environment**, the `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY` environment variables decide. The command line follows the same order.

### Proxy for all accounts

In **Settings › Network**, **Proxy server** sends the requests of all accounts through one proxy:

1. Choose the **Proxy protocol**: HTTP, HTTPS or SOCKS5. **From environment** uses the environment variables instead.
2. Enter the **Host name or IP address**, without `http://` or a port, and the **Port** (1 to 65535).
3. Optionally enter **User name (optional)** and **Password (optional)**. A password needs a user name.
4. Press **Apply**.

The password is stored with your other secrets in the keychain or the encrypted vault. Addresses on this computer connect directly. Running transfers are not interrupted. The setting covers requests to S3, and for AWS accounts to STS and CloudFront.

This proxy is set only in the desktop app; `pail settings` does not change it, but `pail` uses it.

### Proxy of one account

**Proxy** under **Advanced** has three choices:

- **Same as Settings**: the proxy for all accounts.
- **No proxy**: connect directly, ignoring the settings and the environment variables.
- **Its own proxy**: a **Proxy address** such as `socks5://127.0.0.1:1080`, with `http`, `https` or `socks5`.

An account's own proxy takes no user name or password; set a proxy that needs them in Settings. On the command line, `--proxy` takes the address, `direct` for no proxy, or an empty value to use the proxy from the settings.

### Proxy limits

- Pail reads the environment variables, not the proxy settings of the operating system.
- Proxies that need a certificate or another sign-in method, such as NTLM, are not supported.

## CDN URL

An account can have a **CDN URL**, the address your CDN serves objects from. Pail then offers **Copy CDN URL** and **Open CDN URL** for objects, the inspector shows the object's CDN URL, the message after an upload offers **Copy CDN link**, and `pail url` prints the CDN link.

- A base URL, such as `https://cdn.example.com`, gives links like `base/bucket/key`.
- A template with `{bucket}` and `{key}` builds a custom link. A template that uses `{bucket}` needs `{key}` as well.

```text
https://{bucket}.cdn.example.com/{key}
```

**CDN buckets** limits the CDN link to some buckets, separated by commas. Leave it empty to use the CDN URL for every bucket of the account. The URL must be `http` or `https`, without credentials, a query or a fragment.

On the command line these are `--cdn-url` and `--cdn-bucket`, repeated for each bucket. More about links is in [Links](links.md).

## Requester pays

Turn on **Requester pays** under **Advanced** to read and write buckets whose owner bills the requester. Pail then sends the request-payer header with every request of the account, and your account pays for these requests and the data they transfer. On the command line it is `--requester-pays`.
