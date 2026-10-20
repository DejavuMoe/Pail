//go:build linux

package trash

import (
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// parseInfo reads a .trashinfo file and returns its decoded Path and DeletionDate.
func parseInfo(t *testing.T, p string) (path string, date time.Time) {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 3 || lines[0] != "[Trash Info]" {
		t.Fatalf("bad trashinfo:\n%s", b)
	}
	for _, l := range lines[1:] {
		k, v, _ := strings.Cut(l, "=")
		switch k {
		case "Path":
			d, err := url.PathUnescape(v)
			if err != nil {
				t.Fatal(err)
			}
			path = d
		case "DeletionDate":
			if date, err = time.ParseInLocation("2006-01-02T15:04:05", v, time.Local); err != nil {
				t.Fatal(err)
			}
		}
	}
	return path, date
}

func TestLinuxHomeTrashLayout(t *testing.T) {
	data := isolate(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "a b#%.txt")
	if err := os.WriteFile(p, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := time.Now().Add(-2 * time.Second)
	if err := MoveToTrash(p); err != nil {
		t.Fatal(err)
	}
	info := filepath.Join(data, "Trash", "info", "a b#%.txt.trashinfo")
	gotPath, date := parseInfo(t, info)
	if gotPath != p {
		t.Errorf("Path = %q, want %q", gotPath, p)
	}
	if date.Before(before) || date.After(time.Now().Add(2*time.Second)) {
		t.Errorf("DeletionDate %v not near now", date)
	}
	raw, _ := os.ReadFile(info)
	if strings.Contains(string(raw), "a b#%") {
		t.Errorf("Path is not escaped:\n%s", raw)
	}
	b, err := os.ReadFile(filepath.Join(data, "Trash", "files", "a b#%.txt"))
	if err != nil || string(b) != "one" {
		t.Fatalf("trashed file: %q %v", b, err)
	}
	if st, err := os.Stat(filepath.Join(data, "Trash")); err != nil || st.Mode().Perm() != 0o700 {
		t.Errorf("trash dir mode: %v %v", st, err)
	}

	// Same name again: both survive under unique names.
	if err := os.WriteFile(p, []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := MoveToTrash(p); err != nil {
		t.Fatal(err)
	}
	ents, _ := os.ReadDir(filepath.Join(data, "Trash", "files"))
	if len(ents) != 2 {
		t.Fatalf("want 2 trashed files, got %d", len(ents))
	}
	infos, _ := os.ReadDir(filepath.Join(data, "Trash", "info"))
	if len(infos) != 2 {
		t.Fatalf("want 2 info files, got %d", len(infos))
	}
}

func TestLinuxSymlinkMovedNotTarget(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	link := filepath.Join(dir, "link")
	if err := os.WriteFile(target, []byte("t"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skip(err)
	}
	if err := MoveToTrash(link); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatal("link still exists")
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("target must stay: %v", err)
	}
}

func TestLinuxVolumeTrashPathIsRelative(t *testing.T) {
	top := t.TempDir()
	p := filepath.Join(top, "sub", "f.txt")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir, err := volumeTrash(top)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(dir) != ".Trash-"+strconv.Itoa(os.Getuid()) {
		t.Errorf("volume trash dir = %s", dir)
	}
	if err := trashInto(dir, top, p); err != nil {
		t.Fatal(err)
	}
	got, _ := parseInfo(t, filepath.Join(dir, "info", "f.txt.trashinfo"))
	if got != "sub/f.txt" {
		t.Errorf("Path = %q, want relative sub/f.txt", got)
	}
}

func TestLinuxTopDirIsAncestorOnSameDevice(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x")
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	dev, err := deviceOf(p)
	if err != nil {
		t.Fatal(err)
	}
	top, err := topDir(p, dev)
	if err != nil {
		t.Fatal(err)
	}
	if rel, err := filepath.Rel(top, p); err != nil || strings.HasPrefix(rel, "..") {
		t.Fatalf("top %q is not an ancestor of %q", top, p)
	}
	if d, _ := deviceOf(top); d != dev {
		t.Fatal("top dir on another device")
	}
}
