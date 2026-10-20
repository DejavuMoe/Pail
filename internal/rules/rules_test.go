package rules

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestEvaluateUnlimitedFirstMatchIsolation(t *testing.T) {
	var rs []UploadRule
	for i := 0; i < 8; i++ {
		rs = append(rs, UploadRule{Name: fmt.Sprint(i), BucketPattern: "photos*", KeyPattern: "*.jpg", Metadata: map[string]string{fmt.Sprint("field", i): "v"}, Headers: map[string]string{"content-type": "image/jpeg"}})
	}
	rs[7].StorageClass = "STANDARD_IA"
	rs[7].SSE = &Encryption{Type: "AES256"}
	rs[7].Compression = &Compression{Method: "gzip", Level: 9}
	rs[7].Headers["content-type"] = "last"
	out, err := Evaluate(rs, "photos-prod", "folder/猫.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if len(out.MatchedRules) != 2 || len(out.Metadata) != 1 || out.StorageClass != "STANDARD_IA" || out.Headers["Content-Type"] != "image/jpeg" {
		t.Fatalf("bad first-match options: %+v", out)
	}
	out.Metadata["field0"] = "changed"
	out.SSE.Type = "changed"
	out.Compression.Level = 0
	if rs[0].Metadata["field0"] != "v" || rs[7].SSE.Type != "AES256" || rs[7].Compression.Level != 9 {
		t.Fatal("output aliases input")
	}
	none, err := Evaluate(rs, "other", "file.jpg")
	if err != nil || len(none.MatchedRules) != 0 {
		t.Fatal("bucket filter failed")
	}
	rs[7].Disabled = true
	out, err = Evaluate(rs, "photos-prod", "file.jpg")
	if err != nil || out.StorageClass != "" {
		t.Fatal("disabled rule applied")
	}
}
func TestMatchS3Masks(t *testing.T) {
	for _, tc := range []struct {
		mask, value string
		want        bool
	}{{"", "any", true}, {"*.jpg", "a/b.jpg", true}, {"a?", "a猫", true}, {"A*", "abc", false}, {"[a]", "a", false}, {"[a]", "[a]", true}, {"*", "line\nkey", true}} {
		if got := Match(tc.mask, tc.value); got != tc.want {
			t.Errorf("Match(%q,%q)=%v", tc.mask, tc.value, got)
		}
	}
}
func TestEvaluateRejectsMalformedPolicy(t *testing.T) {
	for _, r := range []UploadRule{
		{Headers: map[string]string{"Authorization": "secret"}}, {Headers: map[string]string{"Content-Type": "a\r\nx: y"}}, {Headers: map[string]string{"X-Test": "a", "x-test": "b"}}, {Headers: map[string]string{"x-amz-server-side-encryption": "AES256"}}, {Metadata: map[string]string{"A": "1", "a": "2"}}, {SSE: &Encryption{Type: "SSE-C"}}, {SSE: &Encryption{Type: "AES256", KMSKeyID: "x"}}, {Compression: &Compression{Method: "gzip", Level: 10}}, {Compression: &Compression{Method: "unsupported"}},
	} {
		if _, err := Evaluate([]UploadRule{r}, "b", "k"); err == nil {
			t.Errorf("accepted invalid policy: %+v", r)
		}
	}
}

