package rules

import (
	"archive/zip"
	"compress/flate"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// CompressZIP writes one standard ZIP member with a basename only. Level zero
// stores bytes, -1 uses default deflate, -2 uses Huffman only, and 1..9 deflate.
// The count is source bytes read. Streams remain open. Errors leave partial
// output which callers must discard. Cancellation cannot interrupt blocked I/O.
func CompressZIP(ctx context.Context, dst io.Writer, src io.Reader, name string, level int) (int64, error) {
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
	fw, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: method})
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
