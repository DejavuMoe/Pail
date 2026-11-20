// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package zip

import (
	"compress/flate"
	"io"
)

// A Compressor returns a compressing writer, writing to the
// provided writer. On Close, any pending data should be flushed.
type Compressor func(io.Writer) (io.WriteCloser, error)

// compressor returns the built-in compressor for Store and Deflate.
// Writer.RegisterCompressor overrides it per Writer.
func compressor(method uint16) Compressor {
	switch method {
	case Store:
		return func(w io.Writer) (io.WriteCloser, error) { return &nopCloser{w}, nil }
	case Deflate:
		return func(w io.Writer) (io.WriteCloser, error) { return flate.NewWriter(w, 5) }
	}
	return nil
}

// decompressor wraps r with a decompressing reader for Store and Deflate.
func decompressor(method uint16, r io.Reader) (io.ReadCloser, bool) {
	switch method {
	case Store:
		return io.NopCloser(r), true
	case Deflate:
		return flate.NewReader(r), true
	}
	return nil, false
}
