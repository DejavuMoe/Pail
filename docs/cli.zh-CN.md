# Pail 命令行（`pail`）

[English](cli.md) | 简体中文

`pail` 与桌面应用使用同一套账户、已保存的同步作业和机密：它共用桌面应用的配置目录和机密存储（系统钥匙串或加密保险库）。账户、加密保险库和设置既可以在桌面应用中管理，也可以用命令行管理。请求经由桌面应用**设置 › 网络**里保存的代理发送；没有设置时，`pail` 沿用环境变量 `HTTP_PROXY`、`HTTPS_PROXY` 和 `NO_PROXY`。

发布包使用带版本的文件名，例如 `pail-v<version>-windows-amd64.exe`。下文假设程序安装后名为 `pail`（Windows 上为 `pail.exe`），也可以直接运行完整文件名。

```text
pail accounts
pail ls s3://my-bucket/photos/ -r
```

`pail help` 列出所有命令，`pail help COMMAND` 输出某个命令的用法和参数。

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

用法：

```text
pail accounts                                       # 列出账户
pail account ls|show|add|edit|test|rm ...           # 管理账户，见"账户"
pail vault status|init|unlock|lock|passwd|backend|reset
                                                    # 机密后端和保险库，见"加密保险库"
pail bucket alias s3://bucket [NAME | --clear]      # 存储桶的显示名称
pail buckets                                        # 列出存储桶及其区域
pail ls [s3://bucket/prefix/] [-r]                  # 列出对象和文件夹
pail cp SRC DST [-r] [--conflict POLICY] [--decode gzip|zip|auto]
                                                    # 上传、下载，或在 S3 内复制
pail mv s3://SRC s3://DST [-r] [--conflict POLICY]  # 在 S3 内移动
pail rm s3://bucket/key [-r] [--versions]           # 删除对象、文件夹或各版本
pail mb s3://bucket [--region REGION] [--object-lock]
pail rb s3://bucket                                 # 删除空的存储桶
pail presign s3://bucket/key [--expires 1h] [--method GET|PUT] [--version-id V]
pail url s3://bucket/key [--kind cdn|public]        # CDN 或公开地址，不发请求
pail versions s3://bucket/prefix/                   # 版本和删除标记
pail restore s3://bucket/key --version-id V         # 把旧版本恢复为当前版本
pail restore s3://bucket/prefix/ --deleted [-r]     # 恢复已删除的对象键
pail stat s3://bucket/key [--version-id V]          # 对象的属性
pail cat s3://bucket/key [--version-id V]           # 把对象写到 stdout
pail mkdir s3://bucket/prefix/                      # 创建文件夹
pail du s3://bucket[/prefix/]                       # 前缀下的对象数和大小
pail tags get|set s3://bucket/key ...               # 对象标签，见"对象"
pail acl get|set s3://bucket/key ...                # 对象 ACL
pail props get|set s3://bucket/key ...              # 标头和元数据
pail storage-class s3://bucket/key CLASS [-r]       # 更改存储类型
pail archive-restore s3://bucket/key [-r] [--days N] [--tier T]
                                                    # 从 Glacier 或 Deep Archive 取回
pail multipart ls|abort s3://bucket[/prefix/] ...   # 未完成的分片上传
pail cloudfront ls|invalidate ...                   # CloudFront，仅限 AWS 账户
pail settings get [KEY] | set KEY VALUE             # 与桌面应用共用的设置
pail sync LOCAL s3://bucket/prefix/ [SYNC FLAGS]    # 单向同步：上传
pail sync s3://bucket/prefix/ LOCAL [SYNC FLAGS]    # 单向同步：下载
pail sync --job NAME [--dry-run]
pail sync job ls|add|rm ...                         # 已保存的同步作业
pail version
```

### 命令详解

- `-r`（也可写 `--recursive`）在 `ls`、`cp`、`mv`、`rm` 和 `restore` 中含义相同。
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
- `sync --job NAME` 运行已保存的同步作业，除非给出 `--account`，否则使用该作业的账户。`sync job ls`、`sync job add` 和 `sync job rm` 管理已保存的作业，见"管理"。

使用 `--json` 时，`vault status` 和 `url` 的对象结构：

```text
{"type":"vault","backend":...,"vaultExists":...,"autoLockMinutes":...}
{"type":"url","kind":"cdn","url":...}
```

`kind` 为 `cdn` 或 `public`。

使用 `--json` 时，下列命令每行输出一个对象，最后是汇总。各行的 `type`：

