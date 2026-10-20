// Command release builds and verifies Pail archives using only the Go toolchain.
//
//	go run ./tools/release -version 0.2.0
package main

import (
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
)

const allTargets = "windows/amd64,windows/arm64,linux/amd64,linux/arm64,darwin/amd64,darwin/arm64"

var versionRE = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-(0|[1-9]\d*|\d*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9]\d*|\d*[A-Za-z-][0-9A-Za-z-]*))*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)

func main() {
	version := flag.String("version", "", "required SemVer without v")
	targetList := flag.String("targets", allTargets, "comma-separated OS/architecture targets")
	outDir := flag.String("out-dir", "", "output parent directory (default: <repo>/build/release)")
	cliOnly := flag.Bool("cli-only", false, "build CLI-only local archives; not a complete release")
	flag.Parse()
	if err := release(*version, *targetList, *outDir, *cliOnly); err != nil {
		fmt.Fprintln(os.Stderr, "release:", err)
		os.Exit(1)
	}
}

func targets(list string) ([]string, error) {
	var result []string
	for _, s := range strings.Split(list, ",") {
		s = strings.TrimSpace(s)
		if !slices.Contains(strings.Split(allTargets, ","), s) || slices.Contains(result, s) {
			return nil, fmt.Errorf("unsupported or duplicate target %q", s)
		}
		result = append(result, s)
	}
	return result, nil
}

func findRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		b, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && strings.HasPrefix(string(b), "module github.com/DejavuMoe/Pail\n") {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("run inside the Pail repository")
		}
		dir = parent
	}
}

func release(version, targetList, outDir string, cliOnly bool) error {
	if !versionRE.MatchString(version) {
		return fmt.Errorf("invalid -version %q: expected SemVer without v", version)
	}
	ts, err := targets(targetList)
	if err != nil {
		return err
	}
	root, err := findRoot()
	if err != nil {
		return err
	}
	if outDir == "" {
		outDir = filepath.Join(root, "build", "release")
	}
	outDir, err = filepath.Abs(outDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	// Build in an owned temporary directory; failures leave the previous release intact.
	stage, err := os.MkdirTemp(outDir, ".pail-build-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	out := filepath.Join(stage, "archives")
	if err := os.Mkdir(out, 0o755); err != nil {
		return err
	}
	packer := filepath.Join(stage, "pack")
	if runtime.GOOS == "windows" {
		packer += ".exe"
	}
	if err := goRun(root, runtime.GOOS, runtime.GOARCH, "build", "-o", packer, "./tools/pack"); err != nil {
		return err
	}
	var sums strings.Builder
	for _, target := range ts {
		osName, arch, _ := strings.Cut(target, "/")
		base := "Pail-v" + version + "-" + osName + "-" + arch
		dir := filepath.Join(stage, base)
		if err := os.Mkdir(dir, 0o755); err != nil {
			return err
		}
		var executables []string
		for _, app := range []string{"pail-desktop", "pail"} {
			if cliOnly && app == "pail-desktop" {
				continue
			}
			name := executable(app, version, osName, arch)
			executables = append(executables, name)
			ld := "-s -w -X main.version=" + version
			if app == "pail-desktop" && osName == "windows" {
				ld += " -H windowsgui"
			}
			if err := goRun(root, osName, arch, "build", "-trimpath", "-ldflags", ld, "-o", filepath.Join(dir, name), "./cmd/"+app); err != nil {
				return fmt.Errorf("%s %s: %w", target, app, err)
			}
		}
		if err := copyFile(filepath.Join(root, "LICENSE"), filepath.Join(dir, "LICENSE")); err != nil {
			return err
		}
		if !cliOnly {
			if err := launchAssets(root, dir, version, osName, arch); err != nil {
				return err
			}
		}
		archive := base + ".zip"
		if osName == "linux" {
			archive = base + ".tar.gz"
		}
		file := filepath.Join(out, archive)
		cmd := exec.Command(packer, "-C", dir, "-o", file, "-prefix", base, "-exec", strings.Join(executables, ","))
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("pack %s: %w", target, err)
		}
		f, err := os.Open(file)
		if err != nil {
			return err
		}
		hash := sha256.New()
		_, err = io.Copy(hash, f)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		fmt.Fprintf(&sums, "%x  %s\n", hash.Sum(nil), archive)
		fmt.Println("built", archive)
	}
	if err := os.WriteFile(filepath.Join(out, "SHA256SUMS"), []byte(sums.String()), 0o644); err != nil {
		return err
	}
	if !cliOnly {
		if err := goRun(root, runtime.GOOS, runtime.GOARCH, "run", "./tools/verify-release", "-dir", out, "-version", version, "-targets", strings.Join(ts, ",")); err != nil {
			return err
		}
	} else {
		fmt.Println("CLI-only local build: not verified as a complete release")
	}
	if err := publish(outDir, version, out); err != nil {
		return err
	}
	fmt.Println("output directory:", filepath.Join(outDir, version))
	return nil
}

func goRun(root, osName, arch string, args ...string) error {
	cmd := exec.Command("go", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOOS="+osName, "GOARCH="+arch, "CGO_ENABLED=0")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func executable(app, version, osName, arch string) string {
	name := app + "-v" + version + "-" + osName + "-" + arch
	if osName == "windows" {
		name += ".exe"
	}
	return name
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}

// publish only replaces a directory containing this version's known release files.
func publish(parent, version, source string) error {
	if !versionRE.MatchString(version) {
		return fmt.Errorf("unsafe release directory %q", version)
	}
	r, err := os.OpenRoot(parent)
	if err != nil {
		return err
	}
	defer r.Close()
	rel, err := filepath.Rel(parent, source)
	if err != nil {
		return err
	}
	if !filepath.IsLocal(rel) || rel == version || strings.HasPrefix(rel, version+string(filepath.Separator)) {
		return errors.New("release source must be a separate staging directory inside output parent")
	}
	srcInfo, err := r.Lstat(rel)
	if err != nil {
		return err
	}
	if !srcInfo.IsDir() || srcInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("release source must be a directory, not a link")
	}
	info, err := r.Lstat(version)
	if err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("refusing to replace linked or non-directory release output")
		}
		entries, err := os.ReadDir(filepath.Join(parent, version))
		if err != nil {
			return err
		}
		allowed := map[string]bool{"SHA256SUMS": true}
		for _, target := range strings.Split(allTargets, ",") {
			ext := ".zip"
			if strings.HasPrefix(target, "linux/") {
				ext = ".tar.gz"
			}
			allowed["Pail-v"+version+"-"+strings.ReplaceAll(target, "/", "-")+ext] = true
		}
		for _, e := range entries {
			if !e.Type().IsRegular() || !allowed[e.Name()] {
				return fmt.Errorf("refusing to replace unexpected output %s", e.Name())
			}
		}
		if err := r.RemoveAll(version); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return r.Rename(rel, version)
}
