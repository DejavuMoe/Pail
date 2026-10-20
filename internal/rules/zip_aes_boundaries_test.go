package rules

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"testing"
)

type countingPasswords struct{ calls int }

func (p *countingPasswords) ResolvePassword(context.Context, string) (string, error) {
	p.calls++
	return string(testZIPPassword), nil
}
func TestAES256ZIPRejectsUnsafeMembers(t *testing.T) {
	good := makeAES(t, 0, []byte("payload"))
	for _, kind := range []string{"path", "symlink", "multiple", "strength", "boundary"} {
		t.Run(kind, func(t *testing.T) {
			archive := append([]byte(nil), good...)
			central := bytes.LastIndex(archive, []byte{0x50, 0x4b, 1, 2})
			switch kind {
			case "path":
				copy(archive[central+46:], []byte("../bad.txt"))
			case "symlink":
				binary.LittleEndian.PutUint16(archive[central+4:], 3<<8|20)
				binary.LittleEndian.PutUint32(archive[central+38:], 0120777<<16)
			case "strength":
				aes := bytes.LastIndex(archive, []byte{1, 0x99, 7, 0})
				archive[aes+8] = 2
			case "boundary":
				binary.LittleEndian.PutUint32(archive[central+20:], uint32(len(archive)+100))
			case "multiple":
				var b bytes.Buffer
				zw := zip.NewWriter(&b)
				for _, name := range []string{"a", "b"} {
					w, err := zw.Create(name)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := w.Write([]byte("data")); err != nil {
						t.Fatal(err)
					}
				}
				if err := zw.Close(); err != nil {
					t.Fatal(err)
				}
				archive = b.Bytes()
			}
			resolver := &countingPasswords{}
			var out bytes.Buffer
			if _, _, err := DecryptAES256ZIP(context.Background(), &out, bytes.NewReader(archive), int64(len(archive)), "ref", resolver); err == nil || out.Len() != 0 || resolver.calls != 0 {
				t.Fatalf("unsafe member reached password or output: err=%v calls=%d bytes=%d", err, resolver.calls, out.Len())
			}
		})
	}
	if _, _, err := DecryptAES256ZIP(context.Background(), io.Discard, bytes.NewReader(nil), -1, "ref", testZIPPassword); err == nil {
		t.Fatal("negative archive size accepted")
	}
}
func TestAESExtraRejectsMalformed(t *testing.T) {
	valid := []byte{1, 0x99, 7, 0, 2, 0, 0x41, 0x45, 3, 8, 0}
	for _, extra := range [][]byte{nil, valid[:3], valid[:10], append(append([]byte(nil), valid...), valid...), append(append([]byte(nil), valid...), 1)} {
		if _, err := validateAESExtra(extra); err == nil {
			t.Fatalf("accepted malformed extra %x", extra)
		}
	}
}
