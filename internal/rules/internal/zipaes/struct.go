// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
Package zip reads and writes WinZip AES-256 encrypted ZIP archives for
internal/rules. It is a trimmed fork of github.com/yeka/zip (itself derived
from Go's archive/zip) that keeps only what internal/rules uses and is no
longer synced with upstream. See README.md.

See: http://www.pkware.com/documents/casestudies/APPNOTE.TXT
and http://www.winzip.com/aes_info.htm

This package does not support disk spanning.

A note about ZIP64:

To be backwards compatible the FileHeader has both 32 and 64 bit Size
fields. The 64 bit fields will always contain the correct value and
for normal archives both fields will be the same. For files requiring
the ZIP64 format the 32 bit fields will be 0xffffffff and the 64 bit
fields must be used instead.
*/
package zip

import "time"

// Compression methods.
const (
	Store   uint16 = 0
	Deflate uint16 = 8
)

const (
	fileHeaderSignature      = 0x04034b50
	directoryHeaderSignature = 0x02014b50
	directoryEndSignature    = 0x06054b50
	directory64LocSignature  = 0x07064b50
	directory64EndSignature  = 0x06064b50
	dataDescriptorSignature  = 0x08074b50 // de-facto standard; required by OS X Finder
	fileHeaderLen            = 30         // + filename + extra
	directoryHeaderLen       = 46         // + filename + extra + comment
	directoryEndLen          = 22         // + comment
	dataDescriptorLen        = 16         // four uint32: descriptor signature, crc32, compressed size, size
	dataDescriptor64Len      = 24         // descriptor with 8 byte sizes
	directory64LocLen        = 20         //
	directory64EndLen        = 56         // + extra

	// version numbers
	zipVersion20 = 20 // 2.0
	zipVersion45 = 45 // 4.5 (reads and writes zip64 archives)

	// limits for non zip64 files
	uint16max = (1 << 16) - 1
	uint32max = (1 << 32) - 1

	// extra header id's
	zip64ExtraId     = 0x0001 // zip64 Extended Information Extra Field
	winzipAesExtraId = 0x9901 // winzip AES Extra Field
)

// FileHeader describes a file within a zip file.
// See the zip spec for details.
type FileHeader struct {
	// Name is the name of the file.
	// It must be a relative path: it must not start with a drive
	// letter (e.g. C:) or leading slash, and only forward slashes
	// are allowed.
	Name string

	CreatorVersion     uint16
	ReaderVersion      uint16
	Flags              uint16
	Method             uint16
	ModifiedTime       uint16 // MS-DOS time
	ModifiedDate       uint16 // MS-DOS date
	CRC32              uint32
	CompressedSize     uint32 // Deprecated: Use CompressedSize64 instead.
	UncompressedSize   uint32 // Deprecated: Use UncompressedSize64 instead.
	CompressedSize64   uint64
	UncompressedSize64 uint64
	Extra              []byte
	ExternalAttrs      uint32 // Meaning depends on CreatorVersion
	Comment            string

	password    passwordFn // Returns the password to use when reading/writing
	ae          uint16
	aesStrength byte
}

type directoryEnd struct {
	diskNbr            uint32 // unused
	dirDiskNbr         uint32 // unused
	dirRecordsThisDisk uint64 // unused
	directoryRecords   uint64
	directorySize      uint64
	directoryOffset    uint64 // relative to file
	commentLen         uint16
	comment            string
}

// timeToMsDosTime converts a time.Time to an MS-DOS date and time.
// The resolution is 2s.
// See: http://msdn.microsoft.com/en-us/library/ms724274(v=VS.85).aspx
func timeToMsDosTime(t time.Time) (fDate uint16, fTime uint16) {
	t = t.In(time.UTC)
	fDate = uint16(t.Day() + int(t.Month())<<5 + (t.Year()-1980)<<9)
	fTime = uint16(t.Second()/2 + t.Minute()<<5 + t.Hour()<<11)
	return
}

// SetModTime sets the ModifiedTime and ModifiedDate fields to the given time in UTC.
// The resolution is 2s.
func (h *FileHeader) SetModTime(t time.Time) {
	h.ModifiedDate, h.ModifiedTime = timeToMsDosTime(t)
}

// isZip64 reports whether the file size exceeds the 32 bit limit
func (fh *FileHeader) isZip64() bool {
	return fh.CompressedSize64 > uint32max || fh.UncompressedSize64 > uint32max
}
