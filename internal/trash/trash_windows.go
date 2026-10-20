//go:build windows

package trash

import (
	"fmt"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// SHFileOperation flags (shellapi.h).
const (
	foDelete            = 0x0003
	fofSilent           = 0x0004
	fofNoConfirmation   = 0x0010
	fofAllowUndo        = 0x0040
	fofNoErrorUI        = 0x0400
	fofWantNukeWarnings = 0x4000

	// maxPath: SHFileOperation deletes longer paths permanently, never recycles them.
	maxPath = 260
)

// shFileOpStruct is SHFILEOPSTRUCTW as laid out for 64-bit Windows (natural
// alignment, pshpack8.h). Pail does not support 32-bit Windows, where the
// structure is packed to one byte.
type shFileOpStruct struct {
	Hwnd                 uintptr
	Func                 uint32
	From                 *uint16
	To                   *uint16
	Flags                uint16
	AnyOperationsAborted int32
	NameMappings         uintptr
	ProgressTitle        *uint16
}

var procSHFileOperation = windows.NewLazySystemDLL("shell32.dll").NewProc("SHFileOperationW")

// moveToTrash sends path to the Recycle Bin with SHFileOperationW.
//
// SHFileOperation silently deletes permanently when it cannot recycle: for a
// path of MAX_PATH characters or more, on drives without a Recycle Bin (network,
// removable) and, with FOF_NOCONFIRMATION, for items larger than the bin. The
// first two are rejected up front (ErrUnavailable). FOF_WANTNUKEWARNING, which
// is added to the requested flags, makes the shell ask before a remaining
// permanent deletion instead of doing it; declining leaves the item in place,
// which the caller reports as an error.
func moveToTrash(path string) error {
	if len(path) >= maxPath {
		return fmt.Errorf("%w: the path is too long for the Recycle Bin", ErrUnavailable)
	}
	vol := filepath.VolumeName(path)
	if len(vol) != 2 || vol[1] != ':' {
		return fmt.Errorf("%w: %s is not on a local drive letter", ErrUnavailable, path)
	}
	root, err := windows.UTF16PtrFromString(vol + `\`)
	if err != nil {
		return fmt.Errorf("trash: %w", err)
	}
	if windows.GetDriveType(root) != windows.DRIVE_FIXED {
		return fmt.Errorf("%w: %s has no Recycle Bin (not a fixed drive)", ErrUnavailable, vol)
	}
	// pFrom is a list of NUL-terminated names ended by an extra NUL.
	u16, err := windows.UTF16FromString(path)
	if err != nil {
		return fmt.Errorf("trash: %w", err)
	}
	from := append(u16, 0)
	op := shFileOpStruct{
		Func:  foDelete,
		From:  &from[0],
		Flags: fofAllowUndo | fofNoConfirmation | fofNoErrorUI | fofSilent | fofWantNukeWarnings,
	}
	r, _, _ := procSHFileOperation.Call(uintptr(unsafe.Pointer(&op)))
	if r != 0 {
		return fmt.Errorf("trash: SHFileOperation failed with code 0x%X", r)
	}
	if op.AnyOperationsAborted != 0 {
		return fmt.Errorf("trash: the move to the Recycle Bin was aborted")
	}
	return nil
}
