# Links and CDN

Copy the address of an object, create presigned URLs that work for a set time, give an account a CDN address, and manage CloudFront for AWS accounts. Each of these also works from the command line.

## Copy an object's address

Select an object, right-click it and open **Copy link**. The submenu holds:

| Command | Copies |
| --- | --- |
| **Copy key** | The object key, such as `photos/2026/cat.jpg`. |
| **Copy URI** | The S3 address, such as `s3://my-bucket/photos/2026/cat.jpg`. |
| **Copy URL** | The address the object has when it is public. It works only if the bucket or the object allows public reads. |
| **Copy CDN URL** | The CDN address, when the account has a CDN URL for this bucket. |
| **Copy signed URL…** | Opens the dialog for presigned URLs, described below. |

The same commands are in the command palette. The inspector shows the **URI** and the **URL** of the selected object, and its **CDN URL** when there is one.

## Presigned URLs

A presigned URL lets anyone who has it download an object, or upload to a key, without an account, until it expires.

1. Select one or more objects and choose **Copy link › Copy signed URL…**. Folders in the selection are left out.
2. Choose the **Method**. GET lets the holder download the object. PUT lets the holder upload a file to that key, replacing what is there.
3. Choose how long it is **Valid for**.
4. Choose **Generate**. The URL appears with a line saying the method, when it expires and how long it is valid.
5. Choose **Copy**, or **Copy all** for several objects.

Anyone with a presigned URL can use it until it expires, so choose the shortest expiry that works.

Pail copies signed URLs as confidential clipboard content, so Windows clipboard history, macOS Universal Clipboard and clipboard managers that honour the mark do not keep them.

### Expiry

**Valid for** offers **15 min**, **1 hour**, **1 day** and **7 days**. **Custom** takes an **Amount** in **Minutes**, **Hours** or **Days**. The longest expiry is 7 days, which is the S3 limit.

A URL signed with temporary credentials, such as a session token or an assumed role, stops working when those credentials expire, even if its own expiry is later.

### One version or many objects

- For one version: for a GET URL of a single object, type a **Version ID (optional)**; leave it empty for the latest version. In the **Versions** tab of the inspector, **Signed URL for this version** opens the dialog with the version filled in. A PUT URL cannot point to a version.
- For many objects: with several objects selected, the dialog signs each of them with the same method and expiry. The result is a list with a copy button for each URL; **Copy all** copies them all.

After generating, **Copy public URL** copies the public addresses of the same objects instead. When every object has a CDN address, the button is **Copy CDN URL**.

## CDN URL

An account can have a CDN address. Pail then shows a CDN link for each object of the buckets the CDN serves. Pail only builds the link; it does not configure the CDN.

1. Choose **Edit account…** for the account.
2. Enter the **CDN URL**, such as `https://cdn.example.com`. It must be an http or https address without a user name, password, query or fragment.
3. Optionally list the **CDN buckets**, separated by commas. Leave the field empty to use the CDN URL for every bucket of the account.
4. Save the account.

### How the link is built

A base URL without placeholders gives `base/bucket/key`. With the CDN URL `https://s3-cdn.example.com`, the object `s3://assets/logo.svg` gets this link:

```text
https://s3-cdn.example.com/assets/logo.svg
```

For another layout, write a template with `{bucket}` and `{key}`. A template that uses `{bucket}` needs `{key}` as well. For example:

```text
https://{bucket}.cdn.example.com/{key}
```

The key is percent-encoded as in the public URL, and `/` stays as it is.

### Where CDN links appear

- In the inspector, the **CDN URL** row with **Copy CDN URL** and **Open CDN URL**.
- In the object menu: **Copy link › Copy CDN URL**, and **Open CDN URL**, which opens the link of one object in your browser. With several objects selected, **Copy CDN URL** copies one link per line.
- After you upload a single file to a CDN bucket, the notification offers **Copy CDN link**.

## CloudFront

AWS accounts can manage their CloudFront distributions: invalidate cached paths, and enable or disable a distribution. Open **CloudFront** in the **TOOLS** group of the sidebar, from the command palette, or with `pail-desktop --tool cloudfront`.

The tool lists the account's distributions with their **Domain**, **Aliases**, **Status** and whether they are **Enabled**. Select one to see its invalidations. For other accounts the tool says that CloudFront is available for AWS accounts.

### Invalidations

An invalidation asks CloudFront to drop its cached copies of some paths. To create one:

1. Select a distribution and choose **New invalidation…**.
2. Enter the **Paths**, one per line, each starting with `/`. A `*` is allowed only as the last character and matches everything below that point, such as `/images/*`. **Invalidate whole bucket** fills in `/*`. One request takes up to 3,000 paths.
3. Choose **Invalidate**.

You can also start from the browser. Select objects or folders and choose **Invalidate…** in the object menu: each object becomes its path, and each folder its path with `*`. **Invalidate CloudFront…** in the bucket menu fills in `/*`.

The **INVALIDATIONS** list shows each invalidation's **Status** and when it was **Created**. While one is InProgress, the list refreshes every 10 seconds; **Refresh invalidations** reads it at once.

### Enable or disable a distribution

Select a distribution and choose **Disable** or **Enable**, then confirm. CloudFront takes a few minutes to deploy the change. A disabled distribution stops serving content from its domain, but the distribution and its settings are kept.

## Command line

| Command | Does |
| --- | --- |
| `pail presign` | Prints a presigned URL for one object. |
| `pail url` | Prints the CDN link of an object, or its public address. It sends no request. |
| `pail cloudfront ls` | Lists the CloudFront distributions of an AWS account. |
| `pail cloudfront invalidate` | Creates an invalidation, after a confirmation. |

```sh
pail presign s3://my-bucket/report.pdf --expires 1h
pail presign s3://my-bucket/inbox/upload.zip --method PUT --expires 15m
pail presign s3://my-bucket/report.pdf --version-id 3HL4kqtJlcpXroDTDmJ
pail url s3://my-bucket/report.pdf
pail url s3://my-bucket/report.pdf --kind public
pail cloudfront ls --bucket my-bucket
pail cloudfront invalidate E2EXAMPLE /index.html /images/*
```

- `presign` signs a GET URL by default, valid for 1 hour. `--expires` takes a duration such as `15m`, `1h` or `168h` (7 days).
- `url` prints the CDN link when the account has one for that bucket, otherwise the public address. `--kind cdn` exits with code 1 when no CDN URL is configured.
- `cloudfront ls --bucket` keeps the distributions with an origin in that bucket.
- Enabling and disabling a distribution is only in the desktop app.

The CDN URL is set with `pail account edit --cdn-url` and `--cdn-bucket`. See [CDN URLs](../cli.md#cdn-urls) and [Administration](../cli.md#administration) in the command-line reference.
