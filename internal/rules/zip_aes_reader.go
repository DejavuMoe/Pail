package rules

import (
	"archive/zip"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"

	azip "github.com/DejavuMoe/Pail/internal/rules/internal/zipaes"
)

// DecryptAES256ZIP authenticates the complete ciphertext before streaming a
// single AES-256 member. src MUST remain immutable for the whole call: use a
// private snapshot file handle, never reopen an external path between passes.
// dst must be temporary output; publish only after a nil error (including CRC
// and size checks). ZIP names and metadata are not authenticated by WinZip AES.
// No fixed file-size cap is imposed. The caller controls disk quota via dst.
func DecryptAES256ZIP(ctx context.Context, dst io.Writer, src io.ReaderAt, size int64, passwordRef string, resolver PasswordResolver) (name string, n int64, err error) {
	if err := ctx.Err(); err != nil {
		return "", 0, err
	}
	if size < 0 {
		return "", 0, fmt.Errorf("invalid ZIP size")
	}
	reader := contextReaderAt{ctx, src}
	// Use the maintained standard ZIP parser as the untrusted-input boundary.
	standard, err := zip.NewReader(reader, size)
	if err != nil {
		return "", 0, err
	}
	if len(standard.File) != 1 {
		return "", 0, fmt.Errorf("AES ZIP must contain exactly one file")
	}
	sf := standard.File[0]
	if err := validateZIPName(sf.Name); err != nil {
		return "", 0, err
	}
	if !sf.Mode().IsRegular() || sf.Flags&1 == 0 || sf.Method != 99 {
		return "", 0, fmt.Errorf("ZIP member must be a regular AES-256 encrypted file")
	}
	method, err := validateAESExtra(sf.Extra)
	if err != nil {
		return "", 0, err
	}
	off, err := sf.DataOffset()
	if err != nil {
		return "", 0, err
	}
	if off < 0 || off > size || sf.CompressedSize64 < 28 || sf.CompressedSize64 > uint64(size-off) || sf.UncompressedSize64 > math.MaxInt64 {
		return "", 0, fmt.Errorf("invalid AES ZIP data boundaries")
	}
	password, err := resolveZIPPassword(ctx, passwordRef, resolver)
	if err != nil {
		return "", 0, err
	}
	zr, err := azip.NewReader(reader, size)
	if err != nil {
		return "", 0, err
	}
	if len(zr.File) != 1 {
		return "", 0, fmt.Errorf("inconsistent ZIP directory")
	}
	f := zr.File[0]
	actualOffset, err := f.DataOffset()
	if err != nil {
		return "", 0, err
	}
	if f.Name != sf.Name || f.Method != method || f.CompressedSize64 != sf.CompressedSize64 || f.UncompressedSize64 != sf.UncompressedSize64 || actualOffset != off {
		return "", 0, fmt.Errorf("inconsistent ZIP member headers")
	}
	f.SetPassword(password)
	f.DeferAuth = false
	// The first pass authenticates the entire ciphertext before Open returns.
	rc, err := f.Open()
	if err != nil {
		return "", 0, err
	}
	n, copyErr := io.CopyBuffer(&zipSizeWriter{w: contextWriter{ctx, dst}, remaining: int64(sf.UncompressedSize64)}, contextReader{ctx, rc}, make([]byte, 32*1024))
	err = errors.Join(copyErr, rc.Close(), ctx.Err())
	if err == nil && uint64(n) != sf.UncompressedSize64 {
		err = fmt.Errorf("ZIP plaintext size mismatch")
	}
	return sf.Name, n, err
}
func validateAESExtra(extra []byte) (uint16, error) {
	found := false
	var method uint16
	for len(extra) >= 4 {
		id, n := binary.LittleEndian.Uint16(extra[:2]), int(binary.LittleEndian.Uint16(extra[2:4]))
		extra = extra[4:]
		if n > len(extra) {
			return 0, fmt.Errorf("truncated ZIP extra field")
		}
		data := extra[:n]
		extra = extra[n:]
		if id != 0x9901 {
			continue
		}
		if found || n != 7 {
			return 0, fmt.Errorf("invalid or duplicate WinZip AES field")
		}
		found = true
		version := binary.LittleEndian.Uint16(data[:2])
		method = binary.LittleEndian.Uint16(data[5:7])
		if (version != 1 && version != 2) || string(data[2:4]) != "AE" || data[4] != 3 || (method != zip.Store && method != zip.Deflate) {
			return 0, fmt.Errorf("only WinZip AES-256 Store/Deflate is supported")
		}
	}
	if !found || len(extra) != 0 {
		return 0, fmt.Errorf("missing or malformed WinZip AES field")
	}
	return method, nil
}

type contextReaderAt struct {
	ctx context.Context
	r   io.ReaderAt
}

func (r contextReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.ReadAt(p, off)
}

// ZIP headers are untrusted; stop before exceeding their declared plaintext size.
type zipSizeWriter struct {
	w         io.Writer
	remaining int64
}

func (w *zipSizeWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, fmt.Errorf("ZIP plaintext exceeds declared size")
	}
	n, err := w.w.Write(p)
	w.remaining -= int64(n)
	return n, err
}
