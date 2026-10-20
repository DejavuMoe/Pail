//go:build !windows && !linux

package safepath

import (
	"io/fs"
	"os"
)

func isReparsePoint(fs.FileInfo) bool { return false }

func publishNoReplace(tmp, final string) error { return publishLstat(tmp, final) }

func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
}
