//go:build !windows && !linux

package safepath

import (
	"io/fs"
	"os"
)

func isReparsePoint(fs.FileInfo) bool { return false }

// SyncDir flushes the directory entry to disk, best effort.
func SyncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
}
