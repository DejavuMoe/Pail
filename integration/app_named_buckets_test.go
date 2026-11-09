package integration_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/DejavuMoe/Pail/internal/app"
)

// TestAppNamedBuckets checks named buckets against a policy the server
// evaluates itself: the test bucket's policy lets anonymous requests list the
// folder dir/ (s3:ListBucket with an s3:prefix condition) and read below it,
// and nothing else. Anonymous requests may not list the buckets of the server.
func TestAppNamedBuckets(t *testing.T) {
	a := newApp(t)
	name := a.bucket(t, false)
	a.put(t, name, "dir/a.txt", []byte("inside"))
	a.put(t, name, "dir/sub/b.txt", []byte("deeper"))
	a.put(t, name, "other/c.txt", []byte("outside"))
	policy := fmt.Sprintf(`{"Version":"2012-10-17","Statement":[`+
		`{"Effect":"Allow","Principal":{"AWS":["*"]},"Action":["s3:ListBucket"],"Resource":["arn:aws:s3:::%[1]s"],"Condition":{"StringLike":{"s3:prefix":["dir/*"]}}},`+
		`{"Effect":"Allow","Principal":{"AWS":["*"]},"Action":["s3:GetObject"],"Resource":["arn:aws:s3:::%[1]s/dir/*"]}]}`, name)
	if err := a.svc.PutBucketConfig(a.ctx, a.acct.ID, name, app.ConfigPolicy, policy); err != nil {
		t.Fatalf("bucket policy: %v", err)
	}

	anon := app.Account{Name: "restricted", Provider: app.ProviderS3, Endpoint: a.cfg.Endpoint, Region: "us-east-1", PathStyle: true,
		Credentials: app.CredAnonymous}
	// Without named buckets the account cannot even be tested.
	if err := a.svc.TestAccount(a.ctx, anon, nil); !app.IsKind(err, app.ErrAccessDenied) {
		t.Fatalf("TestAccount without named buckets: %v (want AccessDenied)", err)
	}
	// The bucket root is outside the policy, so naming the bucket alone fails too.
	root := anon
	root.Buckets = []app.AccountBucket{{Name: name}}
	if err := a.svc.TestAccount(a.ctx, root, nil); !app.IsKind(err, app.ErrAccessDenied) {
		t.Fatalf("TestAccount at the bucket root: %v (want AccessDenied)", err)
	}
	anon.Buckets = []app.AccountBucket{{Name: name, Prefix: "dir/"}}
	if err := a.svc.TestAccount(a.ctx, anon, nil); err != nil {
		t.Fatalf("TestAccount with the named folder: %v", err)
	}
	saved, err := a.svc.SaveAccount(anon, nil)
	if err != nil {
		t.Fatal(err)
	}

	bs, err := a.svc.Buckets(a.ctx, saved.ID)
	if err != nil || len(bs) != 1 || bs[0].Name != name || bs[0].Prefix != "dir/" {
		t.Fatalf("Buckets = %+v, %v", bs, err)
	}
	at := func(prefix string) app.Location {
		return app.Location{AccountID: saved.ID, Bucket: name, Prefix: prefix}
	}
	page, err := a.svc.List(a.ctx, at("dir/"), app.ListOptions{})
	if err != nil {
		t.Fatalf("List dir/: %v", err)
	}
	var keys []string
	for _, e := range page.Entries {
		keys = append(keys, e.Key)
	}
	if !slices.Contains(keys, "dir/a.txt") || !slices.Contains(keys, "dir/sub/") || len(keys) != 2 {
		t.Fatalf("List dir/ = %v, want dir/a.txt and dir/sub/", keys)
	}
	if _, err := a.svc.List(a.ctx, at("dir/sub/"), app.ListOptions{}); err != nil {
		t.Fatalf("List dir/sub/: %v", err)
	}
	if _, err := a.svc.List(a.ctx, at(""), app.ListOptions{}); !app.IsKind(err, app.ErrAccessDenied) {
		t.Fatalf("List of the bucket root: %v (want AccessDenied)", err)
	}
	if _, err := a.svc.List(a.ctx, at("other/"), app.ListOptions{}); !app.IsKind(err, app.ErrAccessDenied) {
		t.Fatalf("List other/: %v (want AccessDenied)", err)
	}
	if data, _, err := a.svc.Preview(a.ctx, at("dir/"), "dir/a.txt", "", 1024); err != nil || string(data) != "inside" {
		t.Fatalf("Preview dir/a.txt = %q, %v", data, err)
	}
	// The sidebar's Download bucket for a named folder: the folder as one key,
	// from the bucket root, which is never listed.
	dest := t.TempDir()
	g, err := a.svc.Download(app.DownloadRequest{Source: at(""), Keys: []string{"dir/"}, DestDir: dest})
	if err != nil {
		t.Fatal(err)
	}
	a.groupOK(t, g)
	files := localFiles(t, dest)
	_, inside := hasFileWithSHA(files, sum([]byte("inside")))
	_, deeper := hasFileWithSHA(files, sum([]byte("deeper")))
	if len(files) != 2 || !inside || !deeper {
		t.Fatalf("downloaded %v", files)
	}
}
