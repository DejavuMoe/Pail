# Pail 命令行（`pail`）

[English](cli.md) | 简体中文

`pail` 与桌面应用使用同一套账户、已保存的同步作业和机密：它共用桌面应用的配置目录和机密存储（系统钥匙串或加密保险库）。命令行不能添加或修改账户，请在桌面应用中操作。

发布包使用带版本的文件名，例如 `pail-v<version>-windows-amd64.exe`。下文假设程序安装后名为 `pail`（Windows 上为 `pail.exe`），也可以直接运行完整文件名。

```text
pail accounts
pail ls s3://my-bucket/photos/ -r
```

S3 路径写作 `s3://bucket/key-or-prefix/`，其他参数都视为本地路径。

参数前可以写一个或两个短横线：`-json` 和 `--json` 等价，本页统一写两个。`pail version` 输出 `pail <version>`。命令行没有 `--version` 参数，桌面应用有。

## 全局参数

全局参数可以放在命令名之前或之后。

| 参数 | 含义 |
| --- | --- |
| `--account` | 账户名称或 ID。默认：唯一已配置的账户。有多个账户时命令失败（退出码 2）并列出这些账户。 |
| `--config-dir` | 配置目录。默认：桌面应用所用的目录，即 `<user config dir>/Pail`。 |
| `--json` | 在 stdout 输出按行分隔的 JSON：每个列表行或每个项目结果一个对象，最后是一个 `"type":"summary"` 的对象。失败时以 `"type":"error"` 的对象结束。 |
| `--quiet` | stderr 上不输出逐项行和进度。 |
| `--yes` | 所有确认都回答"是"。 |
| `--timeout` | 超过该时长后中止，例如 `10m`，运行中的任务会被取消。`0` 表示不限时。 |
| `--vault-password-stdin` | 从 stdin 的第一行读取加密保险库的主密码，见"加密保险库"。 |

## 命令

| 命令 | 作用 |
| --- | --- |
| `accounts` | 列出账户的名称、ID、服务商、端点和区域。 |
| `vault` | 显示机密后端和加密保险库的状态。 |
| `buckets` | 列出存储桶及其区域。 |
| `ls` | 列出对象和文件夹。 |
| `cp` | 从本地复制到 S3（上传）、从 S3 复制到本地（下载），或在同一账户的 S3 内复制。 |
| `mv` | 在 S3 内移动对象。 |
| `rm` | 删除对象、文件夹，或某个对象键的所有版本。 |
| `mb` | 创建存储桶。 |
| `rb` | 删除空的存储桶。 |
| `presign` | 生成预签名 URL。 |
| `url` | 不发出请求，直接输出对象的 CDN 或公开地址。 |
| `versions` | 列出版本和删除标记。 |
| `restore` | 把旧版本恢复为当前版本，或恢复已删除的对象键。 |
| `sync` | 在本地文件夹和 S3 前缀之间做单向同步。 |
| `version` | 输出版本号。 |

用法：

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

### 命令详解

- `-r`（也可写 `-recursive`）在 `ls`、`cp`、`mv`、`rm` 和 `restore` 中含义相同。
- `vault status` 显示机密后端（`keyring` 或 `vault`）、`vault.json` 是否存在，以及桌面应用的自动锁定设置。加上 `--vault-password-stdin` 时还会校验主密码。使用 `--json` 时输出一个对象（结构见下）。
- `ls` 把文件夹显示为 `PRE name/`，大小以易读格式显示（`--json` 时为原始字节数）。不带路径时列出存储桶。`-r` 列出前缀下的所有对象键。末尾没有 `/` 且不是文件夹的对象键，只列出该单个对象。
- `cp` 和 `mv` 的 `--conflict` 取值为 `skip`、`overwrite`、`rename`、`newer` 或 `different_size`。省略时都使用桌面应用设置中保存的默认值（初始为 `rename`）。`mv` 先复制，校验后再删除源对象。
- `cp --decode`（`gzip`、`zip` 或 `auto`）在下载时解码对象，对上传和 S3 内复制不起作用。
- 目标路径：上传和 S3 内复制的目标始终是文件夹（缺少末尾的 `/` 会自动补上），源名称保留在其下，与 `cp -r src dst/` 相同。下载的目标是本地目录，不存在则创建。
- `rm -r` 把该对象键当作文件夹处理；`s3://bucket` 加 `-r` 表示整个存储桶。`rm --versions` 永久删除某个对象键的所有版本和删除标记。
- `mb` 默认在账户的区域创建存储桶，用 `--region` 可指定其他区域。`--object-lock` 同时启用版本控制。
- `presign` 生成有效期为 `--expires`（默认 `1h`）的 URL，`--method` 为 `GET`（默认）或 `PUT`。
- 账户为该存储桶设置了 CDN URL 时（见"CDN URL"），`url` 输出 CDN 链接，否则输出公开的 S3 地址。`--kind cdn` 在未配置时以 1 退出；`--kind public` 始终输出 S3 地址。给出前缀而不是对象键属于用法错误（退出码 2）。使用 `--json` 时输出一个对象（结构见下），随后是汇总。
- `versions` 列出一层文件夹，包含删除标记。
- `restore --version-id` 把旧版本恢复为当前版本。`restore --deleted` 恢复前缀下最新记录为删除标记的对象键（加 `-r` 包含子文件夹）。版本历史会保留。
- `sync` 做单向同步。`--mode` 为 `new`、`new_changed`（默认）或 `mirror`，`mirror` 还会删除。`--compare` 为 `size_time`（默认）、`size` 或 `checksum`。`--exclude GLOB` 跳过相对路径匹配该通配符的文件，可重复使用。`--no-rules` 不应用上传规则。`--dry-run` 输出计划后退出，不做任何修改。
- `sync --job NAME` 运行在桌面应用中保存的作业，除非给出 `--account`，否则使用该作业的账户。

