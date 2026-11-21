// Package selfupdate updates an installed Pail (the desktop app and the
// command line together) from the signed update files of a release.
//
// Every release publishes, per target, an update archive, delta updates from
// earlier versions and a manifest, update-<os>-<arch>.json, in the format of
// package updatefmt (that of the mygo updater). Archives and deltas are signed
// with the Ed25519 key whose public half is PublicKey; nothing unsigned is
// installed. The manifest itself is not signed: a server that serves an older
// signed archive under a newer version number can hold an installation back,
// but cannot install anything that was not released.
//
// The files are on GitHub Releases and, for the latest release only, on the
// mirror of pail.download. Check reads the manifest from the chosen source and
// falls back to the other one; Install downloads the delta for the running
// version when there is one, else the archive, and replaces the installation:
// the files of the app directory on Windows and Linux, the app bundle on macOS
// once the process has exited.
package selfupdate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/DejavuMoe/Pail/internal/updatefmt"
)

// PublicKey verifies every update archive and delta (base64, Ed25519). Its
// private half signs releases in GitHub Actions. Changing it strands every
// installation that has the old one: they no longer accept updates.
const PublicKey = "p0L7fn/jFtLZ6umy6Zr/0ACAaf7xgr87PZU7Ki4plQs="

// Source is where updates come from.
type Source string

// Sources. SourceGitHub is the default.
const (
	SourceGitHub Source = "github"
	SourceMirror Source = "mirror"
)

// Valid reports whether s is a known source; "" is not.
func (s Source) Valid() bool { return s == SourceGitHub || s == SourceMirror }

// other is the source Check falls back to.
func (s Source) other() Source {
	if s == SourceMirror {
		return SourceGitHub
	}
	return SourceMirror
}

// Where the update files are. GitHubLatest serves the files of the latest
// published release that is neither a draft nor a prerelease; MirrorBase holds
// the latest stable release only.
const (
	GitHubRepo   = "DejavuMoe/Pail"
	GitHubLatest = "https://github.com/" + GitHubRepo + "/releases/latest/download/"
	MirrorBase   = "https://pail.download/update/"
)

// GitHubRelease is the directory of the files of the release of version.
func GitHubRelease(version string) string {
	return "https://github.com/" + GitHubRepo + "/releases/download/v" + version + "/"
}

// Target is the target of the running program, such as windows-amd64.
func Target() string { return runtime.GOOS + "-" + runtime.GOARCH }

// Names of the files of a release for target.
func ManifestName(target string) string { return updatefmt.ManifestName(target) }

// ArchiveName is the name of the update archive of version for target.
func ArchiveName(version, target string) string {
	return "pail-update-" + version + "-" + target + ".tar.gz"
}

// DeltaName is the name of the delta update from version from to version to.
func DeltaName(from, to, target string) string {
	return "pail-update-" + from + "-to-" + to + "-" + target + ".delta"
}

// Policy is when the desktop app checks for updates by itself.
type Policy string

// Policies. PolicyManual, the default, never checks by itself.
const (
	PolicyManual Policy = "manual"
	PolicyDaily  Policy = "daily"
	PolicyWeekly Policy = "weekly"
)

// Valid reports whether p is a known policy; "" is not.
func (p Policy) Valid() bool { return p == PolicyManual || p == PolicyDaily || p == PolicyWeekly }

// Due reports whether the app, started at now, checks for updates under p
// when it last checked at last (zero: never): on the first start of a day
// (daily) or of a week, Monday to Sunday (weekly), in now's time zone.
func Due(p Policy, last, now time.Time) bool {
	if last.IsZero() {
		return p == PolicyDaily || p == PolicyWeekly
	}
	last = last.In(now.Location())
	switch p {
	case PolicyDaily:
		ly, lm, ld := last.Date()
		ny, nm, nd := now.Date()
		return ly != ny || lm != nm || ld != nd || last.After(now)
	case PolicyWeekly:
		ly, lw := last.ISOWeek()
		ny, nw := now.ISOWeek()
		return ly != ny || lw != nw || last.After(now)
	}
	return false
}

// Reasons Supported gives for an installation that cannot update itself.
var (
	ErrDevBuild      = errors.New("this build is not a release, so it cannot update itself")
	ErrNotBundle     = errors.New("only Pail.app can update itself on macOS")
	ErrAppImage      = errors.New("an AppImage cannot update itself")
	ErrNotWritable   = errors.New("the folder Pail is installed in is not writable")
	errEmptyManifest = errors.New("incomplete update manifest")
)

// startExe is the executable the program started from: on Linux,
// os.Executable reports the old file once an update replaced it.
var startExe, _ = os.Executable()

// Supported reports why the installation of the running program, of version
// current, cannot update itself, or nil when it can.
func Supported(current string) error {
	if current == "" || current == "dev" {
		return ErrDevBuild
	}
	target, err := installTarget()
	if err != nil {
		return err
	}
	probe, err := os.MkdirTemp(filepath.Dir(target), ".pail-update-probe-")
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNotWritable, err)
	}
	os.Remove(probe)
	return nil
}

// installTarget returns what an update replaces: the app bundle on macOS, the
// directory of the executable elsewhere. Links to the executable, as
// ~/.local/bin/pail, lead to the installation.
func installTarget() (string, error) {
	exe := startExe
	if exe == "" {
		return "", errors.New("cannot find the running executable")
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	if runtime.GOOS == "darwin" {
		bundle := filepath.Dir(filepath.Dir(filepath.Dir(exe)))
		if !strings.HasSuffix(bundle, ".app") || filepath.Base(filepath.Dir(exe)) != "MacOS" {
			return "", ErrNotBundle
		}
		return bundle, nil
	}
	if os.Getenv("APPIMAGE") != "" {
		return "", ErrAppImage
	}
	return filepath.Dir(exe), nil
}

// InstallDir is where the running program is installed: the app bundle on
// macOS, the folder of the executables elsewhere.
func InstallDir() (string, error) { return installTarget() }
