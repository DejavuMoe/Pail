package app

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/DejavuMoe/Pail/internal/rules"
)

func TestUploadRulesPersistence(t *testing.T) {
	s, dir := newTestServiceDir(t, "", nil)
	if got := s.UploadRules(); len(got) != 0 {
		t.Fatalf("fresh service has rules: %v", got)
	}
	in := []rules.UploadRule{{
		Name: "r1", BucketPattern: "b*", KeyPattern: "*.log",
		Headers:  map[string]string{"cache-control": "no-cache", "Expires": "Wed, 21 Oct 2037 07:28:00 GMT"},
		Metadata: map[string]string{"k": "v"}, StorageClass: "GLACIER",
		SSE:         &rules.Encryption{Type: "aws:kms", KMSKeyID: "alias/x"},
		Compression: &rules.Compression{Method: "gzip", Level: 5},
	}, {Name: "r2", Disabled: true}}
	if err := s.SaveUploadRules(in); err != nil {
		t.Fatal(err)
	}
	if got := s.UploadRules(); !reflect.DeepEqual(got, in) {
		t.Fatalf("got %+v", got)
	}
	// The returned slice is a deep copy.
	got := s.UploadRules()
	got[0].Headers["x"] = "y"
	got[0].SSE.Type = "AES256"
	got[1].Name = "changed"
	if again := s.UploadRules(); !reflect.DeepEqual(again, in) {
		t.Fatalf("mutating the copy changed the service: %+v", again)
	}
	// And the input is copied on save.
	in[0].Metadata["k"] = "mutated"
	if s.UploadRules()[0].Metadata["k"] != "v" {
		t.Fatal("SaveUploadRules kept a reference to the caller's map")
	}
	in[0].Metadata["k"] = "v"

	// Persisted: a new Service on the same directory sees them.
	s2, _ := newTestServiceDir(t, dir, nil)
	if got := s2.UploadRules(); !reflect.DeepEqual(got, in) {
		t.Fatalf("after reload: %+v", got)
	}
	if fi, err := os.Stat(filepath.Join(dir, "upload-rules.json")); err != nil || fi.Size() == 0 {
		t.Fatalf("upload-rules.json: %v", err)
	}
	// Clearing writes an empty list, not null.
	if err := s.SaveUploadRules(nil); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "upload-rules.json"))
	if strings.TrimSpace(string(b)) != "[]" {
		t.Fatalf("file = %q", b)
	}
}

func TestSaveUploadRulesRejectsInvalid(t *testing.T) {
	s := newTestService(t, nil)
	keep := []rules.UploadRule{{Name: "keep", StorageClass: "STANDARD"}}
	if err := s.SaveUploadRules(keep); err != nil {
		t.Fatal(err)
	}
	for name, r := range map[string]rules.UploadRule{
		"x-amz header":      {Name: "a", Headers: map[string]string{"x-amz-acl": "public-read"}},
		"unstorable header": {Name: "b", Headers: map[string]string{"X-Custom": "1"}},
		"bad Expires":       {Name: "c", Headers: map[string]string{"Expires": "tomorrow"}},
		"header newline":    {Name: "d", Headers: map[string]string{"Cache-Control": "a\nb"}},
		"bad compression":   {Name: "e", Compression: &rules.Compression{Method: "rar"}},
		"gzip with pw":      {Name: "f", Compression: &rules.Compression{Method: "gzip", PasswordRef: "p"}},
		"bad sse":           {Name: "g", SSE: &rules.Encryption{Type: "ROT13"}},
		"sse-c no ref":      {Name: "h", SSE: &rules.Encryption{Type: "SSE-C"}},
	} {
		err := s.SaveUploadRules([]rules.UploadRule{r})
		if err == nil || KindOf(err) != ErrInvalid {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if got := s.UploadRules(); !reflect.DeepEqual(got, keep) {
		t.Fatalf("a rejected save changed the rules: %+v", got)
	}
}

func TestPreviewUploadRules(t *testing.T) {
	s := newTestService(t, nil)
	err := s.SaveUploadRules([]rules.UploadRule{
		{Name: "logs", BucketPattern: "prod-*", KeyPattern: "logs/*", Compression: &rules.Compression{Method: "gzip", Level: 6}},
		{Name: "all", Headers: map[string]string{"cache-control": "no-store"}, Metadata: map[string]string{"Owner": "me"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	o, err := s.PreviewUploadRules("prod-eu", "logs/a.log")
	if err != nil {
		t.Fatal(err)
	}
	if o.Compression == nil || o.Compression.Method != "gzip" || o.Headers["Cache-Control"] != "no-store" || o.Metadata["owner"] != "me" {
		t.Fatalf("%+v", o)
	}
	if !reflect.DeepEqual(o.MatchedRules, []string{"logs", "all"}) {
		t.Fatalf("matched %v", o.MatchedRules)
	}
	o, _ = s.PreviewUploadRules("dev", "logs/a.log")
	if o.Compression != nil || !reflect.DeepEqual(o.MatchedRules, []string{"all"}) {
		t.Fatalf("%+v", o)
	}
}

func TestCorruptUploadRulesFileIsAnError(t *testing.T) {
	s, dir := newTestServiceDir(t, "", nil)
	if err := s.SaveUploadRules([]rules.UploadRule{{Name: "x", StorageClass: "STANDARD"}}); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "upload-rules.json")
	os.WriteFile(p, []byte(`[{"name": `), 0o600)
	if _, err := New(Options{ConfigDir: dir, Keyring: NewMemoryKeyring()}); err == nil || !strings.Contains(err.Error(), "upload-rules.json") {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(p); string(b) != `[{"name": ` {
		t.Fatal("the corrupt file was replaced")
	}
}
