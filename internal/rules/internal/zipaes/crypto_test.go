package zip

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func openTestZip(t *testing.T, name string) *Reader {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("Expected %s to open: %v", name, err)
	}
	return r
}

// Test simple password reading.
func TestPasswordReadSimple(t *testing.T) {
	file := "hello-aes.zip"
	var buf bytes.Buffer
	r := openTestZip(t, file)
	if len(r.File) != 1 {
		t.Fatalf("Expected %s to contain one file.", file)
	}
	f := r.File[0]
	if f.Name != "hello.txt" {
		t.Errorf("Expected %s to have a file named hello.txt", file)
	}
	if f.Method != 0 {
		t.Errorf("Expected %s to have its Method set to 0.", file)
	}
	f.SetPassword("golang")
	rc, err := f.Open()
	if err != nil {
		t.Errorf("Expected to open the readcloser: %v.", err)
	}
	_, err = io.Copy(&buf, rc)
	if err != nil {
		t.Errorf("Expected to copy bytes: %v.", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("Hello World\r\n")) {
		t.Errorf("Expected contents were not found.")
	}
}

// Test for multi-file password protected zip.
// Each file can be protected with a different password.
func TestPasswordHelloWorldAes(t *testing.T) {
	file := "world-aes.zip"
	expecting := "helloworld"
	r := openTestZip(t, file)
	if len(r.File) != 2 {
		t.Errorf("Expected %s to contain two files.", file)
	}
	var b bytes.Buffer
	for _, f := range r.File {
		if !f.IsEncrypted() {
			t.Errorf("Expected %s to be encrypted.", f.Name)
		}
		f.SetPassword("golang")
		rc, err := f.Open()
		if err != nil {
			t.Errorf("Expected to open readcloser: %v", err)
		}
		defer rc.Close()
		if _, err := io.Copy(&b, rc); err != nil {
			t.Errorf("Expected to copy bytes to buffer: %v", err)
		}
	}
	if !bytes.Equal([]byte(expecting), b.Bytes()) {
		t.Errorf("Expected ending content to be %s instead of %s", expecting, b.Bytes())
	}
}

// Test for password protected file that is larger than a single
// AES block size to check CTR implementation.
func TestPasswordMacbethAct1(t *testing.T) {
	file := "macbeth-act1.zip"
	expecting := "Exeunt"
	var b bytes.Buffer
	r := openTestZip(t, file)
	for _, f := range r.File {
		if !f.IsEncrypted() {
			t.Errorf("Expected %s to be encrypted.", f.Name)
		}
		f.SetPassword("golang")
		rc, err := f.Open()
		if err != nil {
			t.Errorf("Expected to open readcloser: %v", err)
		}
		defer rc.Close()
		if _, err := io.Copy(&b, rc); err != nil {
			t.Errorf("Expected to copy bytes to buffer: %v", err)
		}
	}
	if !bytes.Contains(b.Bytes(), []byte(expecting)) {
		t.Errorf("Expected to find %s in the buffer %v", expecting, b.Bytes())
	}
}

// Change to AE-1 and change CRC value to fail check.
// Must be != 0 due to zip package already skipping if == 0.
func returnAE1BadCRC() (io.ReaderAt, int64) {
	return messWith("hello-aes.zip", func(b []byte) {
		// Change version to AE-1(1)
		b[0x2B] = 1 // file
		b[0xBA] = 1 // TOC
		// Change CRC to bad value
		b[0x11]++ // file
		b[0x6B]++ // TOC
	})
}

// Test for AE-1 Corrupt CRC
func TestPasswordAE1BadCRC(t *testing.T) {
	buf := new(bytes.Buffer)
	file, s := returnAE1BadCRC()
	r, err := NewReader(file, s)
	if err != nil {
		t.Errorf("Expected hello-aes.zip to open: %v", err)
	}
	for _, f := range r.File {
		if !f.IsEncrypted() {
			t.Errorf("Expected zip to be encrypted")
		}
		f.SetPassword("golang")
		rc, err := f.Open()
		if err != nil {
			t.Errorf("Expected the readcloser to open.")
		}
		defer rc.Close()
		if _, err := io.Copy(buf, rc); err != ErrChecksum {
			t.Errorf("Expected the checksum to fail")
		}
	}
}

// Corrupt the last byte of ciphertext to fail authentication
func returnTamperedData() (io.ReaderAt, int64) {
	return messWith("hello-aes.zip", func(b []byte) {
		b[0x50]++
	})
}

// Test for tampered file data payload.
// Pail: eager authentication now fails during Open, before plaintext exists.
func TestPasswordTamperedData(t *testing.T) {
	file, s := returnTamperedData()
	r, err := NewReader(file, s)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range r.File {
		if !f.IsEncrypted() {
			t.Fatal("expected encrypted member")
		}
		f.SetPassword("golang")
		rc, err := f.Open()
		if err != ErrAuthentication || rc != nil {
			t.Fatalf("expected authentication failure before reader: %v", err)
		}
	}
}

func TestPasswordWrong(t *testing.T) {
	r := openTestZip(t, "hello-aes.zip")
	f := r.File[0]
	f.SetPassword("not-golang")
	if rc, err := f.Open(); err != ErrPassword || rc != nil {
		t.Fatalf("expected ErrPassword before reader: %v", err)
	}
}

func TestPasswordWriteSimple(t *testing.T) {
	contents := []byte("Hello World")
	conLen := len(contents)

	for _, method := range []uint16{Store, Deflate} {
		raw := new(bytes.Buffer)
		zipw := NewWriter(raw)
		fh := &FileHeader{Name: "hello.txt", Method: method}
		fh.SetPassword("golang")
		fh.SetEncryptionMethod(AES256Encryption)
		w, err := zipw.CreateHeader(fh)
		if err != nil {
			t.Fatalf("Expected to create a new FileHeader: %v", err)
		}
		n, err := io.Copy(w, bytes.NewReader(contents))
		if err != nil || n != int64(conLen) {
			t.Errorf("Expected to write the full contents to the writer.")
		}
		if err := zipw.Close(); err != nil {
			t.Fatal(err)
		}

		// Read the zip
		buf := new(bytes.Buffer)
		zipr, err := NewReader(bytes.NewReader(raw.Bytes()), int64(raw.Len()))
		if err != nil {
			t.Fatalf("Expected to open a new zip reader: %v", err)
		}
		nn := len(zipr.File)
		if nn != 1 {
			t.Fatalf("Expected to have one file in the zip archive, but has %d files", nn)
		}
		z := zipr.File[0]
		if z.Method != method || z.aesStrength != aes256Strength || z.ae != 2 {
			t.Errorf("method=%d strength=%d ae=%d, want %d/3/2", z.Method, z.aesStrength, z.ae, method)
		}
		z.SetPassword("golang")
		rr, err := z.Open()
		if err != nil {
			t.Fatalf("Expected to open the readcloser: %v", err)
		}
		n, err = io.Copy(buf, rr)
		if err != nil {
			t.Errorf("Expected to write to temporary buffer: %v", err)
		}
		if n != int64(conLen) {
			t.Errorf("Expected to copy %d bytes to temp buffer, but copied %d bytes instead", conLen, n)
		}
		if !bytes.Equal(contents, buf.Bytes()) {
			t.Errorf("Expected the unzipped contents to equal '%s', but was '%s' instead", contents, buf.Bytes())
		}
	}
}

// A password without AES-256 (the removed ZipCrypto/AES-128/AES-192 modes) is refused.
func TestWriteRejectsNonAES256(t *testing.T) {
	for _, enc := range []EncryptionMethod{0, 1, 2, 3} {
		fh := &FileHeader{Name: "hello.txt", Method: Deflate}
		fh.SetPassword("golang")
		fh.SetEncryptionMethod(enc)
		if _, err := NewWriter(io.Discard).CreateHeader(fh); err == nil {
			t.Errorf("encryption method %d: expected an error", enc)
		}
	}
}

// An encrypted member without the WinZip AES extra field (legacy ZipCrypto) is refused.
func TestReadRejectsZipCrypto(t *testing.T) {
	r := openTestZip(t, "go-with-datadesc-sig.zip")
	f := r.File[0]
	f.Flags |= 0x1
	f.SetPassword("golang")
	if rc, err := f.Open(); err != ErrAlgorithm || rc != nil {
		t.Fatalf("expected ErrAlgorithm: %v", err)
	}
}

// AES-128 and AES-192 members are refused when reading.
func TestReadRejectsWeakerAES(t *testing.T) {
	for _, strength := range []byte{1, 2} {
		file, s := messWith("hello-aes.zip", func(b []byte) {
			b[0x2F] = strength // file header strength
			b[0xBE] = strength // TOC strength
		})
		r, err := NewReader(file, s)
		if err != nil {
			t.Fatal(err)
		}
		f := r.File[0]
		if f.aesStrength != strength {
			t.Fatalf("strength=%d, want %d", f.aesStrength, strength)
		}
		f.SetPassword("golang")
		if rc, err := f.Open(); err != ErrDecryption || rc != nil {
			t.Errorf("strength %d: expected ErrDecryption: %v", strength, err)
		}
	}
}