| 命令 | type |
| --- | --- |
| `accounts`、`account ls`、`account show` | `account` |
| `stat`、`props get` | `object` |
| `du` | `du` |
| `tags get` | `tag` |
| `acl get` | `acl` |
| `multipart ls` | `upload` |
| `sync job ls` | `job` |
| `cloudfront ls` | `distribution` |
| `cloudfront invalidate` | `invalidation` |
| `settings get` | `setting` |
| `bucket alias` | `alias` |

`cat` 直接写出对象内容，没有 JSON 形式。

## CDN URL

账户可以设置一个 CDN 基础 URL，例如 `https://s3-cdn.example.com`，还可以选择性地给出它所服务的存储桶列表（默认：全部）。不含占位符时，链接为 `<base>/<bucket>/<key>`，因此 `s3://assets/logo.svg` 会变成：

```text
https://s3-cdn.example.com/assets/logo.svg
```

如果使用含 `{key}`（也可含 `{bucket}`）的模板，则按模板填充，例如：

```text
https://{bucket}.cdn.example.com/{key}
```

对象键与公开 URL 一样做百分号编码，保留 `/`。CDN URL 在桌面应用的账户对话框中设置，也可以用 `pail account edit --cdn-url` 设置。`pail url` 只读取它。

## 账户

`pail account` 管理桌面应用也在使用的账户。

```text
pail account ls
pail account show [NAME]
pail account add NAME --endpoint URL --access-key-id ID [--region R] [--path-style] [--bucket B] [--secret-stdin]
pail account edit [NAME] [--region R] [--set-secret [--secret-stdin]]
pail account test [NAME]
pail account rm [NAME]
```

`NAME` 是账户名称或 ID。省略时，命令使用 `--account` 指定的账户，或唯一的账户。`account ls` 与 `accounts` 相同。

- `add` 的参数是新账户的名称。保存前会列一次存储桶来测试连接；给出 `--bucket` 时改为列出指定的文件夹。测试失败就不保存。`--no-test` 跳过测试；匿名账户不能列出存储桶，所以需要它或 `--bucket`。
- `edit` 只修改给出的参数。`--rename NEW` 重命名账户。开关用 `--path-style=false` 关闭。给出空值，例如 `--cdn-url ""`，会清空该字段。
- `show` 输出账户设置，不含机密。静态账户还会说明私有访问密钥是已保存还是缺失。
- `test` 列出已保存账户的存储桶，或它指定的存储桶的文件夹。
- `rm` 先要求确认，再删除账户及其保存的机密。存储桶和对象不受影响。该账户已保存的同步作业会保留，但无法再运行。

账户参数对应桌面应用账户对话框中的字段：

| 参数 | 含义 |
| --- | --- |
| `--provider` | `aws` 或 `s3`。默认：给出 `--endpoint` 时为 `s3`，否则为 `aws`。 |
| `--endpoint` | S3 兼容服务的端点 URL。 |
| `--region` | 区域。 |
| `--path-style` | 以 `endpoint/bucket` 的形式访问存储桶。 |
| `--credentials` | `static`、`profile`、`default` 或 `anonymous`。默认：`static`；给出 `--profile` 时为 `profile`。 |
| `--access-key-id` | 静态凭据的访问密钥 ID，它不是机密。 |
| `--profile` | `profile` 凭据使用的 profile 名称。 |
| `--role-arn` | 要代入的角色。 |
| `--external-id` | 代入角色时使用的外部 ID。 |
| `--role-session-name` | 代入角色时使用的会话名称。 |
| `--insecure-skip-verify` | 不校验服务器的 TLS 证书。 |
| `--checksum-mode` | `auto`、`when_required` 或 `when_supported`。 |
| `--requester-pays` | 以请求者付费方式发送请求，用于所有者设置了请求者付费的存储桶。请求和数据费用由你的账户支付。 |
| `--proxy` | 账户自己的代理，例如 `socks5://127.0.0.1:1080`，不含用户名和密码；`direct` 表示不使用代理；空值表示使用设置中的代理。 |
| `--cdn-url` | CDN 基础 URL 或模板，见"CDN URL"。 |
| `--cdn-bucket` | CDN 所服务的存储桶，多个存储桶就重复使用。 |
| `--bucket` | 不经列出、直接显示的存储桶，写成 `NAME` 或 `NAME/folder/`，用于没有列出存储桶权限的密钥。多个存储桶就重复使用。该存储桶在这个文件夹中打开，`buckets` 也按 `NAME/folder/` 输出。 |

私有访问密钥和会话令牌不会从参数或环境变量读取，也不会输出，`--json` 也一样。`add` 和 `edit --set-secret` 在终端中以不回显的方式询问私有访问密钥；加上 `--secret-stdin` 则改为从 stdin 的一行读取。`--ask-session-token` 还会读取会话令牌，来自第二次提示或 stdin 的下一行。`edit` 只有在给出 `--set-secret` 时才替换已保存的机密。如果机密后端是加密保险库，会先询问它的主密码，因此使用 `--vault-password-stdin` 时主密码是第一行。

