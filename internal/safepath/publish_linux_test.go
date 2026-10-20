//go:build linux

package safepath

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPublishLinkFallback(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "t")
	tmp := writeTemp(t, dir, "linked")
	if err := publishLink(tmp, target); err != nil {
		t.Fatal(err)
	}
	if read(t, target) != "linked" {
		t.Fatal("content lost")
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatal("temp not unlinked")
	}
	if err := publishLink(writeTemp(t, dir, "again"), target); !errors.Is(err, ErrExists) {
		t.Fatalf("want ErrExists, got %v", err)
	}
	if read(t, target) != "linked" {
		t.Fatal("target modified")
	}
}
