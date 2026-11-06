package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

var benchExcludes = []string{"node_modules/", ".git/", "build/", "*.tmp", "*.log", "thumbs.db"}

// path also walks the ancestors of the file, which a scan that prunes excluded
// directories has already done.
func BenchmarkExcluderPath(b *testing.B) {
	ex := newSyExcluder(benchExcludes)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ex.path("src/app/components/widgets/button.go")
	}
}

func BenchmarkExcluderFile(b *testing.B) {
	ex := newSyExcluder(benchExcludes)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ex.file("src/app/components/widgets/button.go")
	}
}

func BenchmarkHashFile(b *testing.B) {
	p := filepath.Join(b.TempDir(), "f")
	data := make([]byte, 64<<10)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		b.Fatal(err)
	}
	fi, _ := os.Stat(p)
	l := syLocal{Abs: p, Size: fi.Size(), MTime: fi.ModTime()}
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	for i := 0; i < b.N; i++ {
		if _, err := syHashFile(context.Background(), l); err != nil {
			b.Fatal(err)
		}
	}
}

// The pruned scan and the path check agree on which files are excluded.
func TestExcluderFileMatchesPathForFilePatterns(t *testing.T) {
	ex := newSyExcluder(benchExcludes)
	for _, rel := range []string{"a.tmp", "x/y/z.log", "x/thumbs.db", "x/y/ok.go", "readme.md"} {
		_, byPath := ex.path(rel)
		if got := ex.file(rel); got != byPath {
			t.Errorf("%s: file=%v path=%v", rel, got, byPath)
		}
	}
}
