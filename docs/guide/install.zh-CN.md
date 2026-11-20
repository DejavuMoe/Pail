# 安装

Pail 为每个系统提供一个压缩包，里面有桌面应用和命令行工具 pail，没有安装程序。本页介绍如何选择和校验压缩包、各系统上的首次启动，以及从源码构建。

## 选择压缩包

从 [最新版本](https://github.com/DejavuMoe/Pail/releases/latest) 下载对应系统的压缩包。镜像 [pan.zsh.moe/Pail](https://pan.zsh.moe/Pail/) 上有相同的文件。

| 系统 | 压缩包 |
| --- | --- |
| Windows x64 | `Pail-v<version>-windows-amd64.zip` |
| Windows Arm | `Pail-v<version>-windows-arm64.zip` |
| macOS，Apple 芯片 | `Pail-v<version>-darwin-arm64.zip` |
| macOS，Intel | `Pail-v<version>-darwin-amd64.zip` |
| Linux x64 | `Pail-v<version>-linux-amd64.tar.gz` |
| Linux Arm | `Pail-v<version>-linux-arm64.tar.gz` |

需要 macOS 11 或更高版本。不提供 32 位版本。

### 压缩包里有什么

每个压缩包解压后是一个同名文件夹。以 0.6.4 版的 Linux x64 为例：

```text
Pail-v0.6.4-linux-amd64/
  pail-desktop-v0.6.4-linux-amd64    桌面应用
  pail-v0.6.4-linux-amd64            命令行工具
  pail.desktop                       启动器条目
  pail.png                           图标
  LICENSE
```

Windows 的压缩包里是 `pail-desktop-v0.6.4-windows-amd64.exe`、`pail-v0.6.4-windows-amd64.exe` 和许可证。macOS 的压缩包里是 `Pail.app`、命令行工具 `pail-v0.6.4-darwin-arm64`（或 `-darwin-amd64`）和许可证。

## 校验下载

每个版本都有一个 `SHA256SUMS` 文件，每个压缩包占一行。计算压缩包的 SHA-256，与对应的那一行比较：

```sh
sha256sum Pail-v0.6.4-linux-amd64.tar.gz           # Linux
shasum -a 256 Pail-v0.6.4-darwin-arm64.zip         # macOS
Get-FileHash Pail-v0.6.4-windows-amd64.zip         # Windows PowerShell
```

在 Linux 上，把 `SHA256SUMS` 和压缩包放在同一个文件夹里，`sha256sum` 可以直接比较，一致时输出 `OK`：

```sh
sha256sum --check --ignore-missing SHA256SUMS
```

`Get-FileHash` 输出的是大写字母，比较时不区分大小写。

## 可执行文件的名字

可执行文件的名字带版本号和平台，例如 `pail-v0.6.4-windows-amd64.exe`。本指南把它们写作 `pail-desktop` 和 `pail`。可以给文件改名、创建别名，或者直接使用完整文件名。

```sh
mv pail-desktop-v0.6.4-linux-amd64 pail-desktop
mv pail-v0.6.4-linux-amd64 pail
```

```powershell
Rename-Item pail-desktop-v0.6.4-windows-amd64.exe pail-desktop.exe
Rename-Item pail-v0.6.4-windows-amd64.exe pail.exe
```

想在任意终端里使用 `pail`，就把它放进 `PATH` 中的某个文件夹。

## 首次启动

### macOS

应用没有签名和公证，macOS 会拒绝打开下载的副本。解压后把 `Pail.app` 移到“应用程序”，然后清除一次隔离标记：

```sh
xattr -dr com.apple.quarantine /Applications/Pail.app
```

### Windows

可执行文件没有签名。如果 SmartScreen 提示“Windows 已保护你的电脑”，点击**更多信息**，再点击**仍要运行**。

### Linux

解压后启动 `pail-desktop-v0.6.4-linux-amd64`。托盘图标需要 `libayatana-appindicator3`；没有它时，关闭窗口就会退出 Pail。

`pail.desktop` 按名字启动 `pail-desktop-v0.6.4-linux-amd64`，不带文件夹路径，图标名是 `pail`。要让 Pail 出现在应用列表里，把可执行文件复制到 `PATH` 中的文件夹，再把条目和图标（256 × 256 像素）复制到主目录下的常用位置：

```sh
mkdir -p ~/.local/bin ~/.local/share/applications ~/.local/share/icons/hicolor/256x256/apps
cp pail-desktop-v0.6.4-linux-amd64 pail-v0.6.4-linux-amd64 ~/.local/bin/
cp pail.desktop ~/.local/share/applications/
cp pail.png ~/.local/share/icons/hicolor/256x256/apps/
```

如果给桌面程序改了名，把 `pail.desktop` 里的 `Exec=` 一行改成新名字。

## 桌面应用的参数

`pail-desktop` 支持这些参数：

| 参数 | 作用 |
| --- | --- |
| `--config-dir` | 使用其他配置目录。 |
| `--theme` | `system`、`light` 或 `dark`。默认使用设置里保存的主题。 |
| `--size` | 初始窗口大小，例如 `1280x800`。默认 `1440x900`。 |
| `--tool` | 启动时打开工具：`sync`、`cloudfront`、`rules`、`mime`、`multipart` 或 `settings`。 |
| `--version` | 输出版本号后退出。 |

例如，用一个单独的空配置试用 Pail：

```sh
pail-desktop --config-dir ~/pail-test --size 1280x800 --tool settings
```

`pail` 也接受 `--config-dir`，见 [命令行参考](../cli.zh-CN.md#global-flags)。

## 更新到新版本

Pail 不检查更新。先读 [更新日志](../../CHANGELOG.zh-CN.md)，再下载新的压缩包，替换旧的可执行文件。账户、设置和同步任务保存在配置文件夹里，不在程序旁边，所以会保留，见 [数据与安全](privacy.zh-CN.md)。上次退出时未完成的传输会以暂停状态恢复。

## 从源码构建

需要 Go 1.27.4，不需要 C 编译器。

```sh
go build -o build/ ./cmd/pail-desktop ./cmd/pail
```

测试、发布打包和项目结构见 [CONTRIBUTING.md](https://github.com/DejavuMoe/Pail/blob/master/CONTRIBUTING.md)（英文）。
