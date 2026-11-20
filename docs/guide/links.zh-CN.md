# 链接与 CDN

复制对象的地址，生成在一段时间内有效的预签名 URL，为账户设置 CDN 地址，并为 AWS 账户管理 CloudFront。这些操作在命令行里也都能完成。

## 复制对象地址

选中对象，右键单击，打开 **复制链接**。子菜单里有：

| 命令 | 复制的内容 |
| --- | --- |
| **复制对象键** | 对象键，例如 `photos/2026/cat.jpg`。 |
| **复制 URI** | S3 地址，例如 `s3://my-bucket/photos/2026/cat.jpg`。 |
| **复制 URL** | 对象公开时的地址。只有存储桶或对象允许公开读取时才能打开。 |
| **复制 CDN URL** | CDN 地址，前提是账户为这个存储桶设置了 CDN URL。 |
| **复制预签名 URL…** | 打开预签名 URL 对话框，见下文。 |

这些命令也在命令面板里。检查器显示所选对象的 **URI** 和 **URL**，有 CDN 地址时还显示 **CDN URL**。

## 预签名 URL

预签名 URL 让持有它的人无需账户就能下载对象或上传到某个对象键，直到链接过期。

1. 选中一个或多个对象，选择 **复制链接 › 复制预签名 URL…**。所选内容里的文件夹会被跳过。
2. 选择 **方法**。GET 让持有者下载对象；PUT 让持有者向这个对象键上传文件，替换原有内容。
3. 选择 **有效期**。
4. 点击 **生成**。URL 下方有一行说明方法、过期时间和有效时长。
5. 点击 **复制**；多个对象时点击 **全部复制**。

任何拿到预签名 URL 的人都能在过期前使用它，所以有效期选能满足需要的最短时长。

Pail 把预签名 URL 作为机密内容放进剪贴板，Windows 剪贴板历史、macOS 通用剪贴板以及遵守这一标记的剪贴板管理器都不会保留它。

### 有效期

**有效期** 可选 **15 分钟**、**1 小时**、**1 天** 和 **7 天**。**自定义** 需要填写 **时长**，单位为 **分钟**、**小时** 或 **天**。最长 7 天，这是 S3 的上限。

用临时凭证（例如会话令牌或代入的角色）签名的 URL，在这些凭证过期时就会失效，即使它自己的有效期还没到。

### 单个版本或多个对象

- 单个版本：为单个对象生成 GET 链接时，可以填写 **版本 ID（可选）**；留空表示最新版本。在检查器的 **版本** 标签页里，**为此版本生成预签名 URL** 会打开对话框并填好版本。PUT 链接不能指向某个版本。
- 多个对象：选中多个对象时，对话框用同样的方法和有效期为每个对象签名。结果是一个列表，每个 URL 旁边都有复制按钮；**全部复制** 一次复制全部链接。

生成之后，**复制公开 URL** 改为复制这些对象的公开地址。如果每个对象都有 CDN 地址，这个按钮就是 **复制 CDN URL**。

## CDN URL

账户可以设置一个 CDN 地址。之后，CDN 所服务的存储桶里的每个对象都会显示 CDN 链接。Pail 只负责生成链接，不会配置 CDN。

1. 为这个账户选择 **编辑账户…**。
2. 填写 **CDN URL**，例如 `https://cdn.example.com`。它必须是 http 或 https 地址，不能带用户名、密码、查询参数或片段。
3. 可选填写 **CDN 存储桶**，多个存储桶用逗号分隔。留空表示账户的所有存储桶都使用这个 CDN URL。
4. 保存账户。

### 链接如何生成

不带占位符的基础 URL 生成 `base/bucket/key`。CDN URL 为 `https://s3-cdn.example.com` 时，对象 `s3://assets/logo.svg` 的链接是：

```text
https://s3-cdn.example.com/assets/logo.svg
```

需要其他结构时，用 `{bucket}` 和 `{key}` 写成模板。使用了 `{bucket}` 的模板必须同时包含 `{key}`。例如：

