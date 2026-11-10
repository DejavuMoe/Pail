package rules

import (
	"archive/zip"
	"compress/flate"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

// CompressZIP writes one standard ZIP member with a basename only, stamped with\n// the modification time of the source file (see zipTimestamp). Level zero
// stores bytes, -1 uses default deflate, -2 uses Huffman only, and 1..9 deflate.
// The count is source bytes read. Streams remain open. Errors leave partial
// output which callers must discard. Cancellation cannot interrupt blocked I/O.
func CompressZIP(ctx context.Context, dst io.Writer, src io.Reader, name string, modified time.Time, level int) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := validateZIPName(name); err != nil {
		return 0, err
	}
	if level < -2 || level > 9 {
		return 0, fmt.Errorf("ZIP level must be -2..9")
	}
	zw := zip.NewWriter(contextWriter{ctx, dst})
	method := uint16(zip.Deflate)
	if level == 0 {
		method = zip.Store
	} else {
		zw.RegisterCompressor(zip.Deflate, func(w io.Writer) (io.WriteCloser, error) { return flate.NewWriter(w, level) })
	}
	fw, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: method, Modified: zipTimestamp(modified)})
	if err != nil {
		return 0, errors.Join(err, zw.Close())
	}
	n, copyErr := io.CopyBuffer(fw, contextReader{ctx, src}, make([]byte, 32*1024))
	return n, errors.Join(copyErr, zw.Close(), ctx.Err())
}
func validateZIPName(name string) error {
	if name == "" || name == "." || name == ".." || !utf8.ValidString(name) || len(name) > 65535 || strings.ContainsAny(name, "/\\:\x00\r\n") {
		return fmt.Errorf("ZIP member must be a single valid filename")
	}
	return nil
}

// zipTimestamp is the time a member is stamped with: the modification time of
// the source file, the current time when that is unknown (zero), limited to the
// years both an MS-DOS date and a 32-bit Unix time can hold (1980..2105) so that\n// it never wraps.
func zipTimestamp(modified time.Time) time.Time {
	if modified.IsZero() {
		modified = time.Now()
	}
	switch {
	case modified.Year() < 1980:
		return time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)
	case modified.Year() > 2105:
		return time.Date(2105, 12, 31, 23, 59, 58, 0, time.UTC)
	}
	return modified
}