```text
$secret | pail account add backup --endpoint https://s3.example.com --region us-east-1 --path-style --access-key-id AKIAEXAMPLE --secret-stdin
```

## 加密保险库

如果机密后端是加密保险库而不是系统钥匙串，每次运行命令行时保险库都处于锁定状态。需要凭据的命令会先将其解锁：使用静态密钥的账户，以及存在 ZIP 密码等机密引用时的 `cp` 和 `sync`。主密码有两种提供方式：

- `--vault-password-stdin` 从 stdin 的第一行读取，例如 `$pw | pail --vault-password-stdin buckets`。此时 stdin 不能再用于 `Proceed? [y/N]` 提示，因此破坏性命令还需要 `--yes`。
- 在终端中，会出现不回显的提示 `Vault master password:`（最多三次尝试）。

两者都没有时，命令以 2 退出并说明原因。使用 profile、默认凭据或匿名访问的账户不会询问，`accounts` 也不会。

密码不能作为参数值或环境变量传入。解锁只在本次运行内有效。

`pail vault` 管理保险库本身：

| 命令 | 作用 |
| --- | --- |
| `vault status` | 显示机密后端和保险库文件。 |
| `vault init` | 把所有机密移入新的加密保险库。 |
| `vault backend vault` | 与 `vault init` 相同。 |
| `vault backend keychain` | 把机密移回系统钥匙串，并删除 `vault.json`。 |
| `vault unlock` | 校验主密码。 |
| `vault lock` | 不产生持续的效果，见下。 |
| `vault passwd` | 更改主密码。 |
| `vault reset` | 忘记密码时删除保险库。 |

- `init` 要求输入两次新主密码，或用 `--vault-password-stdin` 从 stdin 的第一行读取。主密码至少 8 个字符。
- `backend keychain` 需要当前的主密码。
- `unlock` 只校验密码。每次运行都从锁定状态开始，并只为本次运行解锁，所以 `vault lock` 没有可锁的内容。桌面应用有自己的锁定。
- `passwd` 先询问当前密码，再询问两次新密码。使用 `--vault-password-stdin --new-vault-password-stdin` 时读取两行：当前密码，然后是新密码。
- `reset` 删除 `vault.json`，其中的所有机密都会丢失，机密后端变回系统钥匙串。之后需要用 `account edit NAME --set-secret` 重新输入账户的机密。它会列出受影响的账户，然后在终端中要求输入 `RESET`；没有终端时需要 `--yes`。

## 对象

这些命令处理单个对象。`stat`、`cat`、`tags` 和 `acl` 可以用 `--version-id` 指定版本。

- `stat` 输出大小、修改时间、ETag、存储类型、标头、元数据、标签和加密方式。
- `cat` 原样把对象的字节写到 stdout，不输出其他内容，因此可以接管道或重定向到文件。错误写到 stderr。
- `mkdir` 创建文件夹，即一个名为 `prefix/` 的空对象。
- `du` 统计前缀下的对象，不给前缀时统计整个存储桶。它会列出前缀下的所有内容，所以前缀越大耗时越长。旧版本、删除标记和未完成的分片上传不计入。
- `tags get` 以 `KEY=VALUE` 行输出标签。`tags set s3://bucket/key K=V ...` 替换全部标签；`--merge` 保留已有标签，并添加或替换给出的标签。`tags set --clear` 在确认后删除全部标签。S3 每个对象最多允许 10 个标签。
- `acl get` 输出所有者和授权。`acl set s3://bucket/key CANNED` 应用预设 ACL：`private`、`public-read`、`public-read-write`、`authenticated-read`、`aws-exec-read`、`bucket-owner-read` 或 `bucket-owner-full-control`。关闭了 ACL 的存储桶会报告不支持 ACL。
- `props set` 修改标头和用户元数据：`--content-type`、`--content-encoding`、`--content-language`、`--content-disposition`、`--cache-control`、`--expires`（HTTP 日期）、`--metadata KEY=VALUE`（可重复，键用小写）、`--delete-header NAME` 和 `--delete-metadata KEY`。给出空值，例如 `--cache-control ""`，会删除该标头。`props get` 与 `stat` 相同。
- `storage-class s3://bucket/key CLASS` 更改存储类型，例如 `STANDARD_IA` 或 `GLACIER_IR`。
- `archive-restore s3://bucket/key` 把存储类型为 `GLACIER` 或 `DEEP_ARCHIVE` 的对象取回 `--days` 天（默认 7，最多 365）。`--tier` 为 `standard`（默认）、`bulk` 或 `expedited`；Deep Archive 不支持 `expedited`。命令在提交取回请求后返回，取回本身需要几分钟到几小时，`stat` 的 `restore` 显示进度。其他存储类型的对象和正在取回的对象会跳过。取回由服务商另行收费。

