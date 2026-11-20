# Install

Pail comes as one archive per system that holds the desktop app and the pail command-line tool; there is no installer. This page covers choosing and checking the archive, the first launch on each system, and building from source.

## Choose an archive

Download the archive for your system from the [latest release](https://github.com/DejavuMoe/Pail/releases/latest). The same files are mirrored at [pan.zsh.moe/Pail](https://pan.zsh.moe/Pail/).

| System | Archive |
| --- | --- |
| Windows x64 | `Pail-v<version>-windows-amd64.zip` |
| Windows Arm | `Pail-v<version>-windows-arm64.zip` |
| macOS, Apple silicon | `Pail-v<version>-darwin-arm64.zip` |
| macOS, Intel | `Pail-v<version>-darwin-amd64.zip` |
| Linux x64 | `Pail-v<version>-linux-amd64.tar.gz` |
| Linux Arm | `Pail-v<version>-linux-arm64.tar.gz` |

macOS 11 or later is needed. There are no 32-bit builds.

### What the archive holds

Each archive unpacks into a folder of the same name. For version 0.6.4 on Linux x64:

```text
Pail-v0.6.4-linux-amd64/
  pail-desktop-v0.6.4-linux-amd64    desktop app
  pail-v0.6.4-linux-amd64            command-line tool
  pail.desktop                       launcher entry
  pail.png                           icon
  LICENSE
```

The Windows archives hold `pail-desktop-v0.6.4-windows-amd64.exe`, `pail-v0.6.4-windows-amd64.exe` and the license. The macOS archives hold `Pail.app`, the command-line tool `pail-v0.6.4-darwin-arm64` (or `-darwin-amd64`) and the license.

## Check the download

Each release has a `SHA256SUMS` file with one line per archive. Compute the archive's SHA-256 hash and compare it with its line:

```sh
sha256sum Pail-v0.6.4-linux-amd64.tar.gz           # Linux
shasum -a 256 Pail-v0.6.4-darwin-arm64.zip         # macOS
Get-FileHash Pail-v0.6.4-windows-amd64.zip         # Windows PowerShell
```

On Linux, with `SHA256SUMS` in the same folder as the archive, `sha256sum` can do the comparison and print `OK`:

```sh
sha256sum --check --ignore-missing SHA256SUMS
```

`Get-FileHash` prints the hash in capital letters; the comparison does not depend on case.

## Executable names

The executables are named with the version and the platform, for example `pail-v0.6.4-windows-amd64.exe`. This guide calls them `pail-desktop` and `pail`. Rename them, create aliases, or run the full file names.

```sh
mv pail-desktop-v0.6.4-linux-amd64 pail-desktop
mv pail-v0.6.4-linux-amd64 pail
```

```powershell
Rename-Item pail-desktop-v0.6.4-windows-amd64.exe pail-desktop.exe
Rename-Item pail-v0.6.4-windows-amd64.exe pail.exe
```

To use `pail` from any terminal, put it in a folder that is on your `PATH`.

## First launch

### macOS

The app is not signed or notarized, so macOS refuses to open a downloaded copy. Unzip the archive, move `Pail.app` to Applications, and clear the quarantine flag once:

```sh
xattr -dr com.apple.quarantine /Applications/Pail.app
```

### Windows

The executables are not signed. If SmartScreen says "Windows protected your PC", choose **More info**, then **Run anyway**.

### Linux

Unpack the archive and start `pail-desktop-v0.6.4-linux-amd64`. The tray icon needs `libayatana-appindicator3`; without it, closing the window quits Pail.

The `pail.desktop` entry starts `pail-desktop-v0.6.4-linux-amd64` by name, without a folder, and uses the icon name `pail`. To list Pail among your applications, copy the executables to a folder on your `PATH`, and the entry and the icon (256 × 256 pixels) to the usual places in your home folder:

```sh
mkdir -p ~/.local/bin ~/.local/share/applications ~/.local/share/icons/hicolor/256x256/apps
cp pail-desktop-v0.6.4-linux-amd64 pail-v0.6.4-linux-amd64 ~/.local/bin/
cp pail.desktop ~/.local/share/applications/
cp pail.png ~/.local/share/icons/hicolor/256x256/apps/
```

If you rename the desktop executable, change the `Exec=` line of `pail.desktop` to the new name.

## Desktop app flags

`pail-desktop` accepts these flags:

| Flag | Effect |
| --- | --- |
| `--config-dir` | Use another configuration directory. |
| `--theme` | `system`, `light` or `dark`. Default: the theme saved in Settings. |
| `--size` | Initial window size, such as `1280x800`. Default: `1440x900`. |
| `--tool` | Open a tool at start: `sync`, `cloudfront`, `rules`, `mime`, `multipart` or `settings`. |
| `--version` | Print the version and exit. |

For example, to try Pail with a separate, empty configuration:

```sh
pail-desktop --config-dir ~/pail-test --size 1280x800 --tool settings
```

`pail` takes `--config-dir` as well; see the [command-line reference](../cli.md#global-flags).

## Update to a new version

Pail does not check for updates. Read the [changelog](../../CHANGELOG.md), download the new archive and replace the old executables. Accounts, settings and sync jobs live in the configuration folder, not next to the programs, so they stay; see [Data and security](privacy.md). Transfers that were unfinished when Pail quit come back paused.

## Build from source

You need Go 1.27.4. No C compiler is required.

```sh
go build -o build/ ./cmd/pail-desktop ./cmd/pail
```

Tests, release packaging and the project layout are described in [CONTRIBUTING.md](https://github.com/DejavuMoe/Pail/blob/master/CONTRIBUTING.md).
