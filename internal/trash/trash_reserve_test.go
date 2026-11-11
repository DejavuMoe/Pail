package trash

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"testing"
)

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The no-replace move never replaces an existing destination file.
func TestFallbackNoReplaceKeepsExistingFile(t *testing.T) {
	d := t.TempDir()
	src, dst := filepath.Join(d, "src.txt"), filepath.Join(d, "dst.txt")
	write(t, src, "mine")
	write(t, dst, "theirs")
	if err := fallbackNoReplace(src, dst); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("err = %v, want fs.ErrExist", err)
	}
	if read(t, dst) != "theirs" || read(t, src) != "mine" {
		t.Fatal("an existing destination was replaced or the source was lost")
	}
	free := filepath.Join(d, "free.txt")
	if err := fallbackNoReplace(src, free); err != nil {
		t.Fatal(err)
	}
	if read(t, free) != "mine" {
		t.Fatal("content lost")
	}
	if _, err := os.Lstat(src); !os.IsNotExist(err) {
		t.Fatalf("source still there: %v", err)
	}
}

func TestFallbackNoReplaceSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fallback is only used on macOS; Windows reports privilege errors before EEXIST")
	}
	d := t.TempDir()
	src, dst := filepath.Join(d, "ln"), filepath.Join(d, "taken")
	if err := os.Symlink("target-x", src); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	write(t, dst, "theirs")
	if err := fallbackNoReplace(src, dst); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("err = %v, want fs.ErrExist", err)
	}
	if read(t, dst) != "theirs" {
		t.Fatal("existing destination replaced")
	}
	free := filepath.Join(d, "free")
	if err := fallbackNoReplace(src, free); err != nil {
		t.Fatal(err)
	}
	if got, err := os.Readlink(free); err != nil || got != "target-x" {
		t.Fatalf("link = %q, %v", got, err)
	}
	if _, err := os.Lstat(src); !os.IsNotExist(err) {
		t.Fatalf("source still there: %v", err)
	}
}

func TestFallbackNoReplaceDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fallback is only used on macOS; Windows cannot rename over an empty directory")
	}
	d := t.TempDir()
	src := filepath.Join(d, "srcdir")
	write(t, filepath.Join(src, "a.txt"), "a")
	empty := filepath.Join(d, "emptydst")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	// An existing EMPTY directory is the case plain rename(2) would replace.
	if err := fallbackNoReplace(src, empty); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("err = %v, want fs.ErrExist", err)
	}
	if read(t, filepath.Join(src, "a.txt")) != "a" {
		t.Fatal("source lost")
	}
	free := filepath.Join(d, "freedir")
	if err := fallbackNoReplace(src, free); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(free, "a.txt")) != "a" {
		t.Fatal("content lost")
	}
	if _, err := os.Lstat(src); !os.IsNotExist(err) {
		t.Fatalf("source still there: %v", err)
	}
}

// Another process takes the name between our attempt's name choice and the
// move: the earlier trashed file must survive and ours takes the next name.
func TestMoveUniqueDoesNotOverwriteAfterRace(t *testing.T) {
	trashDir, srcDir := t.TempDir(), t.TempDir()
	src := filepath.Join(srcDir, "report.txt")
	write(t, src, "mine")
	first := true
	rename := func(s, dst string) error {
		if first {
			first = false
			write(t, dst, "theirs") // the concurrent winner
		}
		return fallbackNoReplace(s, dst)
	}
	if err := moveUnique(trashDir, src, rename); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(trashDir, "report.txt")); got != "theirs" {
		t.Fatalf("the first trashed file was overwritten: %q", got)
	}
	ents, _ := os.ReadDir(trashDir)
	if len(ents) != 2 {
		t.Fatalf("want 2 entries, got %d", len(ents))
	}
	for _, e := range ents {
		if e.Name() != "report.txt" && read(t, filepath.Join(trashDir, e.Name())) != "mine" {
			t.Fatalf("our file is missing under %s", e.Name())
		}
	}
}

// Many same-named files trashed at the same time all survive.
func TestMoveUniqueConcurrentSameName(t *testing.T) {
	trashDir := t.TempDir()
	const n = 16
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		src := filepath.Join(t.TempDir(), "same.txt")
		write(t, src, "content-"+strconv.Itoa(i))
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- moveUnique(trashDir, src, fallbackNoReplace)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	ents, _ := os.ReadDir(trashDir)
	for _, e := range ents {
		seen[read(t, filepath.Join(trashDir, e.Name()))] = true
	}
	if len(seen) != n {
		t.Fatalf("%d of %d files survived", len(seen), n)
	}
}

func TestMoveUniqueErrors(t *testing.T) {
	d := t.TempDir()
	src := filepath.Join(d, "f")
	write(t, src, "x")
	err := moveUnique(d, src, func(string, string) error { return &os.LinkError{Op: "rename", Err: syscall.EXDEV} })
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("EXDEV: %v", err)
	}
	err = moveUnique(d, src, func(string, string) error { return fs.ErrExist })
	if err == nil || errors.Is(err, ErrUnavailable) {
		t.Fatalf("exhausted names: %v", err)
	}
	err = moveUnique(d, src, func(string, string) error { return ErrUnavailable })
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unavailable passthrough: %v", err)
	}
}
