package rules

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"testing"
)

// Empty plaintext must still authenticate its 28-byte encryption envelope.
func TestRound5AES256ZIPEmptyMemberAuthenticates(t *testing.T) {
	for _, level := range []int{0, 9} {
		good := makeAES(t, level, nil)
		zr, err := zip.NewReader(bytes.NewReader(good), int64(len(good)))
		if err != nil {
			t.Fatal(err)
		}
		offset, err := zr.File[0].DataOffset()
		if err != nil {
			t.Fatal(err)
		}
		authOffset := offset + int64(zr.File[0].CompressedSize64) - 10
		if level == 0 && zr.File[0].CompressedSize64 != 28 {
			t.Fatal("empty Store lost encryption envelope")
		}
		for _, mode := range []string{"valid", "wrong-password", "bad-auth", "short-envelope", "declared-one", "cancel-at-auth"} {
			t.Run(fmt.Sprintf("level%d/%s", level, mode), func(t *testing.T) {
				archive := append([]byte(nil), good...)
				password := testZIPPassword
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var src io.ReaderAt = bytes.NewReader(archive)
				switch mode {
				case "wrong-password":
					password = "wrong"
				case "bad-auth":
					archive[authOffset] ^= 1
				case "short-envelope":
					central := bytes.LastIndex(archive, []byte{0x50, 0x4b, 1, 2})
					binary.LittleEndian.PutUint32(archive[central+20:], 27)
				case "declared-one":
					central := bytes.LastIndex(archive, []byte{0x50, 0x4b, 1, 2})
					binary.LittleEndian.PutUint32(archive[central+24:], 1)
				case "cancel-at-auth":
					src = round5CancelAt{ReaderAt: src, offset: authOffset, cancel: cancel}
				}
				var out bytes.Buffer
				name, n, err := DecryptAES256ZIP(ctx, &out, src, int64(len(archive)), "ref", password)
				if n != 0 || out.Len() != 0 {
					t.Fatalf("empty emitted plaintext n=%d bytes=%d err=%v", n, out.Len(), err)
				}
				if mode == "valid" {
					if err != nil || name != "文件.txt" {
						t.Fatalf("valid empty: %q %v", name, err)
					}
				} else if err == nil {
					t.Fatal("invalid empty accepted")
				}
				if mode == "cancel-at-auth" && !errors.Is(err, context.Canceled) {
					t.Fatalf("auth cancellation lost: %v", err)
				}
			})
		}
	}
}

type round5CancelAt struct {
	io.ReaderAt
	offset int64
	cancel context.CancelFunc
}

func (r round5CancelAt) ReadAt(p []byte, off int64) (int, error) {
	n, err := r.ReaderAt.ReadAt(p, off)
	if off == r.offset && len(p) == 10 {
		r.cancel()
	}
	return n, err
}

type round5BoundedSink struct {
	limit, written int64
	failure        error
	cancel         context.CancelFunc
}

func (w *round5BoundedSink) Write(p []byte) (int, error) {
	if int64(len(p)) > w.limit-w.written {
		return 0, w.failure
	}
	w.written += int64(len(p))
	if w.cancel != nil {
		w.cancel()
	}
	return len(p), nil
}

