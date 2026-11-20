# Buckets

Create, empty and delete buckets, move their whole contents, edit their 14 settings and clean up unfinished multipart uploads. Most of these tasks also have a command on the command line.

## The bucket menu

Right-click a bucket in the sidebar to open its menu. On Windows and Linux, Shift+F10 or the menu key opens it for the bucket that has the keyboard focus. The menu holds:

- **Open**, **Bucket settings…** and **Set alias…**;
- **Download bucket…**, **Copy contents to…** and **Move contents to…**;
- **Change storage class…** and **Change encryption…** for every object in the bucket;
- **Invalidate CloudFront…**, for AWS accounts only;
- **Empty bucket…** and **Delete bucket…**;
- **Copy name**, which copies the real bucket name.

**Bucket settings…** and **New bucket…** are also in the command palette (`Ctrl+K`, `⌘K` on macOS).

When an account names its buckets with a folder, such as `my-bucket/reports/`, **Download bucket…**, **Copy contents to…** and the other whole-bucket actions cover only that folder.

## Create a bucket

1. Choose **New bucket**, the + button beside **BUCKETS** in the sidebar, or **New bucket…** in the command palette.
2. Enter the **Name**: 3 to 63 characters, using a-z, 0-9, periods and hyphens. The dialog says what is wrong while you type. The name cannot be changed later.
3. Choose the **Region**. AWS accounts pick it from a list; other accounts type it, for example `us-east-1`. The region cannot be changed afterwards.
4. Turn on **Object Lock** if the bucket needs it. It also turns on versioning, and it can only be chosen when the bucket is created.
5. Choose **Create**.

## Empty a bucket

**Empty bucket…** deletes every object in the bucket. The dialog shows the account, the bucket, and the number and size of the objects while Pail counts them.

In a versioned bucket a plain delete keeps the older versions and only adds delete markers, so the bucket still holds data. Tick **Also delete all versions and delete markers** to remove those too. Emptying cannot be undone.

## Delete a bucket

**Delete bucket…** asks you to type the bucket name before **Delete bucket** becomes available. A bucket must be empty before it can be deleted. If it is not, the dialog says so and offers **Empty bucket…**. Deleting cannot be undone.

## Copy, move or download the contents

- **Download bucket…** asks for a local folder and downloads every object of the bucket into a folder named after the bucket. Files that already exist are handled by **Settings › Transfers › When a file already exists**.
- **Copy contents to…** copies every object to another bucket of the same account. Choose the **Target bucket**, optionally a **Target prefix** such as `backup/` (empty means the bucket root), and what to do **If an object already exists**. Objects keep their key path below the prefix.
- **Move contents to…** works the same way, then asks for a confirmation and starts when you choose **Move everything**. Each object is deleted from the source bucket once it is copied, and this cannot be undone.

These run as tasks in the transfers panel.

## Bucket settings

**Bucket settings…** opens one dialog with the 14 settings in a list beside the form. Six are edited in a small form; the other eight are edited as the JSON that the storage service exchanges.

A setting that the service does not offer shows **Not supported by this provider**. A setting your key may not read or change says that you do not have permission.

### Settings edited in a form

| Setting | What it does |
| --- | --- |
| **Versioning** | Keeps older versions when an object is overwritten or deleted. A bucket that never had versioning shows **Enable**; after that, the **Status** is **Enabled** or **Suspended**. Suspending keeps the versions already stored. |
| **Encryption** | The default server-side encryption for new uploads: SSE-S3 (AES-256) or AWS KMS with an optional **KMS key ID**. Existing objects do not change. |
| **Public access** | Four switches: **Block public ACLs**, **Ignore public ACLs**, **Block public bucket policies** and **Restrict public buckets**. |
| **Object ownership** | **Bucket owner enforced**, **Bucket owner preferred** or **Object writer**. Bucket owner enforced turns ACLs off. |
| **Requester pays** | Whoever downloads from the bucket pays the request and transfer costs instead of the owner. |
| **Transfer acceleration** | Lets clients use the bucket's accelerated endpoint, for an additional fee. Pail only switches the setting; its own transfers keep using the normal endpoint. |

Change the values and choose **Save**. **Remove configuration** puts the bucket back to the provider's default for that setting, after a confirmation.

### Settings edited as JSON

| Setting | What the JSON holds |
| --- | --- |
| **Policy** | A bucket policy that allows or denies access. It is sent as written and the service checks it. |
| **CORS** | Rules (`CORSRules`) that tell browsers which web origins may call the bucket, with which methods. |
| **Lifecycle** | Rules (`Rules`) that expire objects or move them to another storage class after a set time. |
| **Logging** | Where the access logs of the bucket go (`LoggingEnabled`): a target bucket and prefix. |
| **Tags** | Tags (`TagSet`) on the bucket itself, for example for cost allocation. |
| **Website** | Serves the bucket as a static website with an index and an error document, or redirects all requests. |
| **Object Lock** | The default retention, mode and period, that new object versions receive. It cannot be removed here. |
| **Replication** | Copies new objects to another bucket (`Destination`) with a role S3 can assume (`Role`). Versioning must be on in both buckets. |

A setting with nothing configured shows **Not configured** and a **Configure** button, which fills the editor with a starting template. **Validate** checks that the text is valid JSON; **Save** validates it again and sends it.

## Incomplete multipart uploads

An unfinished multipart upload keeps the parts already sent in the bucket, and the provider bills them as storage until the upload is completed or aborted.

1. Open **Multipart uploads** in the **TOOLS** group of the sidebar, from the command palette, or start the app with `pail-desktop --tool multipart`.
2. Choose the **Bucket**. To narrow the list, type a **Prefix** and press Enter.
3. The table shows each upload's key, when it was **Initiated**, its **Storage class**, **Initiator** and **Upload ID**. **Refresh** reads the list again.
4. Select the uploads and choose **Abort…**. After the confirmation, Pail aborts them and the parts already sent are discarded. This cannot be undone.

## Bucket aliases

An alias is a name you recognise, shown instead of the bucket name in the sidebar, the path bar and the command palette.

1. Right-click the bucket and choose **Set alias…**.
2. Type the **Alias** and save. Leave the field empty to show the bucket name again.

The bucket is not renamed. The alias exists only in Pail's configuration: the dialogs that act on a bucket, links and the command line keep the real name. An alias has at most 64 characters, and two buckets of one account cannot share an alias.

## Command line

| Command | Does |
| --- | --- |
| `pail buckets` | Lists the buckets with their region. |
| `pail mb` | Creates a bucket, in the account's region unless `--region` is given. `--object-lock` also enables versioning. |
| `pail rb` | Deletes an empty bucket, after a confirmation. |
| `pail rm -r` | With `s3://bucket` and `-r`, deletes every object in the bucket, after a confirmation. |
| `pail multipart` | `ls` lists unfinished uploads; `abort` aborts them after a confirmation. |
| `pail bucket alias` | Sets, prints or clears an alias. |

```sh
pail mb s3://my-bucket --region eu-central-1 --object-lock
pail rb s3://my-bucket
pail multipart ls s3://my-bucket/uploads/
pail multipart abort s3://my-bucket --older-than 7d
pail bucket alias s3://my-bucket "Photos"
pail bucket alias s3://my-bucket --clear
```

`--older-than` limits `multipart abort` to uploads started longer ago, for example `36h` or `7d`. The 14 bucket settings are edited only in the desktop app. Flags and confirmations are described in [Administration](../cli.md#administration) and [Confirmation](../cli.md#confirmation) of the command-line reference.
