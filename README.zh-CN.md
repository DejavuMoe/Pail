<p align="center">
  <img src="assets/brand/pail-icon.svg" width="80" height="80" alt="">
</p>

<h1 align="center">Pail</h1>

<p align="center">
  Amazon S3、Cloudflare R2、MinIO 等 S3 兼容存储的桌面客户端。<br>
  支持 Windows、macOS 和 Linux，免费开源。
</p>

<p align="center">
  <a href="https://pail.download/zh/">网站</a> ·
  <a href="https://github.com/DejavuMoe/Pail/releases/latest">下载</a> ·
  <a href="https://pail.download/zh/docs/">文档</a> ·
  <a href="README.md">English</a>
</p>

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/images/main-dark.webp">
  <img src="docs/images/main-light.webp" alt="Pail 主窗口：存储桶列表、对象表格和检查器">
</picture>

## 功能

- **账户**：支持访问密钥、AWS 配置文件、默认凭据链、AssumeRole 和匿名访问。AWS 以外的服务填写端点、区域和路径风格寻址，保存前可以先测试连接。
- **浏览**：可排序、可多选的对象表格，自动分页；有面包屑、筛选、平铺视图和检查器。可以预览文本文件（JSON、CSV、XML 等）、图片和十六进制内容，复制对象键、URI、URL 和预签名链接。
- **传输**：上传、下载文件、文件夹、指定版本或整个存储桶，也可以把文件直接拖进窗口。上传和下载分开排队，并发数可调，显示进度和速度，可以取消或重试。目标已存在时可选跳过、覆盖、两者都保留、较新时覆盖或大小不同时覆盖。大文件分段上传并带校验和；下载先写入临时文件，校验通过后才放到目标位置。
- **对象属性**：为单个或多个对象修改请求头、元数据、标签、ACL、存储类别和服务端加密（SSE-S3、SSE-KMS、SSE-C），没改动的属性保持原样。
- **版本**：列出版本和删除标记，一次下载多个版本，恢复旧版本或已删除的对象键，只永久删除你选中的版本。
- **存储桶**：创建存储桶（可设区域和对象锁定）、清空、删除，复制或移动整个存储桶的内容。用表单或 JSON 编辑 14 项设置：版本控制、加密、策略、CORS、生命周期、日志、标签、静态网站、公共访问阻止、对象所有权、对象锁定、请求者付费、传输加速和复制。列出并中止未完成的分段上传。
- **同步任务**：保存上传、下载或镜像文件夹的任务。可按大小和时间、仅大小或校验和比较，按通配符排除文件，执行前先查看计划。
- **上传规则**：按存储桶和对象键匹配，自动设置请求头、存储类别和加密，或用 GZIP、ZIP、AES-256 ZIP 压缩。下载时可以解码压缩过的对象。
- **链接**：生成带有效期的 GET 或 PUT 预签名链接，可指定版本，也可以一次处理多个对象。每个账户可以设置 CDN 域名，上传完成后可直接复制 CDN 链接。AWS 账户可以提交 CloudFront 失效请求。
- **界面**：命令面板（`Ctrl+K`，macOS 上是 `⌘K`）和键盘快捷键；英文与简体中文，浅色与深色主题。

## 安装