// Exercise public streaming decode with substantial expansion and a caller quota.
func TestRound5AES256ZIPExpansionAndCancelBounded(t *testing.T) {
	plain := bytes.Repeat([]byte("highly-compressible-private-data\x00"), 128*1024)
	archive := makeAES(t, 9, plain)
	if len(archive) >= len(plain)/100 {
		t.Fatal("fixture expansion too small")
	}
	for _, mode := range []string{"output-quota", "cancel-output", "declared-zero", "declared-short"} {
		t.Run(mode, func(t *testing.T) {
			data := append([]byte(nil), archive...)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			quotaErr := errors.New("round5 output quota exceeded")
			sink := &round5BoundedSink{limit: 32768 + 17, failure: quotaErr}
			switch mode {
			case "cancel-output":
				sink.limit = int64(len(plain))
				sink.cancel = cancel
			case "declared-zero", "declared-short":
				declared := uint32(0)
				if mode == "declared-short" {
					declared = 17
				}
				central := bytes.LastIndex(data, []byte{0x50, 0x4b, 1, 2})
				binary.LittleEndian.PutUint32(data[central+24:], declared)
			}
			_, n, err := DecryptAES256ZIP(ctx, sink, bytes.NewReader(data), int64(len(data)), "ref", testZIPPassword)
			if err == nil || n != sink.written || n >= int64(len(plain)) || n > sink.limit {
				t.Fatalf("unbounded/accepted: n=%d stored=%d limit=%d err=%v", n, sink.written, sink.limit, err)
			}
			switch mode {
			case "output-quota":
				if !errors.Is(err, quotaErr) {
					t.Fatalf("quota error lost: %v", err)
				}
			case "cancel-output":
				if !errors.Is(err, context.Canceled) || n == 0 {
					t.Fatalf("expected partial private output then cancellation: n=%d err=%v", n, err)
				}
			default:
				if n != 0 {
					t.Fatalf("forged tiny size emitted %d", n)
				}
			}
		})
	}
}

// Reject legacy/weak encryption before consulting the password resolver.
func TestRound5AES256ZIPRejectsLegacyBeforeSecrets(t *testing.T) {
	good := makeAES(t, 0, []byte("data"))
	for _, mode := range []string{"legacy", "aes128", "aes192", "aes-version-zero"} {
		t.Run(mode, func(t *testing.T) {
			data := append([]byte(nil), good...)
			central := bytes.LastIndex(data, []byte{0x50, 0x4b, 1, 2})
			aesExtra := bytes.LastIndex(data, []byte{1, 0x99, 7, 0})
			switch mode {
			case "legacy":
				binary.LittleEndian.PutUint16(data[central+10:], zip.Store)
			case "aes128":
				data[aesExtra+8] = 1
			case "aes192":
				data[aesExtra+8] = 2
			case "aes-version-zero":
				binary.LittleEndian.PutUint16(data[aesExtra+4:], 0)
			}
			resolver := &countingPasswords{}
			var out bytes.Buffer
			_, n, err := DecryptAES256ZIP(context.Background(), &out, bytes.NewReader(data), int64(len(data)), "ref", resolver)
			if err == nil || n != 0 || out.Len() != 0 || resolver.calls != 0 {
				t.Fatalf("unsupported encryption reached secret/output n=%d calls=%d err=%v", n, resolver.calls, err)
			}
		})
	}
}

// A valid HMAC does not authenticate the ZIP size metadata. A late mismatch
// may follow plaintext writes, so callers must discard the private output.
func TestRound5AES256ZIPRejectsLateSizeMismatch(t *testing.T) {
	plain := bytes.Repeat([]byte("x"), 96*1024+17)
	for _, level := range []int{0, 9} {
		good := makeAES(t, level, plain)
		for _, delta := range []int{-1, 1} {
			t.Run(fmt.Sprintf("level%d/delta%d", level, delta), func(t *testing.T) {
				data := append([]byte(nil), good...)
				central := bytes.LastIndex(data, []byte{0x50, 0x4b, 1, 2})
				declared := len(plain) + delta
				binary.LittleEndian.PutUint32(data[central+24:], uint32(declared))
				var out bytes.Buffer
				_, n, err := DecryptAES256ZIP(context.Background(), &out, bytes.NewReader(data), int64(len(data)), "ref", testZIPPassword)
				if err == nil || n <= 0 || n != int64(out.Len()) || n > int64(declared) {
					t.Fatalf("late size mismatch: n=%d written=%d declared=%d err=%v", n, out.Len(), declared, err)
				}
				if delta == 1 && n != int64(len(plain)) {
					t.Fatalf("expected final EOF size check after all plaintext, n=%d", n)
				}
				if !bytes.Equal(out.Bytes(), plain[:n]) {
					t.Fatal("authenticated prefix unexpectedly changed")
				}
			})
		}
	}
}
