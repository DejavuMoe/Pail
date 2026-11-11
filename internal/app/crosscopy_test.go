package app

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/DejavuMoe/Pail/internal/s3test"
)

// crossEnv is newS3Env plus a second account on a second in-memory server.
func crossEnv(t *testing.T) (*s3Env, *s3test.Server, Account) {
	e := newS3Env(t)
	srv2 := s3test.New()
	t.Cleanup(srv2.Close)
	acc2, err := e.s.SaveAccount(staticAccount("other", srv2.URL), &Secrets{SecretAccessKey: "sk"})
	if err != nil {
		t.Fatal(err)
	}
	return e, srv2, acc2
}

func TestCopyBetweenAccounts(t *testing.T) {
	e, srv2, acc2 := crossEnv(t)
	e.srv.CreateBucket("src", false)
	srv2.CreateBucket("dst", false)
	e.srv.PutObject("src", "a/one.txt", []byte("first"), "text/plain", map[string]string{"owner": "me"})
	e.srv.PutObject("src", "a/sub/two.bin", bytes.Repeat([]byte("x"), 7<<20), "application/octet-stream", nil)
	ctx := context.Background()
	if _, err := e.sdk().PutObjectTagging(ctx, &s3.PutObjectTaggingInput{Bucket: aws.String("src"), Key: aws.String("a/one.txt"),
		Tagging: &types.Tagging{TagSet: []types.Tag{{Key: aws.String("team"), Value: aws.String("x y")}}}}); err != nil {
		t.Fatal(err)
	}
	dest := Location{AccountID: acc2.ID, Bucket: "dst", Prefix: "in/"}
	id, err := e.s.Copy(Selection{Location: e.loc("src", ""), Keys: []string{"a/"}}, dest, CopyOptions{Move: true})
	if err != nil {
		t.Fatal(err)
	}
	bkOK(t, e, id)
	if d, ok := srv2.Object("dst", "in/a/one.txt"); !ok || string(d) != "first" {
		t.Fatalf("one.txt = %q %v", d, ok)
	}
	if d, ok := srv2.Object("dst", "in/a/sub/two.bin"); !ok || len(d) != 7<<20 {
		t.Fatalf("two.bin has %d bytes", len(d))
	}
	if _, ok := e.srv.Object("src", "a/one.txt"); ok {
		t.Fatal("move kept the source")
	}
	c2, err := e.s.storageClient(ctx, acc2.ID)
	if err != nil {
		t.Fatal(err)
	}
	h, err := c2.S3().HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String("dst"), Key: aws.String("in/a/one.txt")})
	if err != nil || aws.ToString(h.ContentType) != "text/plain" || h.Metadata["owner"] != "me" {
		t.Fatalf("attributes not copied: %+v %v", h, err)
	}
	tags, err := c2.S3().GetObjectTagging(ctx, &s3.GetObjectTaggingInput{Bucket: aws.String("dst"), Key: aws.String("in/a/one.txt")})
	if err != nil || len(tags.TagSet) != 1 || aws.ToString(tags.TagSet[0].Value) != "x y" {
		t.Fatalf("tags not copied: %+v %v", tags, err)
	}
}

func TestCopyBetweenAccountsFailsWithoutPartialObject(t *testing.T) {
	e, srv2, acc2 := crossEnv(t)
	e.srv.CreateBucket("src", false)
	srv2.CreateBucket("dst", false)
	e.srv.PutObject("src", "f.txt", []byte("0123456789"), "text/plain", nil)
	// The source connection breaks while the object is read.
	e.srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/f.txt") {
			return &s3test.Fault{DropConnection: true}
		}
		return nil
	})
	id, err := e.s.Copy(Selection{Location: e.loc("src", ""), Keys: []string{"f.txt"}}, Location{AccountID: acc2.ID, Bucket: "dst"}, CopyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if tk := bkWait(t, e, id); tk.Status == StatusSucceeded {
		t.Fatal("a short body was accepted")
	}
	if _, ok := srv2.Object("dst", "f.txt"); ok {
		t.Fatal("a partial object was committed")
	}
	if _, err := e.s.Copy(Selection{Location: e.loc("src", ""), Keys: []string{"f.txt"}}, Location{AccountID: "nobody", Bucket: "dst"}, CopyOptions{}); !IsKind(err, ErrInvalid) {
		t.Fatalf("unknown destination account: %v", err)
	}
}

func TestExactReader(t *testing.T) {
	for _, c := range []struct {
		body string
		size int64
		ok   bool
	}{{"abc", 3, true}, {"ab", 3, false}, {"abcd", 3, false}, {"", 0, true}} {
		_, err := io.ReadAll(&exactReader{r: strings.NewReader(c.body), left: c.size})
		if (err == nil) != c.ok {
			t.Errorf("%q/%d: %v", c.body, c.size, err)
		}
	}
}
