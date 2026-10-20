package rules

import (
	"compress/gzip"
	"context"
	"errors"
	"io"
)

// CompressGZIP streams a standard gzip member. The count is source bytes read,
// not compressed bytes. It does not close src or dst and starts no goroutines.
// Cancellation is checked between reads/writes; callers must interrupt an
// already-blocked underlying Reader/Writer themselves. Partial output on error
// must be discarded. Levels: -2 Huffman only, -1 default, 0..9.
func CompressGZIP(ctx context.Context, dst io.Writer, src io.Reader, level int) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	zw, err := gzip.NewWriterLevel(contextWriter{ctx, dst}, level)
	if err != nil {
		return 0, err
	}
	n, copyErr := io.CopyBuffer(zw, contextReader{ctx, src}, make([]byte, 32*1024))
	closeErr := zw.Close()
	return n, errors.Join(copyErr, closeErr, ctx.Err())
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

type contextWriter struct {
	ctx context.Context
	w   io.Writer
}

func (w contextWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	return w.w.Write(p)
}
