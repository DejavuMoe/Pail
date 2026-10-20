package rules

import (
	"compress/flate"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	azip "github.com/DejavuMoe/Pail/internal/rules/internal/zipaes"
)

// PasswordResolver resolves an opaque keyring reference. Implementations must
// never log or serialize returned passwords; strings cannot be reliably zeroed.
type PasswordResolver interface {
	ResolvePassword(context.Context, string) (string, error)
}

func resolveZIPPassword(ctx context.Context, ref string, resolver PasswordResolver) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if ref == "" || strings.ContainsAny(ref, "\r\n\x00") || resolver == nil {
		return "", fmt.Errorf("ZIP password reference and resolver are required")
	}
	password, err := resolver.ResolvePassword(ctx, ref)
	if err != nil {
		return "", fmt.Errorf("resolve ZIP password: %w", err)
	}
	if password == "" {
		return "", fmt.Errorf("ZIP password must not be empty")
	}
	return password, ctx.Err()
}

// CompressAES256ZIP streams one WinZip AE-2 AES-256 member. Levels are the same
// as CompressZIP. Only the opaque password reference belongs in persisted rules.
// The caller owns both streams and must discard all output on error.
func CompressAES256ZIP(ctx context.Context, dst io.Writer, src io.Reader, name string, level int, passwordRef string, resolver PasswordResolver) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := validateZIPName(name); err != nil {
		return 0, err
	}
	if level < -2 || level > 9 {
		return 0, fmt.Errorf("ZIP level must be -2..9")
	}
	password, err := resolveZIPPassword(ctx, passwordRef, resolver)
	if err != nil {
		return 0, err
	}
	zw := azip.NewWriter(contextWriter{ctx, dst})
	method := uint16(azip.Deflate)
	if level == 0 {
		method = azip.Store
	} else {
		zw.RegisterCompressor(azip.Deflate, func(w io.Writer) (io.WriteCloser, error) { return flate.NewWriter(w, level) })
	}
	fh := &azip.FileHeader{Name: name, Method: method, Flags: 0x800}
	fh.SetPassword(password)
	fh.SetEncryptionMethod(azip.AES256Encryption)
	fw, err := zw.CreateHeader(fh)
	if err != nil {
		return 0, errors.Join(err, zw.Close())
	}
	// Force the salt and verifier even for an empty Store member.
	if _, err := fw.Write(nil); err != nil {
		return 0, errors.Join(err, zw.Close())
	}
	n, copyErr := io.CopyBuffer(fw, contextReader{ctx, src}, make([]byte, 32*1024))
	return n, errors.Join(copyErr, zw.Close(), ctx.Err())
}
