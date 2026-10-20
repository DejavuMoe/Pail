# Pail 命令行（`pail`）

[English](cli.md) | 简体中文

`pail` 是 `internal/app` 之上的一层轻量前端。它与桌面应用共用配置目录和秘密存储（系统钥匙串或加密保险库），因此在桌面应用中创建的账户和保存的同步作业，在这里同样可用。不能通过命令行添加或修改账户，任何命令都不会输出凭据。

```powershell
go build -o bin/pail.exe ./cmd/pail
pail accounts
pail ls s3://my-bucket/photos/ -r
```

S3 路径写作 `s3://bucket/key-or-prefix/`；其他参数一律视为本地路径。

## 全局参数

可放在命令名之前或之后。

| 参数 | 含义 |
| --- | --- |
| `--account NAME\|ID` | 要使用的账户。默认：唯一已配置的账户；有多个账户时命令失败（退出码 2）并列出这些账户。 |
| `--config-dir DIR` | 配置目录（默认：桌面应用所用的目录，`<user config dir>/Pail`）。 |
| `--json` | 在 stdout 输出按行分隔的 JSON：每个列表行或每个项目结果一个对象，最后是一个 `{"type":"summary",...}` 对象。失败时以 `{"type":"error",...}` 结束。 |
| `--quiet` | stderr 上不输出逐项行和进度。 |
| `--yes` | 所有确认一律视为同意。 |
| `--timeout D` | D 时间后中止（例如 `10m`）；运行中的任务会被取消。0 表示不限时。 |
| `--vault-password-stdin` | 从 stdin 的第一行读取加密保险库的主密码（见下文）。 |

## 命令

| 命令 | 说明 |
| --- | --- |
| `accounts` | 名称、ID、服务商、端点、区域。 |
| `vault status [--vault-password-stdin]` | 秘密后端（`keyring` 或 `vault`）、`vault.json` 是否存在，以及桌面应用的自动锁定设置。带 `--vault-password-stdin` 时还会校验主密码。`--json` 输出 `{"type":"vault","backend":...,"vaultExists":...,"autoLockMinutes":...}`。绝不输出秘密。 |
| `buckets` | 存储桶及其区域。 |
| `ls [s3://b/prefix/] [-r]` | 文件夹显示为 `PRE name/`；大小以易读格式显示（`--json` 时为原始字节数）。不带路径时：列出存储桶。末尾没有 `/` 且不是文件夹的对象键，只列出该单个对象。 |
| `cp SRC DST [-r] [--conflict P] [--decode gzip\|zip\|auto]` | 本地到 S3（上传），S3 到本地（下载），S3 到 S3（在账户内复制）。 |
| `mv SRC DST [-r] [--conflict P]` | 仅限 S3 到 S3。先复制，校验后再删除源。 |
| `rm s3://b/key [-r] [--versions]` | 删除。`-r` 把该键当作文件夹处理（`s3://b` 加 `-r` 表示整个存储桶）。`--versions` 永久删除某个键的所有版本和删除标记。 |
| `mb s3://b [--region R] [--object-lock]`, `rb s3://b` | 创建 / 删除（空的）存储桶。 |
| `presign s3://b/key [--expires 1h] [--method GET\|PUT] [--version-id V]` | 输出 URL。 |
| `url s3://b/key [--kind cdn\|public]` | 不发出任何请求，直接输出对象的地址。默认：账户为该存储桶设置了 CDN URL 时（在桌面应用的账户对话框中设置；见下文）输出 CDN 链接，否则输出公开的 S3 地址。`--kind cdn` 在未配置时以 1 退出；`--kind public` 始终输出 S3 地址。`--json` 输出 `{"type":"url","kind":"cdn"\|"public","url":...}` 和汇总。给出前缀属于用法错误（退出码 2）。 |
| `versions s3://b/prefix/` | 列出一层文件夹；包含删除标记。 |
| `restore s3://b/key --version-id V` / `restore s3://b/prefix/ --deleted [-r]` | 把旧版本恢复为当前版本 / 恢复最新记录为删除标记的键。历史记录绝不会被删除。 |
| `sync LOCAL s3://b/prefix/` / `sync s3://b/prefix/ LOCAL` | `--mode new\|new_changed\|mirror`、`--compare size_time\|size\|checksum`、可重复的 `--exclude GLOB`、`--no-rules`、`--dry-run`。 |
| `sync --job NAME [--dry-run]` | 运行在桌面应用中保存的作业（除非给出 `--account`，否则使用该作业的账户）。 |
| `version` | 输出 `pail <version>`。 |

