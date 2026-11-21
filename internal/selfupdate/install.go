package selfupdate

// The installation of an update follows mygo v0.10.0's updater (updater.go,
// MIT License, Copyright (c) 2026 MyGo contributors; see
// internal/updatefmt/LICENSE.mygo), so that it replaces an installation the
// same way: the file names of the swap and the leftovers it cleans are its.

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/DejavuMoe/Pail/internal/updatefmt"
)

// Install downloads the update, checks that it is signed with PublicKey and
// replaces the installation with it; progress, when not nil, is called with
// the bytes downloaded so far. A delta that fails to make the new version is
// followed by the whole archive, and the progress starts over.
//
// On Windows and Linux the files are replaced at once; the running program
// keeps running the old version until it starts again. On macOS the new app
// bundle replaces the old one when this process has exited (RelaunchAfterExit).
func (up *Update) Install(ctx context.Context, progress func(downloaded, total int64)) error {
	target, err := installTarget()
	if err != nil {
		return err
	}
	if err := installUpdate(ctx, up.open, up.manifest, up.u.publicKey(), up.u.Current, target, progress); err != nil {
		return fmt.Errorf("installing the update: %w", err)
	}
	return nil
}

type getter func(ctx context.Context, url string) (io.ReadCloser, error)

func installUpdate(ctx context.Context, get getter, m updatefmt.Manifest, key, current, target string, progress func(downloaded, total int64)) error {
	pub, err := updatefmt.ParsePublicKey(key)
	if err != nil {
		return err
	}
	// Next to the app, so that it can be renamed into place.
	work, err := os.MkdirTemp(filepath.Dir(target), workPattern(target))
	if err != nil {
		return fmt.Errorf("cannot write next to the app: %w", err)
	}
	staged := false
	defer func() {
		if !staged {
			os.RemoveAll(work)
		}
	}()
	unpacked := filepath.Join(work, "app")
	swap := swapFiles
	if runtime.GOOS == "darwin" {
		swap = func(dir, target string) error {
			err := stageBundle(work, dir, target)
			staged = err == nil
			return err
		}
	}

	if d := m.Delta(current); d != nil {
		err := applyDelta(ctx, get, *d, pub, m.Version, target, work, unpacked, progress)
		if err == nil {
			return swap(unpacked, target)
		}
		if ctx.Err() != nil {
			return err
		}
		log.Printf("selfupdate: downloading the whole update, as its delta update failed: %v", err)
		if err := os.RemoveAll(unpacked); err != nil {
			return err
		}
	}

	if m.Size <= 0 || m.Size > updatefmt.MaxArchiveSize {
		return fmt.Errorf("invalid size %d", m.Size)
	}
	archive := filepath.Join(work, "update.tar.gz")
	if err := download(ctx, get, m.URL, m.Size, m.Signature, pub, archive, progress); err != nil {
		return err
	}
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	err = updatefmt.ExtractArchive(f, unpacked)
	f.Close()
	if err != nil {
		return err
	}
	return swap(unpacked, target)
}

// workPattern names the directory an update is unpacked in, next to target.
func workPattern(target string) string {
	name := filepath.Base(target)
	if name == "." || strings.ContainsFunc(name, func(r rune) bool { return r < 0x80 && os.IsPathSeparator(uint8(r)) }) {
		name = "app"
	}
	return "." + name + ".update-"
}

func applyDelta(ctx context.Context, get getter, d updatefmt.Delta, pub ed25519.PublicKey, version, target, work, dir string, progress func(downloaded, total int64)) error {
	if d.Size <= 0 || d.Size > updatefmt.MaxArchiveSize {
		return fmt.Errorf("invalid size %d", d.Size)
	}
	p := filepath.Join(work, "update.delta")
	if err := download(ctx, get, d.URL, d.Size, d.Signature, pub, p, progress); err != nil {
		return err
	}
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	if runtime.GOOS == "darwin" {
		if err := os.Mkdir(dir, 0o755); err != nil {
			return err
		}
		dir = filepath.Join(dir, filepath.Base(target))
	}
	return updatefmt.ApplyDelta(f, d.Size, d.From, version, target, dir)
}

