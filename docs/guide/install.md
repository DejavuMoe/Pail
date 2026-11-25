# Install

Pail comes as one archive per system that holds the desktop app and the pail command-line tool. This page covers choosing and checking the archive, the first launch on each system, updates, and building from source.

## Choose an archive

Download the archive for your system from the [latest release](https://github.com/DejavuMoe/Pail/releases/latest). The files of the latest release are also on [pail.download](https://pail.download/download/).

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

Each archive unpacks into a folder named `Pail`. On Linux:

```text
Pail/
  pail-desktop    desktop app
  pail            command-line tool
  install.sh      installs Pail for your user
  pail.desktop    launcher entry
  pail.png        icon
  .mygo-files     list of the files, used by updates
  LICENSE
```

The Windows archives hold `pail-desktop.exe`, `pail.exe`, `.mygo-files` and the license. The macOS archives hold `Pail.app` and the license; the command-line tool is `Pail.app/Contents/MacOS/pail`.

## Check the download

Each release has a `SHA256SUMS` file with one line per archive. Compute the archive's SHA-256 hash and compare it with its line:

```sh
sha256sum Pail-v0.7.3-linux-amd64.tar.gz           # Linux
shasum -a 256 Pail-v0.7.3-darwin-arm64.zip         # macOS
Get-FileHash Pail-v0.7.3-windows-amd64.zip         # Windows PowerShell
```

On Linux, with `SHA256SUMS` in the same folder as the archive, `sha256sum` can do the comparison and print `OK`:

```sh
sha256sum --check --ignore-missing SHA256SUMS
```

`Get-FileHash` prints the hash in capital letters; the comparison does not depend on case.

## First launch

### macOS

The app is not signed or notarized, so macOS refuses to open a downloaded copy. Unzip the archive, move `Pail.app` to Applications, and clear the quarantine flag once:

```sh
xattr -dr com.apple.quarantine /Applications/Pail.app
```

### Windows

The executables are not signed. If SmartScreen says "Windows protected your PC", choose **More info**, then **Run anyway**.

Keep `pail-desktop.exe` and `pail.exe` together in a folder you can write to, such as one under your user folder, so that Pail can update itself.

### Linux

Unpack the archive and run `install.sh` in the `Pail` folder:

```sh
./install.sh
```

It copies Pail to `~/.local/share/pail`, links `pail-desktop` and `pail` into `~/.local/bin`, adds Pail to your applications, and makes Pail open `pail://` setup links. Running it again from a newer archive replaces the installed copy. You can also start `./pail-desktop` from the unpacked folder without installing.

The tray icon needs `libayatana-appindicator3`; without it, closing the window quits Pail.

## Use pail from a terminal

On Linux, `install.sh` puts `pail` in `~/.local/bin`; if that folder is not on your `PATH`, the script says what to add.

On macOS, link the tool inside the app:

```sh
sudo mkdir -p /usr/local/bin
sudo ln -sf /Applications/Pail.app/Contents/MacOS/pail /usr/local/bin/pail
```

On Windows, add the folder that holds `pail.exe` to your `PATH`, or run it by its full path.

The link and the `PATH` entry point to the installed copy, so they keep working after an update.

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

Open **Settings › Updates** and click **Check now**. When a new version is available, **Download and install** downloads it, and **Restart** starts it. Pail does not restart while a transfer is running. An update replaces both the desktop app and the `pail` command-line tool; `pail update` does the same from a terminal.

**Check for updates** is **Manually** by default. **Daily** or **Weekly** checks the first time Pail starts on a new day or week, and only tells you when a new version is available; nothing is downloaded until you click.

**Download from** chooses GitHub or pail.download; if one cannot be reached, Pail uses the other. Pail installs only files signed with the Pail release key. When there is a delta update for your version, Pail downloads it instead of the whole release.

Pail cannot update itself when it runs from a folder it cannot write to, as an AppImage, or on macOS outside `Pail.app`. Then read the [changelog](../../CHANGELOG.md), download the new archive and replace the old files.

Accounts, settings and sync jobs live in the configuration folder, not next to the programs, so they stay; see [Data and security](privacy.md). Transfers that were unfinished when Pail quit come back paused.

## Build from source

You need Go 1.27.4. No C compiler is required.

```sh
go build -o build/ ./cmd/pail-desktop ./cmd/pail
```

A build from source does not update itself. Tests, release packaging and the project layout are described in [CONTRIBUTING.md](https://github.com/DejavuMoe/Pail/blob/master/CONTRIBUTING.md).