`--conflict` 取值为 `skip`、`overwrite`、`rename`、`newer` 或 `different_size`。省略时，`cp` 和 `mv` 都使用桌面应用设置中保存的默认值（初始为 `rename`）。

目标路径规则：上传或 S3 复制的目标始终是文件夹（缺少末尾的 `/` 会自动补上），源名称保留在其下，与 `cp -r src dst/` 相同。下载的目标始终是本地目录（不存在则创建）。

## CDN URL

账户可以设置一个 CDN 基础 URL，例如 `https://s3-cdn.example.com`，还可以选择性地给出它所服务的存储桶列表（默认：全部）。不含占位符时，链接为 `<base>/<bucket>/<key>`，因此 `s3://assets/logo.svg` 会变成 `https://s3-cdn.example.com/assets/logo.svg`。如果使用含 `{key}`（也可含 `{bucket}`）的模板，例如 `https://{bucket}.cdn.example.com/{key}`，则按模板填充。对象键与公开 URL 一样做百分号编码，保留 `/`。该设置存储在 `accounts.json` 中，字段为 `cdnUrl` / `cdnBuckets`，在桌面应用里编辑；命令行只读取它。

## 加密保险库

当桌面应用把秘密存放在加密保险库（`vault.json`，由主密码保护）而不是系统钥匙串中时，每次运行命令行时保险库都处于锁定状态。需要凭据的命令（使用静态密钥的账户；存在 ZIP 密码等秘密引用时的 `cp` 和 `sync`）会先将其解锁：

- `--vault-password-stdin`：密码取自 stdin 的第一行，例如 `$pw | pail --vault-password-stdin buckets`。此时 stdin 不能再用于 `Proceed? [y/N]` 提示，因此破坏性命令还需要 `--yes`。
- 在终端中，会出现不回显的提示 `Vault master password:`（最多三次尝试）。
- 两者都没有时，命令以 2 退出并给出相应说明。使用 profile、默认凭据或匿名访问的账户不会询问，`accounts` 也不会。

密码绝不作为参数值或环境变量接受（两者都能被其他进程看到），不会写入日志，也不会回显。解锁只在本次运行内有效；命令行绝不写入保险库、创建保险库、更改其密码或重置它：请使用桌面应用。

## 进度与输出

每完成一个项目，stderr 上输出一行（`done`、`skipped`、`FAILED`、`UNKNOWN`、`unprocessed`），最后是一行汇总。在终端中，会有一行实时显示项目数、字节数和速度。机器可读的结果请用 `--json`；否则 stdout 只输出列表、URL 和同步计划。

## 确认

`rm`、`rb`、`mv`，以及会删除内容的 `sync`，会先输出范围：账户、存储桶/前缀、对象数和字节数（用 `Estimate` 统计），对于 sync 还有删除数量（下载方向镜像的本地删除会进入系统回收站）。stdin 是终端时，它们会询问 `Proceed? [y/N]`；不是终端时，除非给出 `--yes`，否则以 2 退出。`cp` 从不需要确认，包括 `--conflict overwrite`。`sync --dry-run` 输出计划后以 0 退出，不做任何修改，也不询问。

## 退出码

| 退出码 | 含义 |
| --- | --- |
| 0 | 成功（跳过的项目算作成功）。 |
| 1 | 失败：出错、任务失败、所有项目都失败，或确认被拒绝。 |
| 2 | 用法错误或输入无效、需要确认但没有终端且没有 `--yes`，或加密保险库已锁定而无法询问密码（没有终端，也没有 `--vault-password-stdin`）。 |
| 3 | 部分失败：部分项目成功，部分失败、结果未知（结果不确定，绝不重放）或未处理。 |
| 130 | 被中断（Ctrl+C）：运行中的任务被取消，并有最多 10 秒的时间停止。下载写入的是会被删除的临时文件，因此不会留下残缺的文件。 |

错误会用文字（英文原文）说明类别：not found、access denied、not supported by this provider、conflict、network error、canceled、invalid input、locked。保险库密码错误属于 "access denied"（退出码 1）。

## 限制

- 不能即时重命名目标；S3 路径不做通配符展开；`versions` 只列一层文件夹；`rm --versions` 只接受单个键。
- 复制和移动只能在同一个账户内进行。
- 验证：这些命令由针对 `internal/s3test` 的进程内测试覆盖（协议层）。其下的应用层也针对 MinIO 和自建 VersityGW 测试过（见 [status.md](status.md)）；命令行前端本身尚未对真实服务端运行过。
