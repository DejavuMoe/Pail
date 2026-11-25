package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// installScript installs a Linux archive for the current user: the files the
// archive lists in .mygo-files into ${XDG_DATA_HOME:-~/.local/share}/pail,
// where updates replace them, links in ~/.local/bin and an entry in the
// application menu. Run from the installed folder, it only registers again.
const installScript = `#!/bin/sh
# Installs Pail for the current user:
#   the programs  ${XDG_DATA_HOME:-~/.local/share}/pail
#   the commands  ~/.local/bin/pail, ~/.local/bin/pail-desktop
#   the menu      ${XDG_DATA_HOME:-~/.local/share}/applications/pail.desktop
set -eu
src=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
data=${XDG_DATA_HOME:-$HOME/.local/share}
dest=$data/pail
bin=$HOME/.local/bin
mkdir -p "$dest"
dest=$(CDPATH= cd -- "$dest" && pwd -P)
if [ "$src" != "$dest" ]; then
	while IFS= read -r f; do
		[ -n "$f" ] || continue
		cp -p "$src/$f" "$dest/.$f.new"
		mv -f "$dest/.$f.new" "$dest/$f"
	done < "$src/.mygo-files"
	cp -p "$src/.mygo-files" "$dest/.mygo-files"
fi
mkdir -p "$bin" "$data/applications"
ln -sf "$dest/pail" "$bin/pail"
ln -sf "$dest/pail-desktop" "$bin/pail-desktop"
cat > "$data/applications/pail.desktop" <<EOF
[Desktop Entry]
Type=Application
Name=Pail
Comment=S3 storage manager
Exec="$dest/pail-desktop" %u
Icon=$dest/pail.png
Categories=Network;Utility;
MimeType=x-scheme-handler/pail;
Terminal=false
EOF
if command -v update-desktop-database >/dev/null 2>&1; then
	update-desktop-database "$data/applications" >/dev/null 2>&1 || true
fi
# pail:// setup links open Pail even before its first start (Pail registers
# them again each time it starts).
if command -v xdg-mime >/dev/null 2>&1; then
	xdg-mime default pail.desktop x-scheme-handler/pail >/dev/null 2>&1 || true
fi
echo "Installed Pail in $dest"
case ":$PATH:" in
*":$bin:"*) ;;
*) echo "Add $bin to PATH to run pail from a terminal." ;;
esac
`

func launchAssets(root, dir, version, osName, arch string) error {
	desktop := executable("pail-desktop", osName)
	gen := filepath.Join(root, "assets", "brand", "generated")
	switch osName {
	case "linux":
		entry := fmt.Sprintf("[Desktop Entry]\nType=Application\nName=Pail\nComment=S3 storage manager\nExec=%s %%u\nIcon=pail\nCategories=Network;Utility;\nMimeType=x-scheme-handler/pail;\nTerminal=false\n", desktop)
		if err := os.WriteFile(filepath.Join(dir, "pail.desktop"), []byte(entry), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "install.sh"), []byte(installScript), 0o755); err != nil {
			return err
		}
		return copyFile(filepath.Join(gen, "pail-256.png"), filepath.Join(dir, "pail.png"))
	case "darwin":
		app := filepath.Join(dir, "Pail.app", "Contents")
		for _, d := range []string{"MacOS", "Resources"} {
			if err := os.MkdirAll(filepath.Join(app, d), 0o755); err != nil {
				return err
			}
		}
		// The command line program is in the bundle too, so that updates of
		// the app update it.
		for _, exe := range []string{desktop, executable("pail", osName)} {
			if _, err := os.Stat(filepath.Join(dir, exe)); os.IsNotExist(err) {
				continue
			}
			if err := os.Rename(filepath.Join(dir, exe), filepath.Join(app, "MacOS", exe)); err != nil {
				return err
			}
		}
		if err := copyFile(filepath.Join(gen, "pail.icns"), filepath.Join(app, "Resources", "pail.icns")); err != nil {
			return err
		}
		plistVersion := strings.FieldsFunc(version, func(r rune) bool { return r == '-' || r == '+' })[0]
		plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>Pail</string>
  <key>CFBundleDisplayName</key><string>Pail</string>
  <key>CFBundleIdentifier</key><string>moe.dejavu.pail</string>
  <key>CFBundleExecutable</key><string>%s</string>
  <key>CFBundleIconFile</key><string>pail</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleVersion</key><string>%s</string>
  <key>CFBundleShortVersionString</key><string>%s</string>
  <key>LSMinimumSystemVersion</key><string>11.0</string>
  <key>NSHighResolutionCapable</key><true/>
  <key>CFBundleURLTypes</key>
  <array>
    <dict>
      <key>CFBundleURLName</key><string>moe.dejavu.pail.setup-link</string>
      <key>CFBundleURLSchemes</key><array><string>pail</string></array>
    </dict>
  </array>
</dict>
</plist>
`, desktop, plistVersion, plistVersion)
		return os.WriteFile(filepath.Join(app, "Info.plist"), []byte(plist), 0o644)
	}
	return nil
}
