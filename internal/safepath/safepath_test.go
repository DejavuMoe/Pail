package safepath

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestValidKeyTable(t *testing.T) {
	type tc struct {
		key     string
		windows bool // validate under Windows rules
		ok      bool
	}
	cases := []tc{
		// accepted everywhere
		{"a.txt", false, true}, {"a.txt", true, true}, {"dir/sub/file name+1.txt", true, true},
		{"目录/文件.txt", true, true}, {"..a/b..c/.hidden", true, true}, {"a b/c", true, true},
		{"CONSOLE.txt", true, true}, {"COM10", true, true}, {"com/x", true, true},
		// rejected everywhere
		{"", false, false}, {"/abs", false, false}, {"//server/share/x", false, false},
		{"../x", false, false}, {"a/../x", false, false}, {"a/..", false, false}, {"./x", false, false}, {"a/./b", false, false},
		{"a//b", false, false}, {"a/", false, false}, {"a\x00b", false, false}, {"C:/x", false, false}, {"c:x", false, false}, {"Z:\\x", false, false},
		// Windows only
		{`a\b`, true, false}, {`..\x`, true, false}, {"CON", true, false}, {"con", true, false}, {"con.txt", true, false},
		{"dir/NUL", true, false}, {"dir/nul.tar.gz", true, false}, {"PRN.x", true, false}, {"AUX", true, false},
		{"COM1", true, false}, {"com9.txt", true, false}, {"LPT1", true, false}, {"lpt9.log", true, false},
		{"CON .txt", true, false}, {"CONIN$", true, false}, {"CONOUT$.x", true, false},
		{"name.", true, false}, {"name ", true, false}, {"dir /x", true, false}, {"a/b.", true, false},
		{"a<b", true, false}, {"a>b", true, false}, {"a:b", true, false}, {`a"b`, true, false}, {"a|b", true, false},
		{"a?b", true, false}, {"a*b", true, false}, {"a\x1fb", true, false}, {"file.txt:stream", true, false},
		// legal on POSIX, rejected on Windows
		{`a\b`, false, true}, {"CON", false, true}, {"a<b", false, true}, {"name.", false, true},
	}
	for _, c := range cases {
		err := validKey(c.key, c.windows)
		if c.ok != (err == nil) {
			t.Errorf("validKey(%q, windows=%v) = %v, want ok=%v", c.key, c.windows, err, c.ok)
		}
		if err != nil && !errors.Is(err, ErrUnsafe) {
			t.Errorf("rejection of %q is not ErrUnsafe: %v", c.key, err)
		}
	}
}

func TestJoin(t *testing.T) {
	root := t.TempDir()
	got, err := Join(root, "a/b/c.txt")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "a", "b", "c.txt"); got != want {
		t.Fatalf("got %s want %s", got, want)
	}
	for _, key := range []string{"../escape", "a/../../escape", "", "/etc/passwd", "a//b"} {
		if p, err := Join(root, key); err == nil {
			t.Errorf("Join(%q) = %s, want error", key, p)
		}
	}
	if _, err := Join("", "a"); err == nil {
		t.Error("empty root accepted")
	}
	// A relative root is made absolute.
	if p, err := Join(".", "x"); err != nil || !filepath.IsAbs(p) {
		t.Errorf("relative root: %q %v", p, err)
	}
}

// link creates a symlink (or a junction on Windows, which needs no privilege).
func link(t *testing.T, target, name string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", name, target).CombinedOutput(); err != nil {
			t.Skipf("cannot create junction: %v %s", err, out)
		}
		return
	}
	if err := os.Symlink(target, name); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
}

