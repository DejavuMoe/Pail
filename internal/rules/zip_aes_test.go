package rules

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type testPasswords string

func (p testPasswords) ResolvePassword(context.Context, string) (string, error) {
	return string(p), nil
}

const testZIPPassword = testPasswords("pail-test-only-password")

func makeAES(t *testing.T, level int, data []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	n, err := CompressAES256ZIP(context.Background(), &out, bytes.NewReader(data), "文件.txt", level, "test-ref", testZIPPassword)
	if err != nil || n != int64(len(data)) {
		t.Fatalf("encrypt: count=%d err=%v", n, err)
	}
	return out.Bytes()
}
func TestAES256ZIPLevelsConcurrent(t *testing.T) {
	for level := -2; level <= 9; level++ {
		t.Run(fmt.Sprint(level), func(t *testing.T) {
			t.Parallel()
			for _, data := range [][]byte{nil, bytes.Repeat([]byte("Zip级别并发test\x00"), 900)} {
				archive := makeAES(t, level, data)
				var out bytes.Buffer
				name, n, err := DecryptAES256ZIP(context.Background(), &out, bytes.NewReader(archive), int64(len(archive)), "test-ref", testZIPPassword)
				if err != nil || name != "文件.txt" || n != int64(len(data)) || !bytes.Equal(out.Bytes(), data) {
					t.Fatalf("decrypt: name=%q count=%d err=%v", name, n, err)
				}
				zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
				if err != nil {
					t.Fatal(err)
				}
				method, err := validateAESExtra(zr.File[0].Extra)
				want := uint16(zip.Deflate)
				if level == 0 {
					want = zip.Store
				}
				if err != nil || method != want {
					t.Fatalf("method=%d err=%v", method, err)
				}
			}
		})
	}
}
func TestAES256ZIPRejectsBeforePlaintext(t *testing.T) {
	good := makeAES(t, 0, bytes.Repeat([]byte("authenticated"), 100))
	zr, _ := zip.NewReader(bytes.NewReader(good), int64(len(good)))
	offset, _ := zr.File[0].DataOffset()
	for _, kind := range []string{"password", "ciphertext", "authcode", "short", "malformed-extra", "not-encrypted", "size-limit"} {
		t.Run(kind, func(t *testing.T) {
			archive := append([]byte(nil), good...)
			password := testZIPPassword
			switch kind {
			case "password":
				password = "wrong"
			case "ciphertext":
				archive[offset+18] ^= 1
			case "authcode":
				archive[offset+int64(zr.File[0].CompressedSize64)-1] ^= 1
			case "size-limit":
				at := bytes.LastIndex(archive, []byte{0x50, 0x4b, 1, 2})
				binary.LittleEndian.PutUint32(archive[at+24:], 1)
			case "short":
				archive = archive[:20]
			case "malformed-extra":
				at := bytes.LastIndex(archive, []byte{1, 0x99, 7, 0})
				binary.LittleEndian.PutUint16(archive[at+2:], 1)
			case "not-encrypted":
				var plain bytes.Buffer
				_, err := CompressZIP(context.Background(), &plain, strings.NewReader("plain"), "plain.txt", 0)
				if err != nil {
					t.Fatal(err)
				}
				archive = plain.Bytes()
			}
			var out bytes.Buffer
			_, _, err := DecryptAES256ZIP(context.Background(), &out, bytes.NewReader(archive), int64(len(archive)), "test-ref", password)
			if err == nil || out.Len() != 0 {
				t.Fatalf("rejected before output: err=%v len=%d", err, out.Len())
			}
		})
	}
}
func TestAES256ZIPFailures(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CompressAES256ZIP(ctx, io.Discard, strings.NewReader("x"), "x", 0, "test-ref", testZIPPassword); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: %v", err)
	}
	for _, c := range []struct {
		name  string
		level int
		ref   string
		p     PasswordResolver
	}{{"../x", 0, "ref", testZIPPassword}, {"x", 10, "ref", testZIPPassword}, {"x", 0, "", testZIPPassword}, {"x", 0, "ref", nil}, {"x", 0, "ref", testPasswords("")}} {
		if _, err := CompressAES256ZIP(context.Background(), io.Discard, strings.NewReader("x"), c.name, c.level, c.ref, c.p); err == nil {
			t.Fatal("invalid encryption accepted")
		}
	}
	if err := Validate([]UploadRule{{Compression: &Compression{Method: "gzip", PasswordRef: "ref"}}}); err == nil {
		t.Fatal("gzip encryption accepted")
	}
	archive := makeAES(t, 9, []byte("hello"))
	if _, _, err := DecryptAES256ZIP(ctx, io.Discard, bytes.NewReader(archive), int64(len(archive)), "ref", testZIPPassword); !errors.Is(err, context.Canceled) {
		t.Fatalf("decrypt canceled: %v", err)
	}
}

type patternReader struct{ offset int64 }

func (r *patternReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(r.offset + int64(i))
	}
	r.offset += int64(len(p))
	return len(p), nil
}

type observedReaderAt struct {
	io.ReaderAt
	largest int
	total   int64
}

