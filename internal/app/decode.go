package app

import (
	"archive/zip"
	"compress/gzip"
	"context"
	"io"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/DejavuMoe/Pail/internal/rules"
	"github.com/DejavuMoe/Pail/internal/storage"
)

// Client-side decoding of downloaded objects. The downloaded bytes live in a
// private temporary file; decoding streams them into a second temporary file.
// Nothing is published unless decoding finishes without error (CRC, AES
// authentication, size limit), so a wrong password or a decompression bomb
// leaves no file behind.

// decodeHints is what the object itself says about its encoding: the
// Content-Encoding header and the pail-compression marker that Pail writes
// on compressed uploads (gzip, zip or aes-zip).
type decodeHints struct {
	ContentEncoding string
	Compression     string
}

func (h decodeHints) gzip() bool {
	for _, e := range strings.Split(h.ContentEncoding, ",") {
		if strings.EqualFold(strings.TrimSpace(e), "gzip") {
			return true
		}
	}
	return strings.EqualFold(h.Compression, "gzip")
}

func (h decodeHints) zip() bool {
	return strings.EqualFold(h.Compression, "zip") || strings.EqualFold(h.Compression, "aes-zip")
}

// headDecodeHints reads the decode hints of one object (or version) with a HEAD.
func headDecodeHints(ctx context.Context, c *storage.Client, bucket, key, versionID string) (decodeHints, error) {
	in := &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)}
	if versionID != "" {
		in.VersionId = aws.String(versionID)
	}
	h, err := c.S3().HeadObject(ctx, in)
	if err != nil {
		return decodeHints{}, classify("Download", err)
	}
	return decodeHints{ContentEncoding: aws.ToString(h.ContentEncoding), Compression: metaGet(h.Metadata, compressionMetaKey)}, nil
}

// detectDecode resolves DecodeAuto. Auto only undoes compression Pail itself
// applied: gzip when the object says Content-Encoding: gzip or carries the
// pail-compression=gzip marker, ZIP when the marker says zip or aes-zip, and
// in both cases only if the first bytes agree (gzip 1f 8b, ZIP "PK\x03\x04").
// Everything else (a user's backup.tar.gz, an archive they uploaded) is
// DecodeRaw: auto never fails on a plain file and never unpacks someone else's
// archive. Other modes are returned unchanged.
func detectDecode(in io.ReaderAt, size int64, mode DecodeMode, hints decodeHints) (DecodeMode, error) {
	if mode != DecodeAuto {
		return mode, nil
	}
	var magic [4]byte
	n, err := in.ReadAt(magic[:], 0)
	if err != nil && err != io.EOF {
		return DecodeRaw, err
	}
	switch {
	case hints.gzip() && n >= 2 && magic[0] == 0x1f && magic[1] == 0x8b:
		return DecodeGzip, nil
	case hints.zip() && n >= 4 && magic == [4]byte{'P', 'K', 3, 4}:
		return DecodeZip, nil
	}
	return DecodeRaw, nil
}

// decodeInto decodes in (size bytes) into out as mode (DecodeGzip or
// DecodeZip), writing at most limit bytes. An encrypted ZIP member is
// decrypted with the secret stored under passwordRef.
func (s *Service) decodeInto(ctx context.Context, in *os.File, size int64, out io.Writer, mode DecodeMode, passwordRef string, limit int64) error {
	const op = "Decode"
	lw := &decodeLimitWriter{w: out, remaining: limit, limit: limit}
	switch mode {
	case DecodeGzip:
		zr, err := gzip.NewReader(decodeCtxReader{ctx, io.NewSectionReader(in, 0, size)})
		if err != nil {
			return invalid(op, "not a valid gzip stream: %v", err)
		}
		_, err = io.Copy(lw, decodeCtxReader{ctx, zr})
		if cerr := zr.Close(); err == nil {
			err = cerr
		}
		return classify(op, err)
	case DecodeZip:
		zr, err := zip.NewReader(in, size)
		if err != nil {
			return invalid(op, "not a valid ZIP archive: %v", err)
		}
		if len(zr.File) != 1 {
			return invalid(op, "expected a ZIP archive with exactly one file, found %d", len(zr.File))
		}
		m := zr.File[0]
		if !m.Mode().IsRegular() {
			return invalid(op, "the ZIP member %q is not a regular file", m.Name)
		}
		if m.UncompressedSize64 > uint64(limit) {
			return invalid(op, "the ZIP member declares %d bytes, more than the decoded size limit of %d bytes", m.UncompressedSize64, limit)
		}
		if m.Flags&1 != 0 { // encrypted
			if passwordRef == "" {
				return invalid(op, "the ZIP archive is encrypted; choose a password reference, or download without decoding")
			}
			_, _, err := rules.DecryptAES256ZIP(ctx, lw, in, size, passwordRef, s)
			return classify(op, err)
		}
		rc, err := m.Open()
		if err != nil {
			return classify(op, err)
		}
		_, err = io.Copy(lw, decodeCtxReader{ctx, rc})
		if cerr := rc.Close(); err == nil {
			err = cerr
		}
		return classify(op, err)
	}
	return invalid(op, "unknown decode mode %q", mode)
}

// decodeLimitWriter fails (instead of truncating) when output would exceed limit.
type decodeLimitWriter struct {
	w                io.Writer
	remaining, limit int64
}

func (w *decodeLimitWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, invalid("Decode", "decoded size exceeds the limit of %d bytes; nothing was saved", w.limit)
	}
	n, err := w.w.Write(p)
	w.remaining -= int64(n)
	return n, err
}

type decodeCtxReader struct {
	ctx context.Context
	r   io.Reader
}

func (r decodeCtxReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