使用 `--json` 时，`vault status` 和 `url` 的对象结构：

```text
{"type":"vault","backend":...,"vaultExists":...,"autoLockMinutes":...}
{"type":"url","kind":"cdn","url":...}
```

`kind` 为 `cdn` 或 `public`。

## CDN URL

账户可以设置一个 CDN 基础 URL，例如 `https://s3-cdn.example.com`，还可以选择性地给出它所服务的存储桶列表（默认：全部）。不含占位符时，链接为 `<base>/<bucket>/<key>`，因此 `s3://assets/logo.svg` 会变成：

```text
https://s3-cdn.example.com/assets/logo.svg
```

如果使用含 `{key}`（也可含 `{bucket}`）的模板，则按模板填充，例如：

```text
https://{bucket}.cdn.example.com/{key}
```

对象键与公开 URL 一样做百分号编码，保留 `/`。CDN URL 在桌面应用的账户对话框中设置，命令行只读取它。

## 加密保险库

如果桌面应用把机密存放在加密保险库而不是系统钥匙串中，每次运行命令行时保险库都处于锁定状态。需要凭据的命令会先将其解锁：使用静态密钥的账户，以及存在 ZIP 密码等机密引用时的 `cp` 和 `sync`。主密码有两种提供方式：

- `--vault-password-stdin` 从 stdin 的第一行读取，例如 `$pw | pail --vault-password-stdin buckets`。此时 stdin 不能再用于 `Proceed? [y/N]` 提示，因此破坏性命令还需要 `--yes`。
- 在终端中，会出现不回显的提示 `Vault master password:`（最多三次尝试）。

两者都没有时，命令以 2 退出并说明原因。使用 profile、默认凭据或匿名访问的账户不会询问，`accounts` 也不会。

密码不能作为参数值或环境变量传入。解锁只在本次运行内有效。命令行只负责解锁：创建保险库、更改密码和重置，请在桌面应用中进行。

## 进度与输出

每完成一个项目，stderr 上输出一行（`done`、`skipped`、`FAILED`、`UNKNOWN` 或 `unprocessed`），最后是一行汇总。在终端中，会有一行实时显示项目数、字节数和速度。需要机器可读的结果时使用 `--json`；否则 stdout 只输出列表、URL 和同步计划。

## 确认

`rm`、`rb`、`mv`，以及会删除内容的 `sync`，会先输出范围：账户、存储桶和前缀、对象数和字节数，`sync` 还有删除数量。下载方向镜像的本地删除会进入系统回收站。在终端中，它们会询问 `Proceed? [y/N]`；没有终端时，除非给出 `--yes`，否则以 2 退出。`cp` 不需要确认，包括 `--conflict overwrite`。`sync --dry-run` 输出计划，不做修改，也不询问，以 0 退出。

## 退出码

| 退出码 | 含义 |
| --- | --- |
| 0 | 成功。跳过的项目算作成功。 |
| 1 | 失败：出错、任务失败、所有项目都失败，或确认被拒绝。 |
| 2 | 用法错误或输入无效；需要确认但没有终端且没有 `--yes`；或加密保险库已锁定而无法询问密码（没有终端，也没有 `--vault-password-stdin`）。 |
| 3 | 部分失败：部分项目成功，其余失败、结果未知（不会重试）或未处理。 |
| 130 | 被 Ctrl+C 中断。运行中的任务被取消，并有最多 10 秒的时间停止。未完成的下载写入临时文件，这些文件会被删除。 |

错误会用文字（英文原文）说明类别：not found、access denied、not supported by this provider、conflict、network error、canceled、invalid input、locked。保险库密码错误会报告为 access denied（退出码 1）。

## 限制

- 不能即时重命名目标，S3 路径不做通配符展开，`versions` 只列一层文件夹，`rm --versions` 只接受单个对象键。
- 复制和移动只能在同一个账户内进行。
