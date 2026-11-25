# 安装

Pail 为每个系统提供一个压缩包，里面有桌面应用和命令行工具 pail。本页介绍如何选择和校验压缩包、各系统上的首次启动、更新，以及从源码构建。

## 选择压缩包

从 [最新版本](https://github.com/DejavuMoe/Pail/releases/latest) 下载对应系统的压缩包。最新版本的文件在 [pail.download](https://pail.download/download/) 上也有。

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

每个压缩包解压后是一个名为 `Pail` 的文件夹。以 Linux 为例：

```text
Pail/
  pail-desktop    桌面应用
  pail            命令行工具
  install.sh      为当前用户安装 Pail
  pail.desktop    启动器条目
  pail.png        图标
  .mygo-files     文件清单，更新时使用
  LICENSE
```

Windows 的压缩包里是 `pail-desktop.exe`、`pail.exe`、`.mygo-files` 和许可证。macOS 的压缩包里是 `Pail.app` 和许可证，命令行工具是 `Pail.app/Contents/MacOS/pail`。

## 校验下载

每个版本都有一个 `SHA256SUMS` 文件，每个压缩包占一行。计算压缩包的 SHA-256，与对应的那一行比较：

```sh
sha256sum Pail-v0.7.2-linux-amd64.tar.gz           # Linux
shasum -a 256 Pail-v0.7.2-darwin-arm64.zip         # macOS
Get-FileHash Pail-v0.7.2-windows-amd64.zip         # Windows PowerShell
```

在 Linux 上，把 `SHA256SUMS` 和压缩包放在同一个文件夹里，`sha256sum` 可以直接比较，一致时输出 `OK`：

```sh
sha256sum --check --ignore-missing SHA256SUMS
```

`Get-FileHash` 输出的是大写字母，比较时不区分大小写。

## 首次启动

### macOS

应用没有签名和公证，macOS 会拒绝打开下载的副本。解压后把 `Pail.app` 移到“应用程序”，然后清除一次隔离标记：

```sh
xattr -dr com.apple.quarantine /Applications/Pail.app
```

### Windows

可执行文件没有签名。如果 SmartScreen 提示“Windows 已保护你的电脑”，点击 **更多信息**，再点击 **仍要运行**。

把 `pail-desktop.exe` 和 `pail.exe` 放在同一个你有写入权限的文件夹里，例如用户文件夹下的某个文件夹，Pail 才能自动更新。

### Linux

解压后在 `Pail` 文件夹里运行 `install.sh`：

```sh
./install.sh
```

它把 Pail 复制到 `~/.local/share/pail`，在 `~/.local/bin` 里创建 `pail-desktop` 和 `pail` 的链接，并把 Pail 加入应用列表。用更新的压缩包再运行一次，会替换已安装的副本。也可以不安装，直接在解压的文件夹里启动 `./pail-desktop`。

托盘图标需要 `libayatana-appindicator3`；没有它时，关闭窗口就会退出 Pail。

## 在终端里使用 pail

在 Linux 上，`install.sh` 会把 `pail` 放进 `~/.local/bin`；如果这个文件夹不在 `PATH` 里，脚本会提示要添加什么。

在 macOS 上，链接应用里的命令行工具：

```sh
sudo mkdir -p /usr/local/bin
sudo ln -sf /Applications/Pail.app/Contents/MacOS/pail /usr/local/bin/pail
```

在 Windows 上，把 `pail.exe` 所在的文件夹加入 `PATH`，或者用完整路径运行。

链接和 `PATH` 指向已安装的副本，更新后仍然有效。

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

打开 **设置 › 更新**，点击 **立即检查**。有新版本时，**下载并安装** 会下载它，**重启** 后开始使用。有传输在进行时 Pail 不会重启。更新会同时替换桌面应用和命令行工具 `pail`；在终端里运行 `pail update` 效果相同。

**检查更新** 默认是 **手动**。选 **每天** 或 **每周** 时，Pail 在当天或当周第一次启动时检查，有新版本只提示，点击之前不会下载。

**下载来源** 可选 GitHub 或 pail.download，连不上其中一个时改用另一个。Pail 只安装用 Pail 发布密钥签名的文件。有适用于当前版本的增量更新时，只下载增量更新，不下载完整版本。

以下情况 Pail 不能自动更新：所在文件夹没有写入权限、以 AppImage 运行，或在 macOS 上不在 `Pail.app` 里运行。这时先读 [更新日志](../../CHANGELOG.zh-CN.md)，再下载新的压缩包，替换旧的文件。

账户、设置和同步任务保存在配置文件夹里，不在程序旁边，所以会保留，见 [数据与安全](privacy.zh-CN.md)。上次退出时未完成的传输会以暂停状态恢复。

## 从源码构建

需要 Go 1.27.4，不需要 C 编译器。

```sh
go build -o build/ ./cmd/pail-desktop ./cmd/pail
```

从源码构建的版本不会自动更新。测试、发布打包和项目结构见 [CONTRIBUTING.md](https://github.com/DejavuMoe/Pail/blob/master/CONTRIBUTING.md)（英文）。