// download fetches url into the file p and checks its size and signature.
func download(ctx context.Context, get getter, url string, size int64, signature string, pub ed25519.PublicKey, p string, progress func(downloaded, total int64)) error {
	body, err := get(ctx, url)
	if err != nil {
		return err
	}
	defer body.Close()
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	sum := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, sum), &progressReader{r: io.LimitReader(body, size+1), total: size, fn: progress})
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("downloading: %w", err)
	}
	if n != size {
		return fmt.Errorf("downloaded %d bytes, want %d", n, size)
	}
	return updatefmt.Verify(pub, sum.Sum(nil), signature)
}

type progressReader struct {
	r     io.Reader
	n     int64
	total int64
	fn    func(downloaded, total int64)
	last  time.Time
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.n += int64(n)
	if p.fn != nil && (err != nil || p.n == p.total || time.Since(p.last) > 100*time.Millisecond) {
		p.last = time.Now()
		p.fn(p.n, p.total)
	}
	return n, err
}

// ---- macOS: the bundle is replaced after the process exits ------------------

type stagedUpdate struct {
	work, bundle, target string
	helper               *exec.Cmd
}

var pendingUpdate atomic.Pointer[stagedUpdate]

// appPid is the process whose exit a staged update waits for.
var appPid = os.Getpid()

// stageBundle leaves the bundle in dir to replace target once this process
// has exited, in place of an update staged before.
func stageBundle(work, dir, target string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), ".app") {
		return errors.New("the update holds no app bundle")
	}
	p := &stagedUpdate{work: work, bundle: filepath.Join(dir, entries[0].Name()), target: target}
	p.helper = exec.Command("/bin/sh", "-c", installAfterExit, "pail-update", strconv.Itoa(appPid), p.target, p.bundle, p.work)
	if err := p.helper.Start(); err != nil {
		return err
	}
	if old := pendingUpdate.Swap(p); old != nil {
		_ = old.helper.Process.Kill()
		_ = old.helper.Wait()
		_ = os.RemoveAll(old.work)
	}
	return nil
}

// installAfterExit waits for the process to exit, swaps the bundles (putting
// the old one back if the new one cannot be moved in) and opens the app again
// when RelaunchAfterExit asked for it.
const installAfterExit = `pid=$1 target=$2 new=$3 work=$4
trap '' HUP
while kill -0 "$pid" 2>/dev/null; do sleep 0.05; done
if mv "$target" "$work/old"; then
	mv "$new" "$target" || mv "$work/old" "$target"
fi
relaunch=
if [ -e "$work/relaunch" ]; then relaunch=1; . "$work/relaunch"; fi
rm -rf "$work"
if [ -n "$relaunch" ]; then exec /usr/bin/open "$@"; fi
`

// Staged reports whether an update waits for this process to exit (macOS).
func Staged() bool { return pendingUpdate.Load() != nil }

// RelaunchAfterExit asks the helper of a staged update to open the app with
// args once it has installed the update, reporting whether there is one. The
// caller then quits; without a staged update it relaunches as usual.
func RelaunchAfterExit(args []string) bool {
	p := pendingUpdate.Load()
	if p == nil {
		return false
	}
	open := []string{p.target}
	if len(args) > 0 {
		open = append(append(open, "--args"), args...)
	}
	var b strings.Builder
	b.WriteString("set --")
	for _, a := range open {
		b.WriteString(" '" + strings.ReplaceAll(a, "'", `'\''`) + "'")
	}
	b.WriteString("\n")
	return os.WriteFile(filepath.Join(p.work, "relaunch"), []byte(b.String()), 0o644) == nil
}

// ---- Windows and Linux: the files of the app directory are replaced --------