```text
https://{bucket}.cdn.example.com/{key}
```

对象键的百分号编码方式与公开 URL 相同，`/` 保持不变。

### CDN 链接出现的位置

- 检查器里的 **CDN URL** 一行，带 **复制 CDN URL** 和 **打开 CDN URL**。
- 对象菜单里的 **复制链接 › 复制 CDN URL**，以及 **打开 CDN URL**，后者在浏览器里打开单个对象的链接。选中多个对象时，**复制 CDN URL** 每行复制一个链接。
- 向 CDN 存储桶上传单个文件后，通知里会提供 **复制 CDN 链接**。

## CloudFront

AWS 账户可以管理自己的 CloudFront 分配：让缓存的路径失效，以及启用或停用分配。从侧栏 **工具** 分组里打开 **CloudFront**，也可以从命令面板打开，或用 `pail-desktop --tool cloudfront` 启动应用。

工具列出账户的分配，包括 **域名**、**别名**、**状态** 以及是否 **已启用**。选中一个分配即可查看它的失效记录。其他类型的账户会看到提示：CloudFront 仅适用于 AWS 账户。

### 失效

失效请求让 CloudFront 丢弃某些路径的缓存副本。创建方法：

1. 选中一个分配，点击 **新建失效…**。
2. 在 **路径** 里每行填写一个路径，以 `/` 开头。`*` 只能作为最后一个字符，匹配该位置以下的所有内容，例如 `/images/*`。**使整个存储桶失效** 会填入 `/*`。一次请求最多 3,000 个路径。
3. 点击 **创建失效**。

也可以从浏览器开始。选中对象或文件夹，在对象菜单里选择 **创建失效…**：对象使用它自己的路径，文件夹使用路径加 `*`。存储桶菜单里的 **创建 CloudFront 失效…** 会填入 `/*`。

**失效** 列表显示每个失效请求的 **状态** 和 **创建时间**。有请求处于 InProgress 时，列表每 10 秒刷新一次；**刷新失效列表** 立即重新读取。

### 启用或停用分配

选中一个分配，点击 **停用** 或 **启用**，然后确认。CloudFront 需要几分钟部署这项更改。停用后，分配不再通过它的域名提供内容，但分配本身和它的设置都会保留。

## 命令行

| 命令 | 作用 |
| --- | --- |
| `pail presign` | 为一个对象输出预签名 URL。 |
| `pail url` | 输出对象的 CDN 链接或公开地址，不发送请求。 |
| `pail cloudfront ls` | 列出 AWS 账户的 CloudFront 分配。 |
| `pail cloudfront invalidate` | 确认后创建失效请求。 |

```sh
pail presign s3://my-bucket/report.pdf --expires 1h
pail presign s3://my-bucket/inbox/upload.zip --method PUT --expires 15m
pail presign s3://my-bucket/report.pdf --version-id 3HL4kqtJlcpXroDTDmJ
pail url s3://my-bucket/report.pdf
pail url s3://my-bucket/report.pdf --kind public
pail cloudfront ls --bucket my-bucket
pail cloudfront invalidate E2EXAMPLE /index.html /images/*
```

- `presign` 默认生成 GET 链接，有效期 1 小时。`--expires` 接受 `15m`、`1h` 或 `168h`（7 天）这样的时长。
- 账户为该存储桶设置了 CDN URL 时，`url` 输出 CDN 链接，否则输出公开地址。没有配置 CDN URL 时，`--kind cdn` 以退出码 1 结束。
- `cloudfront ls --bucket` 只保留源站在该存储桶里的分配。
- 启用和停用分配只能在桌面应用里操作。

CDN URL 用 `pail account edit --cdn-url` 和 `--cdn-bucket` 设置。详见命令行参考的 [CDN URL](../cli.zh-CN.md#cdn-urls) 和 [管理](../cli.zh-CN.md#administration)。