func (r *observedReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if len(p) > r.largest {
		r.largest = len(p)
	}
	n, err := r.ReaderAt.ReadAt(p, off)
	r.total += int64(n)
	return n, err
}
func TestAES256ZIPLargeStream(t *testing.T) {
	if testing.Short() {
		t.Skip("80 MiB streaming test")
	}
	const size = 80 << 20
	file, err := os.CreateTemp(t.TempDir(), "large-*.zip")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	want := sha256.New()
	n, err := CompressAES256ZIP(context.Background(), file, io.TeeReader(io.LimitReader(&patternReader{}, size), want), "large.bin", 0, "ref", testZIPPassword)
	if err != nil || n != size {
		t.Fatalf("encrypt %d %v", n, err)
	}
	stat, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	source := &observedReaderAt{ReaderAt: file}
	got := sha256.New()
	_, n, err = DecryptAES256ZIP(context.Background(), got, source, stat.Size(), "ref", testZIPPassword)
	if err != nil || n != size || !bytes.Equal(got.Sum(nil), want.Sum(nil)) {
		t.Fatalf("large decrypt %d %v", n, err)
	}
	if source.largest > 1<<20 || source.total < 2*size {
		t.Fatalf("expected bounded two passes: largest=%d total=%d", source.largest, source.total)
	}
}
func TestAES256ZIP7ZipInterop(t *testing.T) {
	tool, err := exec.LookPath("7z")
	if err != nil {
		tool, err = exec.LookPath("7zz")
	}
	if err != nil {
		t.Skip("7-Zip executable unavailable")
	}
	dir := t.TempDir()
	data := bytes.Repeat([]byte("independent AES-256 interoperability\x00\n"), 400)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(tool, args...)
		cmd.Dir = dir
		log, err := os.Create(filepath.Join(dir, "7zip.log"))
		if err != nil {
			t.Fatal(err)
		}
		cmd.Stdout = log
		cmd.Stderr = log
		runErr := cmd.Run()
		log.Close()
		if runErr != nil {
			output, _ := os.ReadFile(filepath.Join(dir, "7zip.log"))
			t.Fatalf("7zip %v: %v\n%s", args, runErr, output)
		}
	}
	archive := makeAES(t, 9, data)
	own := filepath.Join(dir, "pail.zip")
	if err := os.WriteFile(own, archive, 0600); err != nil {
		t.Fatal(err)
	}
	run("x", "-y", "-p"+string(testZIPPassword), "-o"+filepath.Join(dir, "out"), own)
	readBack, err := os.ReadFile(filepath.Join(dir, "out", "文件.txt"))
	if err != nil || !bytes.Equal(readBack, data) {
		t.Fatalf("7zip extraction mismatch: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "payload.bin"), data, 0600); err != nil {
		t.Fatal(err)
	}
	run("a", "-tzip", "-mem=AES256", "-mx=9", "-p"+string(testZIPPassword), "external.zip", "payload.bin")
	file, err := os.Open(filepath.Join(dir, "external.zip"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	name, n, err := DecryptAES256ZIP(context.Background(), &out, file, stat.Size(), "ref", testZIPPassword)
	if err != nil || name != "payload.bin" || n != int64(len(data)) || !bytes.Equal(out.Bytes(), data) {
		t.Fatalf("7zip decrypt name=%q n=%d err=%v", name, n, err)
	}
}

type failingPasswords struct{}

func (failingPasswords) ResolvePassword(context.Context, string) (string, error) {
	return "", errInjected
}

type cancellingReaderAt struct {
	io.ReaderAt
	cancel context.CancelFunc
}

func (r cancellingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	n, err := r.ReaderAt.ReadAt(p, off)
	if len(p) == 32*1024 {
		r.cancel()
	}
	return n, err
}
func TestAES256ZIPIOAndMidstreamCancellation(t *testing.T) {
	for _, level := range []int{0, 9} {
		if _, err := CompressAES256ZIP(context.Background(), failedWriter{}, strings.NewReader("data"), "x", level, "ref", testZIPPassword); err == nil {
			t.Fatal("writer failure lost")
		}
		if _, err := CompressAES256ZIP(context.Background(), io.Discard, failedReader{}, "x", level, "ref", testZIPPassword); !errors.Is(err, errInjected) {
			t.Fatalf("source error lost: %v", err)
		}
	}
	if _, err := CompressAES256ZIP(context.Background(), io.Discard, strings.NewReader("data"), "x", 0, "ref", failingPasswords{}); !errors.Is(err, errInjected) {
		t.Fatalf("resolver error lost: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := CompressAES256ZIP(ctx, io.Discard, &cancellingReader{cancel: cancel}, "x", 9, "ref", testZIPPassword); !errors.Is(err, context.Canceled) {
		t.Fatalf("encrypt cancel lost: %v", err)
	}
	archive := makeAES(t, 0, bytes.Repeat([]byte("x"), 256*1024))
	if _, _, err := DecryptAES256ZIP(context.Background(), failedWriter{}, bytes.NewReader(archive), int64(len(archive)), "ref", testZIPPassword); !errors.Is(err, errInjected) {
		t.Fatalf("decrypt sink error lost: %v", err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	var out bytes.Buffer
	source := cancellingReaderAt{bytes.NewReader(archive), cancel}
	if _, _, err := DecryptAES256ZIP(ctx, &out, source, int64(len(archive)), "ref", testZIPPassword); !errors.Is(err, context.Canceled) || out.Len() != 0 {
		t.Fatalf("auth cancel must expose no output: err=%v n=%d", err, out.Len())
	}
}
