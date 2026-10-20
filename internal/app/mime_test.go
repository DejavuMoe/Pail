package app

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSaveMIMETypesNormalises(t *testing.T) {
	s, dir := newTestServiceDir(t, "", nil)
	in := map[string]string{
		"TXT":    "Text/Plain; charset=UTF-8",
		".Foo":   "application/x-foo",
		" .bar ": "application/x-bar",
	}
	if err := s.SaveMIMETypes(in); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{".txt": "text/plain; charset=UTF-8", ".foo": "application/x-foo", ".bar": "application/x-bar"}
	if got := s.MIMETypes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
	// Readers get a copy.
	got := s.MIMETypes()
	got[".zzz"] = "x/y"
	if _, ok := s.MIMETypes()[".zzz"]; ok {
		t.Fatal("MIMETypes returned the internal map")
	}
	// Persisted and reloaded.
	s2, _ := newTestServiceDir(t, dir, nil)
	if got := s2.MIMETypes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("after reload: %v", got)
	}
	// An empty table is allowed and clears the file content.
	if err := s.SaveMIMETypes(nil); err != nil {
		t.Fatal(err)
	}
	if len(s.MIMETypes()) != 0 {
		t.Fatal("not cleared")
	}
}

func TestSaveMIMETypesRejects(t *testing.T) {
	s := newTestService(t, nil)
	if err := s.SaveMIMETypes(map[string]string{".keep": "a/b"}); err != nil {
		t.Fatal(err)
	}
	for name, m := range map[string]map[string]string{
		"wildcard subtype": {".a": "image/*"},
		"wildcard all":     {".a": "*/*"},
		"no slash":         {".a": "plain"},
		"garbage":          {".a": "text/plain; charset"},
		"empty value":      {".a": ""},
		"control char":     {".a": "text/plain\r\nX: y"},
		"duplicate":        {"TXT": "a/b", ".txt": "a/c"},
		"inner dot":        {".tar.gz": "a/b"},
		"empty ext":        {"": "a/b"},
		"dot only":         {".": "a/b"},
		"slash in ext":     {".a/b": "a/b"},
		"space in ext":     {".a b": "a/b"},
	} {
		err := s.SaveMIMETypes(m)
		if err == nil || KindOf(err) != ErrInvalid {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if got := s.MIMETypes(); !reflect.DeepEqual(got, map[string]string{".keep": "a/b"}) {
		t.Fatalf("a rejected save changed the table: %v", got)
	}
}

func TestCorruptMIMEFileIsAnError(t *testing.T) {
	s, dir := newTestServiceDir(t, "", nil)
	if err := s.SaveMIMETypes(map[string]string{".a": "a/b"}); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "mime-types.json")
	os.WriteFile(p, []byte(`{"txt": `), 0o600)
	if _, err := New(Options{ConfigDir: dir, Keyring: NewMemoryKeyring()}); err == nil || !strings.Contains(err.Error(), "mime-types.json") {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(p); string(b) != `{"txt": ` {
		t.Fatal("the corrupt file was replaced")
	}
}

func TestContentTypeFor(t *testing.T) {
	s := newTestService(t, nil)
	if err := s.SaveMIMETypes(map[string]string{".txt": "text/x-mine", ".zzqq9": "application/x-zzqq"}); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		"a.txt":                 "text/x-mine",
		"dir/A.TXT":             "text/x-mine",
		"x.zzqq9":               "application/x-zzqq",
		"p.png":                 "image/png",
		"noext":                 "application/octet-stream",
		"trailingdot.":          "application/octet-stream",
		"weird.unknownext1":     "application/octet-stream",
		".hidden":               "application/octet-stream",
		filepath.Join("d", "f"): "application/octet-stream",
	} {
		if got := s.ContentTypeFor(path); got != want {
			t.Errorf("ContentTypeFor(%q) = %q, want %q", path, got, want)
		}
	}
}
