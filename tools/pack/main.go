// Command pack writes a release archive (.zip or .tar.gz) of a staging
// directory with Unix permissions set explicitly, so archives built on Windows
// still carry executable bits: directories and the files named by -exec are
// 0755, everything else 0644. Entries are placed under -prefix; -entries
// limits the archive to the named top-level entries of the directory.
//
//	go run ./tools/pack -C build/stage/x -o out.tar.gz -prefix Pail -exec pail-desktop,pail
package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

func main() {
	dir := flag.String("C", "", "directory to archive")
	out := flag.String("o", "", "output archive (.zip or .tar.gz)")
	prefix := flag.String("prefix", "", "top-level directory inside the archive")
	execs := flag.String("exec", "", "comma-separated base names that are executables")
	only := flag.String("entries", "", "comma-separated top-level entries to archive (default: all)")
	flag.Parse()
	if *dir == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "pack: -C and -o are required")
		os.Exit(2)
	}
	var entries []string
	if *only != "" {
		entries = strings.Split(*only, ",")
	}
	if err := pack(*dir, *out, *prefix, strings.Split(*execs, ","), entries); err != nil {
		fmt.Fprintln(os.Stderr, "pack:", err)
		os.Exit(1)
	}
}

type entry struct {
	name string // slash-separated, under prefix
	src  string
	dir  bool
	mode fs.FileMode
	info fs.FileInfo
}

func collect(dir, prefix string, execs, only []string) ([]entry, error) {
	var es []entry
	seen := map[string]bool{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		if rel == "." && prefix == "" {
			return nil
		}
		if top, _, _ := strings.Cut(filepath.ToSlash(rel), "/"); rel != "." && len(only) > 0 {
			if !slices.Contains(only, top) {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			seen[top] = true
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		name := path.Join(prefix, filepath.ToSlash(rel))
		if rel == "." {
			name = prefix
		}
		e := entry{name: name, src: p, dir: d.IsDir(), mode: 0o644, info: info}
		if e.dir || slices.Contains(execs, d.Name()) {
			e.mode = 0o755
		}
		es = append(es, e)
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, name := range only {
		if !seen[name] {
			return nil, fmt.Errorf("no entry %q in %s", name, dir)
		}
	}
	return es, nil
}

func pack(dir, out, prefix string, execs, only []string) error {
	es, err := collect(dir, prefix, execs, only)
	if err != nil {
		return err
	}
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	switch {
	case strings.HasSuffix(out, ".zip"):
		err = writeZip(f, es)
	case strings.HasSuffix(out, ".tar.gz"):
		err = writeTarGz(f, es)
	default:
		err = fmt.Errorf("unknown archive type: %s", out)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(out)
	}
	return err
}

func writeZip(w io.Writer, es []entry) error {
	zw := zip.NewWriter(w)
	for _, e := range es {
		h, err := zip.FileInfoHeader(e.info)
		if err != nil {
			return err
		}
		h.Name = e.name
		if e.dir {
			h.Name += "/"
			h.SetMode(fs.ModeDir | e.mode)
		} else {
			h.Method = zip.Deflate
			h.SetMode(e.mode)
		}
		fw, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		if !e.dir {
			if err := copyFile(fw, e.src); err != nil {
				return err
			}
		}
	}
	return zw.Close()
}

func writeTarGz(w io.Writer, es []entry) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	for _, e := range es {
		h := &tar.Header{Name: e.name, Mode: int64(e.mode), ModTime: e.info.ModTime(), Format: tar.FormatPAX}
		if e.dir {
			h.Typeflag, h.Name = tar.TypeDir, e.name+"/"
		} else {
			h.Typeflag, h.Size = tar.TypeReg, e.info.Size()
		}
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if !e.dir {
			if err := copyFile(tw, e.src); err != nil {
				return err
			}
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func copyFile(w io.Writer, src string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}