func TestGZIPLevels(t *testing.T) {
	data := []byte(strings.Repeat("中文 content \x00 abc\n", 10000))
	for level := -2; level <= 9; level++ {
		t.Run(fmt.Sprint(level), func(t *testing.T) {
			var out bytes.Buffer
			n, err := CompressGZIP(context.Background(), &out, bytes.NewReader(data), level)
			if err != nil || n != int64(len(data)) {
				t.Fatalf("compress n=%d err=%v", n, err)
			}
			zr, err := gzip.NewReader(&out)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := io.ReadAll(zr)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(data, decoded) {
				t.Fatal("round trip changed data")
			}
			if err := zr.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

var errInjected = errors.New("injected")

type failedWriter struct{}

func (failedWriter) Write([]byte) (int, error) { return 0, errInjected }

type failedReader struct{}

func (failedReader) Read([]byte) (int, error) { return 0, errInjected }

type cancellingReader struct {
	cancel context.CancelFunc
	done   bool
}

func (r *cancellingReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	r.cancel()
	return copy(p, "payload"), nil
}
func TestGZIPFailures(t *testing.T) {
	if _, err := CompressGZIP(context.Background(), io.Discard, strings.NewReader("x"), 10); err == nil {
		t.Fatal("invalid level accepted")
	}
	if _, err := CompressGZIP(context.Background(), failedWriter{}, strings.NewReader("x"), 1); !errors.Is(err, errInjected) {
		t.Fatalf("lost writer error: %v", err)
	}
	if _, err := CompressGZIP(context.Background(), io.Discard, failedReader{}, 1); !errors.Is(err, errInjected) {
		t.Fatalf("lost reader error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	if _, err := CompressGZIP(ctx, &out, strings.NewReader("x"), 1); !errors.Is(err, context.Canceled) || out.Len() != 0 {
		t.Fatal("pre-cancel wrote output")
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	if _, err := CompressGZIP(ctx, io.Discard, &cancellingReader{cancel: cancel}, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("midstream cancellation lost: %v", err)
	}
}

func TestSyncPlansBothDirectionsAndDeletion(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	entry := func(p string, n int64, dt time.Duration) Entry { return Entry{Path: p, Size: n, Modified: now.Add(dt)} }
	local := []Entry{entry("same", 1, 0), entry("new", 1, 0), entry("changed", 2, time.Hour), entry("conflict", 1, 0), entry("excluded", 2, time.Hour)}
	remote := []Entry{entry("same", 1, 0), entry("changed", 1, 0), entry("conflict", 2, time.Hour), entry("only-remote", 1, 0), entry("excluded", 1, 0)}
	before := append([]Entry(nil), local...)
	p, err := PlanSync(local, remote, SyncOptions{Exclude: []string{"excluded"}})
	if err != nil {
		t.Fatal(err)
	}
	if p.Unchanged != 1 || p.Excluded != 1 || p.Retained != 1 || len(p.Actions) != 3 {
		t.Fatalf("bad counts: %+v", p)
	}
	if got := []ActionKind{p.Actions[0].Kind, p.Actions[1].Kind, p.Actions[2].Kind}; !reflect.DeepEqual(got, []ActionKind{ActionUpload, ActionConflict, ActionUpload}) {
		t.Fatal(got)
	}
	if !reflect.DeepEqual(local, before) {
		t.Fatal("mutated manifest")
	}
	p.Actions[0].Source.Size = 999
	if !reflect.DeepEqual(local, before) {
		t.Fatal("plan aliases manifest")
	}
	p, err = PlanSync(local, remote, SyncOptions{Direction: Download, DeleteExtraneous: true, Exclude: []string{"excluded"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 4 || p.Actions[0].Kind != ActionConflict || p.Actions[1].Kind != ActionDownload || p.Actions[2].Kind != ActionDelete || p.Actions[3].Kind != ActionDownload {
		t.Fatalf("reverse plan: %+v", p.Actions)
	}
	for _, a := range p.Actions {
		if a.Target != "local" {
			t.Fatal("wrong deletion/transfer target")
		}
	}
}
func TestSyncChecksumAndTolerance(t *testing.T) {
	now := time.Now()
	a := Entry{Path: "x", Size: 1, Modified: now, Checksum: "aa", ChecksumAlgorithm: "SHA256"}
	b := a
	b.Modified = now.Add(time.Second)
	p, err := PlanSync([]Entry{a}, []Entry{b}, SyncOptions{Compare: Checksum})
	if err != nil || p.Unchanged != 1 {
		t.Fatal("same hash should ignore mtime", p, err)
	}
	b.Checksum = "bb"
	p, _ = PlanSync([]Entry{a}, []Entry{b}, SyncOptions{Compare: Checksum})
	if p.Actions[0].Kind != ActionConflict {
		t.Fatal("overwrote newer target")
	}
	p, _ = PlanSync([]Entry{a}, []Entry{b}, SyncOptions{Compare: Checksum, OverwriteNewer: true})
	if p.Actions[0].Kind != ActionUpload {
		t.Fatal("explicit overwrite ignored")
	}
	b.Checksum = ""
	p, _ = PlanSync([]Entry{a}, []Entry{b}, SyncOptions{Compare: Checksum})
	if p.Actions[0].Kind != ActionConflict {
		t.Fatal("missing hash guessed")
	}
	a.ChecksumAlgorithm = "etag"
	b = a
	p, _ = PlanSync([]Entry{a}, []Entry{b}, SyncOptions{Compare: Checksum})
	if p.Actions[0].Kind != ActionConflict {
		t.Fatal("ETag treated as hash")
	}
	b.Modified = now.Add(time.Second)
	p, _ = PlanSync([]Entry{a}, []Entry{b}, SyncOptions{TimeTolerance: 2 * time.Second})
	if p.Unchanged != 1 {
		t.Fatal("tolerance ignored")
	}
	a.Modified = time.Time{}
	p, _ = PlanSync([]Entry{a}, []Entry{b}, SyncOptions{})
	if p.Actions[0].Kind != ActionConflict {
		t.Fatal("missing mtime guessed")
	}
}
func TestSyncRejectsInvalidInputsAndProtectsExclusions(t *testing.T) {
	for _, path := range []string{"", "/abs", "../x", "a/../x", "a//b", "a/", "C:/x", "a\\b"} {
		if _, err := PlanSync([]Entry{{Path: path}}, nil, SyncOptions{}); err == nil {
			t.Errorf("accepted %q", path)
		}
	}
	if _, err := PlanSync([]Entry{{Path: "x"}, {Path: "x"}}, nil, SyncOptions{}); err == nil {
		t.Fatal("duplicate accepted")
	}
	for _, o := range []SyncOptions{{Direction: "both"}, {Compare: "guess"}, {TimeTolerance: -1}} {
		if _, err := PlanSync(nil, nil, o); err == nil {
			t.Fatal("invalid options accepted")
		}
	}
	p, err := PlanSync(nil, []Entry{{Path: "keep.tmp"}}, SyncOptions{DeleteExtraneous: true, Exclude: []string{"*.tmp"}})
	if err != nil || len(p.Actions) != 0 || p.Excluded != 1 {
		t.Fatal("excluded target deleted", p, err)
	}
}
