package app

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DejavuMoe/Pail/internal/rules"
)

// The member of a ZIP made by an upload rule is dated like the local file.
func TestUploadZIPMemberKeepsTheFileDate(t *testing.T) {
	mtime := time.Date(2020, 5, 6, 7, 8, 10, 0, time.UTC)
	for name, comp := range map[string]rules.Compression{
		"zip":     {Method: "zip", Level: -1},
		"aes-zip": {Method: "zip", Level: -1, PasswordRef: "zpw"},
	} {
		comp := comp
		t.Run(name, func(t *testing.T) {
			e := newTrEnv(t)
			if err := e.s.SetSecret("zpw", "pa55word"); err != nil {
				t.Fatal(err)
			}
			if err := e.s.SaveUploadRules([]rules.UploadRule{{Name: "c", Compression: &comp}}); err != nil {
				t.Fatal(err)
			}
			src := filepath.Join(t.TempDir(), "d.txt")
			trWrite(t, src, []byte("dated content\n"))
			if err := os.Chtimes(src, mtime, mtime); err != nil {
				t.Fatal(err)
			}
			trExpect(t, e.upload(UploadRequest{Dest: e.loc("b", ""), Paths: []string{src}}), StatusSucceeded)
			raw, ok := e.srv.Object("b", "d.txt.zip")
			if !ok {
				t.Fatalf("object missing; have %v", e.srv.Buckets())
			}
			zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
			if err != nil || len(zr.File) != 1 {
				t.Fatalf("read archive: %v", err)
			}
			got := zr.File[0].Modified
			if d := got.Sub(mtime); d < -3*time.Second || d > 3*time.Second {
				t.Fatalf("member dated %v, want %v", got, mtime)
			}
		})
	}
}