// swapFiles replaces the entries of the app directory target with those in
// dir, and removes those that the version it replaces installed and dir lacks
// (updatefmt.FileList). Replaced entries are renamed aside and deleted once
// every entry of dir is in place; if one cannot be, everything goes back. The
// running executable is renamed away, which Windows allows, and removed at the
// next start (CleanLeftovers).
func swapFiles(dir, target string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var names []string
	ours := map[string]bool{}
	for _, e := range entries {
		if e.Name() != updatefmt.FileList {
			names = append(names, e.Name())
			ours[e.Name()] = true
		}
	}
	if len(names) == 0 {
		return errors.New("the update is empty")
	}
	suffix := ".old-" + strconv.Itoa(os.Getpid())
	var moved, placed [][2]string
	undo := func() {
		for i := len(placed) - 1; i >= 0; i-- {
			if rename(placed[i][1], placed[i][0]) != nil {
				_ = os.RemoveAll(placed[i][1])
			}
		}
		for i := len(moved) - 1; i >= 0; i-- {
			_ = rename(moved[i][1], moved[i][0])
		}
	}
	moveAside := func(dst string) error {
		aside := filepath.Join(target, "."+filepath.Base(dst)+suffix)
		if err := rename(dst, aside); err != nil {
			return err
		}
		moved = append(moved, [2]string{dst, aside})
		return nil
	}

	running := runningEntry(target)
	var left []string
	for _, name := range installedEntries(target) {
		dst := filepath.Join(target, name)
		if ours[name] || name == running {
			continue
		}
		if _, err := os.Lstat(dst); err != nil {
			continue
		}
		if err := moveAside(dst); err != nil {
			log.Printf("selfupdate: leaving %s, which the update no longer has: %v", dst, err)
			left = append(left, name)
		}
	}
	if err := updatefmt.WriteFileList(dir, append(names, left...)); err != nil {
		undo()
		return err
	}
	for _, name := range append(names, updatefmt.FileList) {
		src, dst := filepath.Join(dir, name), filepath.Join(target, name)
		if _, err := os.Lstat(dst); err == nil {
			if err := moveAside(dst); err != nil {
				undo()
				return err
			}
		}
		if err := rename(src, dst); err != nil {
			undo()
			return err
		}
		placed = append(placed, [2]string{src, dst})
	}
	for _, m := range moved {
		_ = os.RemoveAll(m[1])
	}
	return nil
}

func installedEntries(target string) []string {
	var b []byte
	err := retryInUse(func() (err error) {
		b, err = os.ReadFile(filepath.Join(target, updatefmt.FileList))
		return err
	})
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			log.Printf("selfupdate: removing none of the files that the update no longer has: %v", err)
		}
		return nil
	}
	return updatefmt.ParseFileList(b)
}

func runningEntry(target string) string {
	if startExe == "" {
		return ""
	}
	exe, dir := startExe, target
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	if filepath.Dir(exe) != filepath.Clean(dir) {
		return ""
	}
	return filepath.Base(exe)
}

// CleanLeftovers removes what an update could not remove while the previous
// version ran (Windows keeps running executables open).
func CleanLeftovers() {
	if runtime.GOOS == "darwin" || startExe == "" {
		return
	}
	exe := startExe
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	olds, _ := filepath.Glob(filepath.Join(filepath.Dir(exe), ".*.old-*"))
	for _, p := range olds {
		_ = os.RemoveAll(p)
	}
}

// rename renames a file of the app, retrying while Windows reports it in use
// (antivirus software and indexers open new files).
func rename(from, to string) error {
	return retryInUse(func() error { return os.Rename(from, to) })
}

func retryInUse(f func() error) error {
	for i := 1; ; i++ {
		err := f()
		if err == nil || i == 10 || runtime.GOOS != "windows" ||
			!errors.Is(err, syscall.Errno(32)) && !errors.Is(err, syscall.Errno(5)) {
			return err
		}
		time.Sleep(time.Duration(i) * 100 * time.Millisecond)
	}
}
