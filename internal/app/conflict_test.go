package app

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DejavuMoe/Pail/internal/s3test"
	"github.com/DejavuMoe/Pail/internal/storage"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

func TestNumberedName(t *testing.T) {
	cases := map[string]string{
		"report.pdf":    "report (2).pdf",
		"a.tar.gz":      "a.tar (2).gz",
		"README":        "README (2)",
		".env":          ".env (2)",
		"dir/x.txt":     "dir/x (2).txt",
		"d1/d2/.hide":   "d1/d2/.hide (2)",
		"name.":         "name (2).",
		"unicode 文件.md": "unicode 文件 (2).md",
	}
	for in, want := range cases {
		if got := numberedKey(in, 2); got != want {
			t.Errorf("numberedKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveRemoteConflict(t *testing.T) {
	e := newS3Env(t)
	e.srv.CreateBucket("b", false)
	e.srv.PutObject("b", "dir/f.txt", []byte("12345"), "text/plain", nil)
	e.srv.PutObject("b", "dir/f (1).txt", []byte("x"), "text/plain", nil)
	c, err := e.s.storageClient(testCtx(t), e.acc.ID)
	if err != nil {
		t.Fatal(err)
	}
	ctx := testCtx(t)
	past, future := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)

	cases := []struct {
		name    string
		key     string
		policy  ConflictPolicy
		size    int64
		mod     time.Time
		wantKey string
		skip    bool
		create  bool
		ifMatch bool
	}{
		{"missing+skip", "dir/new.txt", ConflictSkip, 1, past, "dir/new.txt", false, true, false},
		{"missing+rename", "dir/new.txt", ConflictRename, 1, past, "dir/new.txt", false, true, false},
		{"exists+skip", "dir/f.txt", ConflictSkip, 5, past, "dir/f.txt", true, false, false},
		{"exists+overwrite", "dir/f.txt", ConflictOverwrite, 5, past, "dir/f.txt", false, false, false},
		{"exists+rename skips taken (1)", "dir/f.txt", ConflictRename, 5, past, "dir/f (2).txt", false, true, false},
		{"exists+newer older src", "dir/f.txt", ConflictNewer, 5, past, "dir/f.txt", true, false, false},
		{"exists+newer newer src", "dir/f.txt", ConflictNewer, 5, future, "dir/f.txt", false, false, true},
		{"exists+same size", "dir/f.txt", ConflictDifferentSize, 5, past, "dir/f.txt", true, false, false},
		{"exists+different size", "dir/f.txt", ConflictDifferentSize, 6, past, "dir/f.txt", false, false, true},
	}
	for _, tc := range cases {
		d, err := resolveRemoteConflict(ctx, c, "b", tc.key, tc.policy, tc.size, tc.mod)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if d.Key != tc.wantKey || d.Skip != tc.skip || d.CreateOnly != tc.create || (d.IfMatch != "") != tc.ifMatch {
			t.Errorf("%s: got %+v", tc.name, d)
		}
		if d.Skip && d.Reason == "" {
			t.Errorf("%s: skip without reason", tc.name)
		}
	}
	if _, err := resolveRemoteConflict(ctx, c, "b", "dir/f.txt", "bogus", 1, past); !IsKind(err, ErrInvalid) {
		t.Fatalf("bogus policy: %v", err)
	}
}

func TestWriteConditionalFallsBackWhenHeaderRejected(t *testing.T) {
	e := newS3Env(t)
	rejected := fxOpErr("PutObject", &smithy.GenericAPIError{Code: "NotImplemented", Message: "A header you provided implies functionality that is not implemented: If-None-Match"})
	var calls [][2]string
	write := func(inm, im string) error {
		calls = append(calls, [2]string{inm, im})
		if inm != "" || im != "" {
			return rejected
		}
		return nil
	}
	if err := e.s.writeConditional(e.acc.ID, conflictDecision{Key: "k", CreateOnly: true}, write); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[0] != [2]string{"*", ""} || calls[1] != [2]string{"", ""} {
		t.Fatalf("calls %v", calls)
	}
	if !e.s.condWritesUnsupported(e.acc.ID) {
		t.Fatal("capability not recorded")
	}
	// Known-unsupported: no conditional attempt at all.
	calls = nil
	if err := e.s.writeConditional(e.acc.ID, conflictDecision{Key: "k", IfMatch: `"e"`}, write); err != nil || len(calls) != 1 || calls[0] != [2]string{"", ""} {
		t.Fatalf("second write calls %v err %v", calls, err)
	}
	// Saving the account clears the flag.
	if _, err := e.s.SaveAccount(e.acc, nil); err != nil {
		t.Fatal(err)
	}
	if e.s.condWritesUnsupported(e.acc.ID) {
		t.Fatal("flag survived account save")
	}
}

func TestWriteConditionalKeepsRealConflicts(t *testing.T) {
	e := newS3Env(t)
	precondition := &smithy.GenericAPIError{Code: "PreconditionFailed", Message: "At least one of the pre-conditions you specified did not hold"}
	n := 0
	err := e.s.writeConditional(e.acc.ID, conflictDecision{CreateOnly: true}, func(string, string) error { n++; return precondition })
	if !errors.Is(err, error(precondition)) || n != 1 || e.s.condWritesUnsupported(e.acc.ID) {
		t.Fatalf("412 must surface once and not mark the provider: n=%d err=%v", n, err)
	}
}

// The in-memory server honours If-None-Match, so a create-only write against
// an existing key is a real conflict end to end and is not mistaken for an
// unsupported header.
func TestCreateOnlyAgainstServer(t *testing.T) {
	e := newS3Env(t)
	e.srv.CreateBucket("b", false)
	e.srv.PutObject("b", "k", []byte("old"), "text/plain", nil)
	c, _ := e.s.storageClient(testCtx(t), e.acc.ID)
	put := func(inm, im string) error {
		in := &s3.PutObjectInput{Bucket: aws.String("b"), Key: aws.String("k"), Body: strings.NewReader("new")}
		if inm != "" {
			in.IfNoneMatch = aws.String(inm)
		}
		_, err := c.S3().PutObject(testCtx(t), in)
		return classify("PutObject", err)
	}
	err := e.s.writeConditional(e.acc.ID, conflictDecision{Key: "k", CreateOnly: true}, put)
	if !IsKind(err, ErrConflict) || e.s.condWritesUnsupported(e.acc.ID) {
		t.Fatalf("want Conflict without marking provider, got %v", err)
	}
	if data, _ := e.srv.Object("b", "k"); string(data) != "old" {
		t.Fatalf("object overwritten: %q", data)
	}
	e.srv.ResetRequests()
	e.srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.Header.Get("If-None-Match") != "" {
			return &s3test.Fault{Status: 501, Code: "NotImplemented", Message: "If-None-Match not implemented"}
		}
		return nil
	})
	if err := e.s.writeConditional(e.acc.ID, conflictDecision{Key: "k", CreateOnly: true}, put); err != nil {
		t.Fatalf("fallback write: %v", err)
	}
	if !e.s.condWritesUnsupported(e.acc.ID) {
		t.Fatal("NotImplemented not recorded")
	}
}

