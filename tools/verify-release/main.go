// Command verify-release checks the complete release before it is published.
package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"debug/buildinfo"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func main() {
	dir := flag.String("dir", "", "release directory")
	version := flag.String("version", "", "semantic version without v")
	targets := flag.String("targets", "windows/amd64,windows/arm64,linux/amd64,linux/arm64,darwin/amd64,darwin/arm64", "comma-separated targets")
	flag.Parse()
	if err := verify(*dir, *version, strings.Split(*targets, ",")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("Verified release archives, checksums, binary targets and executable permissions")
}

func verify(dir, version string, targets []string) error {
	if dir == "" || version == "" {
		return fmt.Errorf("-dir and -version are required")
	}
	sums, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	if err != nil {
		return err
	}
	sumLines := strings.Split(strings.TrimSpace(strings.ReplaceAll(string(sums), "\r\n", "\n")), "\n")
	expected := map[string]bool{"SHA256SUMS": true}
	for _, target := range targets {
		osName, arch, ok := strings.Cut(target, "/")
		if !ok || !slices.Contains([]string{"windows", "linux", "darwin"}, osName) || !slices.Contains([]string{"amd64", "arm64"}, arch) {
			return fmt.Errorf("unsupported target %q", target)
		}
		base := "Pail-v" + version + "-" + osName + "-" + arch
		ext := ".zip"
		if osName == "linux" {
			ext = ".tar.gz"
		}
		name := base + ext
		if expected[name] {
			return fmt.Errorf("duplicate target %q", target)
		}
		expected[name] = true
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		line := fmt.Sprintf("%x  %s", sha256.Sum256(data), name)
		if !slices.Contains(sumLines, line) {
			return fmt.Errorf("%s: checksum missing or incorrect", name)
		}
		if err := verifyArchive(data, base, version, osName, arch); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	if len(sumLines) != len(targets) {
		return fmt.Errorf("unexpected checksum entries")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !expected[e.Name()] || !e.Type().IsRegular() {
			return fmt.Errorf("unexpected release output %s", e.Name())
		}
	}
	return nil
}

func verifyArchive(data []byte, base, version, osName, arch string) error {
	suffix := "-v" + version + "-" + osName + "-" + arch
	exe := ""
	if osName == "windows" {
		exe = ".exe"
	}
	cli := "pail" + suffix + exe
	desktop := "pail-desktop" + suffix + exe
	desktopPath := desktop
	required := map[string]bool{"LICENSE": false, "README.md": false, "README.zh-CN.md": false, cli: false}
	if osName == "darwin" {
		desktopPath = "Pail.app/Contents/MacOS/" + desktop
		required["Pail.app/Contents/Info.plist"] = false
		required["Pail.app/Contents/Resources/pail.icns"] = false
	}
	if osName == "linux" {
		required["pail.desktop"], required["pail.png"] = false, false
	}
	required[desktopPath] = false
	seen := map[string]bool{}
	check := func(name string, mode fs.FileMode, r io.Reader) error {
		if !strings.HasPrefix(name, base+"/") || strings.Contains(name, "\\") || strings.Contains(name, "/../") || seen[name] {
			return fmt.Errorf("invalid or duplicate path %q", name)
		}
		seen[name] = true
		if mode.IsDir() {
			return nil
		}
		if !mode.IsRegular() {
			return fmt.Errorf("non-regular file %q", name)
		}
		rel := strings.TrimPrefix(name, base+"/")
		if _, ok := required[rel]; !ok {
			return fmt.Errorf("unexpected file %q", rel)
		}
		body, err := io.ReadAll(io.LimitReader(r, 256<<20))
		if err != nil || len(body) == 0 || len(body) == 256<<20 {
			return fmt.Errorf("invalid file contents %q: %v", rel, err)
		}
		required[rel] = true
		if rel == cli || rel == desktopPath {
			if mode.Perm() != 0o755 {
				return fmt.Errorf("%s: executable mode is %o", rel, mode.Perm())
			}
			info, err := buildinfo.Read(bytes.NewReader(body))
			if err != nil {
				return err
			}
			settings := map[string]string{}
			for _, s := range info.Settings {
				settings[s.Key] = s.Value
			}
			pkg := "github.com/DejavuMoe/Pail/cmd/pail"
			if rel == desktopPath {
				pkg += "-desktop"
			}
			if settings["GOOS"] != osName || settings["GOARCH"] != arch || settings["CGO_ENABLED"] != "0" || info.Path != pkg {
				return fmt.Errorf("%s: incorrect platform, cgo or executable package", rel)
			}
		}
		if rel == "pail.desktop" && !strings.Contains(string(body), "\nExec="+desktop+"\n") {
			return fmt.Errorf("desktop entry names the wrong executable")
		}
		if rel == "Pail.app/Contents/Info.plist" && !strings.Contains(string(body), "<key>CFBundleExecutable</key><string>"+desktop+"</string>") {
			return fmt.Errorf("app bundle names the wrong executable")
		}
		return nil
	}
	if osName == "linux" {
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return err
		}
		defer gz.Close()
		tr := tar.NewReader(gz)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			if err := check(h.Name, h.FileInfo().Mode(), tr); err != nil {
				return err
			}
		}
	} else {
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return err
		}
		for _, f := range zr.File {
			r, err := f.Open()
			if err != nil {
				return err
			}
			err = check(f.Name, f.Mode(), r)
			r.Close()
			if err != nil {
				return err
			}
		}
	}
	for name, present := range required {
		if !present {
			return fmt.Errorf("missing %s", name)
		}
	}
	return nil
}