`props set` 和 `storage-class` 会原地重写对象，因此对象的修改时间会变化，开启版本控制的存储桶会多出一个新版本。加上 `-r` 时，它们作用于前缀下的所有对象，并会先输出范围、要求确认。

## 管理

- `bucket alias s3://bucket NAME` 设置桌面应用中显示的存储桶名称。`NAME` 为空或使用 `--clear` 会移除别名。省略 `NAME` 时输出当前别名。别名只保存在 Pail 的配置中，存储服务上没有任何变化，命令仍然使用真实的存储桶名称。
- `multipart ls s3://bucket[/prefix/]` 列出未完成的分片上传，包括开始时间、存储类型、上传 ID 和对象键。`multipart abort` 使用同样的路径，在确认后中止这些上传，并丢弃已上传的分片。`--older-than D` 只处理开始于 `D` 之前的上传，例如 `36h` 或 `7d`。
- `sync job ls` 列出已保存的同步作业。`sync job add NAME LOCAL s3://bucket/prefix/` 保存一个上传作业，`sync job add NAME s3://bucket/prefix/ LOCAL` 保存一个下载作业，`--mode`、`--compare`、`--exclude` 和 `--no-rules` 与 `sync` 相同。本地文件夹必须已存在。`sync job rm NAME` 在确认后删除已保存的作业，本地文件和对象不受影响。用 `sync --job NAME` 运行作业。
- `cloudfront ls` 列出账户的 CloudFront 分配；`--bucket B` 只保留源站在该存储桶的分配。`cloudfront invalidate DISTRIBUTION PATH...` 先输出分配和路径并要求确认，再创建失效。它只适用于 AWS 账户。
- `settings get` 以 `KEY VALUE` 输出所有设置；`settings get KEY` 只输出值。`settings set KEY VALUE` 修改一项。它们就是桌面应用的设置。

| 设置 | 取值 |
| --- | --- |
| `theme` | `system`、`light` 或 `dark` |
| `upload-limit` | 并行上传数，1 到 64 |
| `download-limit` | 并行下载数，1 到 64 |
| `other-limit` | 其他任务的并行数，1 到 64 |
| `bulk-workers` | 一个批量任务内的工作线程数，1 到 64 |
| `part-size-mib` | 分片大小，单位 MiB，5 到 5120 |
| `upload-kibps` | 上传限速，单位 KiB/s，所有账户共用，0 表示不限 |
| `download-kibps` | 下载限速，单位 KiB/s，所有账户共用，0 表示不限 |
| `download-dir` | 默认下载文件夹，留空表示没有 |
| `conflict` | `skip`、`overwrite`、`rename`、`newer` 或 `different_size` |
| `show-versions` | `on` 或 `off` |
| `log-to-file` | `on` 或 `off` |
| `vault-auto-lock-minutes` | 桌面应用在空闲多少分钟后锁定保险库，0 表示不锁定 |
| `hide-tray-icon` | `on` 或 `off` |
| `quit-when-idle` | `on` 或 `off`；没有传输在进行时，关闭桌面窗口即退出 |
| `recent-ops` | 保留的最近操作条数，1 到 1000 |
| `keep-recent-ops` | `on` 或 `off` |
| `secret-backend` | 只读。用 `vault init` 和 `vault backend` 更改。 |

代理不在 `settings` 里，请在桌面应用中设置。
## 进度与输出

每完成一个项目，stderr 上输出一行（`done`、`skipped`、`FAILED`、`UNKNOWN` 或 `unprocessed`），最后是一行汇总。在终端中，会有一行实时显示项目数、字节数和速度。需要机器可读的结果时使用 `--json`；否则 stdout 只输出列表、URL 和同步计划。

## 确认

`rm`、`rb`、`mv`，以及会删除内容的 `sync`，会先输出范围：账户、存储桶和前缀、对象数和字节数，`sync` 还有删除数量。下载方向镜像的本地删除会进入系统回收站。在终端中，它们会询问 `Proceed? [y/N]`；没有终端时，除非给出 `--yes`，否则以 2 退出。`cp` 不需要确认，包括 `--conflict overwrite`。`sync --dry-run` 输出计划，不做修改，也不询问，以 0 退出。

下列命令以同样的方式询问：`account rm`、`sync job rm`、`multipart abort`（有匹配项时）、`cloudfront invalidate`、`tags set --clear`，以及带 `-r` 的 `props set`、`storage-class` 和 `archive-restore`。`vault reset` 要求输入 `RESET`，而不是 `y`。

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
