//go:build windows

package trash

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// MAX_PATH counts UTF-16 code units, not UTF-8 bytes: a path of CJK characters
// that is far longer than 260 bytes is still short enough for the Recycle Bin.
func TestMoveToTrashMaxPathCountsUTF16Units(t *testing.T) {
	dir := os.TempDir()
	vol := filepath.VolumeName(dir)
	if len(vol) != 2 {
		t.Skipf("temp dir %q is not on a drive letter", dir)
	}
	// The name does not exist: nothing is recycled, we only look at the up-front check.
	short := filepath.Join(vol+`\`, strings.Repeat("文", 90)) // 3+270 bytes, 93 units
	if len(short) < maxPath {
		t.Fatalf("setup: %d bytes", len(short))
	}
	err := moveToTrash(short)
	if err != nil && strings.Contains(err.Error(), "too long") {
		t.Fatalf("a path of %d UTF-16 units was refused as too long: %v", 93, err)
	}

	// A path of maxPath UTF-16 units is refused, whatever its bytes.
	long := filepath.Join(vol+`\`, strings.Repeat("a", maxPath))
	if err := moveToTrash(long); !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "too long") {
		t.Fatalf("a path of %d units: %v", len(long), err)
	}
	longCJK := filepath.Join(vol+`\`, strings.Repeat("文", maxPath))
	if err := moveToTrash(longCJK); !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "too long") {
		t.Fatalf("a long CJK path: %v", err)
	}
}