func fxOpErr(op string, err error) error {
	return &smithy.OperationError{ServiceID: "S3", OperationName: op, Err: err}
}

// Only a failed operation that carried the conditional header may mark the
// account as not supporting conditional writes.
func TestFxCondHeaderRejectedScope(t *testing.T) {
	notImpl := &smithy.GenericAPIError{Code: "NotImplemented", Message: "not implemented"}
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"PutObject NotImplemented", fxOpErr("PutObject", notImpl), true},
		{"PutObject XNotImplemented", fxOpErr("PutObject", &smithy.GenericAPIError{Code: "XNotImplemented", Message: "x"}), true},
		{"PutObject UnsupportedOperation", fxOpErr("PutObject", &smithy.GenericAPIError{Code: "UnsupportedOperation"}), true},
		{"PutObject bare 501", fxOpErr("PutObject", apiErr(501, "", "", "")), true},
		{"CompleteMultipartUpload NotImplemented", fxOpErr("CompleteMultipartUpload", notImpl), true},
		{"CopyObject NotImplemented", fxOpErr("CopyObject", notImpl), true},
		{"PutObject InvalidArgument naming the header", fxOpErr("PutObject", &smithy.GenericAPIError{Code: "InvalidArgument", Message: "Unsupported conditional header If-None-Match"}), true},
		{"PutObject InvalidRequest unrelated", fxOpErr("PutObject", &smithy.GenericAPIError{Code: "InvalidRequest", Message: "bad storage class"}), false},
		{"PutObject 403", fxOpErr("PutObject", apiErr(403, "AccessDenied", "no", "")), false},
		{"PutObject 412", fxOpErr("PutObject", apiErr(412, "PreconditionFailed", "", "")), false},
		{"CreateMultipartUpload NotImplemented", fxOpErr("CreateMultipartUpload", notImpl), false},
		{"CreateMultipartUpload bare 501", fxOpErr("CreateMultipartUpload", apiErr(501, "", "", "")), false},
		{"UploadPartCopy NotImplemented", fxOpErr("UploadPartCopy", notImpl), false},
		{"PutObjectTagging NotImplemented", fxOpErr("PutObjectTagging", notImpl), false},
		{"GetObjectTagging NotImplemented", fxOpErr("GetObjectTagging", notImpl), false},
		{"no operation at all", notImpl, false},
	}
	for _, tc := range cases {
		if got := condHeaderRejected(tc.err); got != tc.want {
			t.Errorf("%s: condHeaderRejected = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// NotImplemented on CreateMultipartUpload (a create-only multipart copy) is
// not about the conditional header and must not mark the account.
func TestFxCreateMultipartNotImplementedDoesNotMarkAccount(t *testing.T) {
	e := newS3Env(t)
	bkSeed(e, "bkt", false, "src/a.txt")
	e.srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.Method == http.MethodPost && r.URL.Query().Has("uploads") {
			return &s3test.Fault{Status: 501, Code: "NotImplemented", Message: "multipart is not implemented"}
		}
		return nil
	})
	id, err := e.s.Copy(Selection{Location: e.loc("bkt", ""), Keys: []string{"src/a.txt"}}, e.loc("bkt", "dst/"), CopyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	tk := bkWait(t, e, id)
	if tk.Status != StatusPartiallyFailed {
		t.Fatalf("status %v items %+v", tk.Status, e.s.TaskItems(id))
	}
	if e.s.condWritesUnsupported(e.acc.ID) {
		t.Fatal("NotImplemented on CreateMultipartUpload marked the account as lacking conditional writes")
	}
}

func TestFxPutObject501MarksAccount(t *testing.T) {
	for _, f := range []s3test.Fault{
		{Status: 501},
		{Status: 501, Code: "XNotImplemented", Message: "conditional writes"},
	} {
		t.Run(f.Code, func(t *testing.T) {
			e := newS3Env(t)
			e.srv.CreateBucket("b", false)
			c, _ := e.s.storageClient(testCtx(t), e.acc.ID)
			fault := f
			e.srv.Inject(func(r *http.Request) *s3test.Fault {
				if r.Method == http.MethodPut && r.Header.Get("If-None-Match") != "" {
					return &fault
				}
				return nil
			})
			put := func(inm, im string) error {
				in := &s3.PutObjectInput{Bucket: aws.String("b"), Key: aws.String("k"), Body: strings.NewReader("new")}
				if inm != "" {
					in.IfNoneMatch = aws.String(inm)
				}
				_, err := c.S3().PutObject(testCtx(t), in)
				return classify("PutObject", err)
			}
			if err := e.s.writeConditional(e.acc.ID, conflictDecision{Key: "k", CreateOnly: true}, put); err != nil {
				t.Fatalf("fallback write: %v", err)
			}
			if !e.s.condWritesUnsupported(e.acc.ID) {
				t.Fatal("PutObject rejection not recorded")
			}
		})
	}
}

// The same through storage.Client.Upload (the SDK transfer manager that the
// upload tasks use): the operation name must survive its error wrapping.
func TestFxUploadManager501MarksAccount(t *testing.T) {
	e := newS3Env(t)
	e.srv.CreateBucket("b", false)
	c, _ := e.s.storageClient(testCtx(t), e.acc.ID)
	e.srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.Method == http.MethodPut && r.Header.Get("If-None-Match") != "" {
			return &s3test.Fault{Status: 501, Code: "NotImplemented", Message: "conditional writes"}
		}
		return nil
	})
	put := func(inm, im string) error {
		in := &s3.PutObjectInput{Bucket: aws.String("b"), Key: aws.String("k"), Body: strings.NewReader("new")}
		if inm != "" {
			in.IfNoneMatch = aws.String(inm)
		}
		_, err := c.Upload(testCtx(t), in, storage.TransferOptions{})
		return err
	}
	if err := e.s.writeConditional(e.acc.ID, conflictDecision{Key: "k", CreateOnly: true}, put); err != nil {
		t.Fatalf("fallback write: %v", err)
	}
	if !e.s.condWritesUnsupported(e.acc.ID) {
		t.Fatal("rejection through the upload manager was not recorded")
	}
}