func TestJoinRefusesLinks(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	link(t, outside, filepath.Join(root, "escape"))
	for _, key := range []string{"escape/file.txt", "escape", "escape/deeper/file.txt"} {
		if p, err := Join(root, key); !errors.Is(err, ErrUnsafe) {
			t.Errorf("Join(%q) = %q, %v; want ErrUnsafe", key, p, err)
		}
	}
	// Ordinary directories and not-yet-existing paths remain fine.
	if err := os.MkdirAll(filepath.Join(root, "real", "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"real/dir/f", "real/new/f", "new"} {
		if _, err := Join(root, key); err != nil {
			t.Errorf("Join(%q): %v", key, err)
		}
	}
	// A link used as the root itself is the user's choice and is allowed.
	rootLink := filepath.Join(t.TempDir(), "rootlink")
	link(t, outside, rootLink)
	if _, err := Join(rootLink, "f"); err != nil {
		t.Errorf("link as root rejected: %v", err)
	}
}

func TestCreateTemp(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "new", "dir")
	f, err := CreateTemp(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	base := filepath.Base(f.Name())
	if filepath.Dir(f.Name()) != dir || !strings.HasPrefix(base, ".pail-") || !strings.HasSuffix(base, ".tmp") {
		t.Fatalf("unexpected temp file %s", f.Name())
	}
}

func writeTemp(t *testing.T, dir, content string) string {
	t.Helper()
	f, err := CreateTemp(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	return f.Name()
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPublishSemantics(t *testing.T) {
	dir := t.TempDir()
	final := filepath.Join(dir, "sub", "out.bin")
	for _, overwrite := range []bool{false, true} {
		t.Run("new target", func(t *testing.T) {
			target := final + map[bool]string{false: ".a", true: ".b"}[overwrite]
			tmp := writeTemp(t, filepath.Dir(final), "data")
			if err := Publish(tmp, target, overwrite); err != nil {
				t.Fatal(err)
			}
			if read(t, target) != "data" {
				t.Fatal("content lost")
			}
			if _, err := os.Stat(tmp); !os.IsNotExist(err) {
				t.Fatal("temp file left behind")
			}
		})
	}
	t.Run("exists without overwrite", func(t *testing.T) {
		target := filepath.Join(dir, "keep.txt")
		if err := os.WriteFile(target, []byte("original"), 0o644); err != nil {
			t.Fatal(err)
		}
		tmp := writeTemp(t, dir, "new")
		err := Publish(tmp, target, false)
		if !errors.Is(err, ErrExists) {
			t.Fatalf("want ErrExists, got %v", err)
		}
		if read(t, target) != "original" {
			t.Fatal("existing file was modified")
		}
		if _, err := os.Stat(tmp); err != nil {
			t.Fatal("temp file must be kept on ErrExists so the caller can retry or clean up")
		}
	})
	t.Run("exists with overwrite", func(t *testing.T) {
		target := filepath.Join(dir, "replace.txt")
		if err := os.WriteFile(target, []byte("original"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := Publish(writeTemp(t, dir, "replacement"), target, true); err != nil {
			t.Fatal(err)
		}
		if read(t, target) != "replacement" {
			t.Fatal("not replaced")
		}
	})
	t.Run("missing temp", func(t *testing.T) {
		if err := Publish(filepath.Join(dir, "nope.tmp"), filepath.Join(dir, "x"), false); err == nil {
			t.Fatal("missing temp file accepted")
		}
	})
	t.Run("directory target", func(t *testing.T) {
		target := filepath.Join(dir, "adir")
		if err := os.Mkdir(target, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := Publish(writeTemp(t, dir, "x"), target, false); !errors.Is(err, ErrExists) {
			t.Fatalf("want ErrExists for directory, got %v", err)
		}
	})
}

func TestPublishLstatFallback(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "t")
	tmp := writeTemp(t, dir, "x")
	if err := publishLstat(tmp, target); err != nil {
		t.Fatal(err)
	}
	if err := publishLstat(writeTemp(t, dir, "y"), target); !errors.Is(err, ErrExists) {
		t.Fatalf("want ErrExists, got %v", err)
	}
	if read(t, target) != "x" {
		t.Fatal("target modified")
	}
}