从 [最新版本](https://github.com/DejavuMoe/Pail/releases/latest) 下载对应系统的压缩包。镜像 [pan.zsh.moe/Pail](https://pan.zsh.moe/Pail/) 上有相同的文件。

- **Windows**：`Pail-v<version>-windows-amd64.zip`（x64）或 `Pail-v<version>-windows-arm64.zip`（Arm）
- **macOS 11 及以上**：`Pail-v<version>-darwin-arm64.zip`（Apple 芯片）或 `Pail-v<version>-darwin-amd64.zip`（Intel）
- **Linux**：`Pail-v<version>-linux-amd64.tar.gz`（x64）或 `Pail-v<version>-linux-arm64.tar.gz`（Arm）

每个压缩包都含桌面应用和命令行工具 `pail`。macOS 压缩包里是 `Pail.app`；Linux 压缩包还带 `.desktop` 文件和图标。不提供 32 位版本。

可执行文件名包含版本和平台，例如 `pail-v0.3.0-windows-amd64.exe`。本文的示例使用短名称 `pail-desktop` 和 `pail`，安装时可以重命名或创建别名。Linux 的 `.desktop` 文件从 `PATH` 中启动带版本号的桌面程序。

### 校验下载

计算压缩包的 SHA-256，与同一版本 `SHA256SUMS` 中对应的那一行比较：

```sh
sha256sum Pail-v0.3.0-linux-amd64.tar.gz           # Linux
shasum -a 256 Pail-v0.3.0-darwin-arm64.zip         # macOS
Get-FileHash Pail-v0.3.0-windows-amd64.zip         # Windows PowerShell
```

### 在 macOS 上首次打开

应用没有签名和公证，macOS 会拒绝打开下载的副本。解压后把 `Pail.app` 移到"应用程序"，然后清除一次隔离标记：

```sh
xattr -dr com.apple.quarantine /Applications/Pail.app
```

### 在 Windows 上首次打开

可执行文件没有签名。如果 SmartScreen 提示"Windows 已保护你的电脑"，点击**更多信息**，再点击**仍要运行**。

## 快速开始

1. 启动 `pail-desktop`，打开侧栏顶部的账户菜单，添加账户。自建或第三方服务需要填写：
   - 端点，例如 `https://s3.example.com`，以及区域，例如 `us-east-1`；
   - 访问密钥 ID 和私有访问密钥；
   - **路径风格寻址**：MinIO 和大多数自建网关需要打开，AWS 保持关闭；
   - 可选的 **CDN URL**，例如 `https://cdn.example.com`。

   点击**测试连接**，然后保存。私有访问密钥会存进系统钥匙串。
2. 在侧栏选择存储桶。把文件拖进窗口即可上传；选中对象后点击**下载**保存到本地。
3. 没有账户也可以先试用：运行 `pail-desktop --demo`。它在应用内启动一个内存 S3 服务，不发送任何数据，也不保存任何内容。

`pail-desktop` 还支持这些参数：

| 参数 | 作用 |
| --- | --- |
| `--config-dir` | 使用其他配置目录。 |
| `--theme` | `system`、`light` 或 `dark`。 |
| `--size` | 初始窗口大小，例如 `1280x800`。 |
| `--tool` | 启动时打开工具：`sync`、`cloudfront`、`rules`、`mime`、`multipart` 或 `settings`。 |
| `--version` | 输出版本号后退出。 |

## 命令行

`pail` 使用桌面应用的账户和已保存的同步任务，账户需要先在桌面应用里添加。

```sh
pail accounts
pail ls s3://my-bucket/photos/ -r
pail cp ./dist s3://my-bucket/site/ -r --conflict newer
pail sync ./photos s3://my-bucket/photos/ --dry-run
pail presign s3://my-bucket/report.pdf --expires 1h
```

全部命令、参数、JSON 输出和退出码见 [命令行参考](docs/cli.zh-CN.md)。

## 数据与隐私

- **配置**保存在用户配置目录下的 `Pail` 文件夹：Windows 是 `%AppData%\Pail`，macOS 是 `~/Library/Application Support/Pail`，Linux 是 `~/.config/Pail`；也可以用 `--config-dir` 指定。里面有设置、账户、同步任务、上传规则和已保存机密的名称。Pail 旧名称 `openS3` 留下的文件夹会在首次启动时迁移一次。
- **机密**（私有访问密钥、会话令牌和保存的密码）存进系统钥匙串，服务名为 `Pail`：Windows 凭据管理器、macOS 钥匙串或 Linux 的 Secret Service。没有钥匙串时，可以在**设置 › 安全**里改用加密保险库 `vault.json`。访问密钥 ID 不是机密，和账户保存在一起。
- **日志**默认关闭。开启后，Pail 在配置目录写入 `logs/pail.log`，并去掉 URL 查询参数、Authorization 请求头、签名和已知的机密值。手动输入的密码只能按标签识别，分享日志前请先检查一遍。
- **网络**：Pail 只连接你添加的账户所对应的端点，没有遥测，也不检查更新。

## 已测试的平台与服务

- 桌面应用已在 Windows（x64）、Linux（x64）和 macOS（Apple 芯片与 Intel）上手动运行过。Windows 和 Linux 的 Arm 版本还没有手动试过。
- 已测试的服务：Amazon S3、Cloudflare R2、Wasabi、MinIO 和 VersityGW。
- 还没有在真实 AWS 账户上测试：AssumeRole 凭据和 CloudFront。

已知限制见 [更新日志](CHANGELOG.zh-CN.md)。

## 从源码构建

需要 Go 1.27.3，不需要 C 编译器。

```sh
go build -o build/ ./cmd/pail-desktop ./cmd/pail
```

测试、发布打包和项目结构见 [CONTRIBUTING.md](CONTRIBUTING.md)（英文）。

## 许可证

Pail 以 [MIT 许可证](LICENSE) 发布，随附 [Lucide](https://lucide.dev) 图标（ISC 许可证）和 Inter 字体（SIL Open Font License 1.1）。

Amazon S3 是 Amazon.com, Inc. 的商标。Pail 与 AWS 没有关联，也未获其认可或赞助。
