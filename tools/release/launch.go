package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func launchAssets(root, dir, version, osName, arch string) error {
	desktop := executable("pail-desktop", version, osName, arch)
	gen := filepath.Join(root, "assets", "brand", "generated")
	switch osName {
	case "linux":
		entry := fmt.Sprintf("[Desktop Entry]\nType=Application\nName=Pail\nComment=S3 storage manager\nExec=%s\nIcon=pail\nCategories=Network;Utility;\nTerminal=false\n", desktop)
		if err := os.WriteFile(filepath.Join(dir, "pail.desktop"), []byte(entry), 0o644); err != nil {
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
		if err := os.Rename(filepath.Join(dir, desktop), filepath.Join(app, "MacOS", desktop)); err != nil {
			return err
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
</dict>
</plist>
`, desktop, plistVersion, plistVersion)
		return os.WriteFile(filepath.Join(app, "Info.plist"), []byte(plist), 0o644)
	}
	return nil
}
