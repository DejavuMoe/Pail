package integration_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/DejavuMoe/Pail/internal/app"
)

func TestAppBucketManagement(t *testing.T) {
	a := newApp(t)
	name := fmt.Sprintf("pail-app-mgmt-%d", time.Now().UnixNano())
	if err := a.svc.CreateBucket(a.ctx, a.acct.ID, name, "", false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.forceClean(t, name) })
	list, err := a.svc.Buckets(a.ctx, a.acct.ID)
	found := false
	for _, b := range list {
		if b.Name == name {
			found = !b.Created.IsZero()
		}
	}
	if err != nil || !found {
		t.Fatalf("Buckets: found=%v err=%v", found, err)
	}
	if err := a.svc.CreateBucket(a.ctx, a.acct.ID, name, "", false); err == nil || !(app.IsKind(err, app.ErrConflict) || app.IsKind(err, app.ErrOther)) {
		t.Errorf("creating an existing bucket should fail with a conflict, got %v", err)
	}

	// Versioning on, then off (suspended).
	if cfg, err := a.svc.GetBucketConfig(a.ctx, a.acct.ID, name, app.ConfigVersioning); err != nil || cfg != "" {
		t.Errorf("fresh bucket versioning = %q, %v (want not configured)", cfg, err)
	}
	if err := a.svc.PutBucketConfig(a.ctx, a.acct.ID, name, app.ConfigVersioning, `{"Status":"Enabled"}`); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := a.svc.GetBucketConfig(a.ctx, a.acct.ID, name, app.ConfigVersioning); !strings.Contains(cfg, "Enabled") {
		t.Errorf("versioning after enable: %q", cfg)
	}
	if err := a.svc.PutBucketConfig(a.ctx, a.acct.ID, name, app.ConfigVersioning, `{"Status":"Suspended"}`); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := a.svc.GetBucketConfig(a.ctx, a.acct.ID, name, app.ConfigVersioning); !strings.Contains(cfg, "Suspended") {
		t.Errorf("versioning after suspend: %q", cfg)
	}
	if err := a.svc.PutBucketConfig(a.ctx, a.acct.ID, name, app.ConfigVersioning, `{"Status":"Maybe"}`); !app.IsKind(err, app.ErrInvalid) {
		t.Errorf("bad versioning status must be Invalid, got %v", err)
	}

	policy := fmt.Sprintf(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":"*","Action":["s3:GetObject"],"Resource":["arn:aws:s3:::%s/public/*"]}]}`, name)
	for _, c := range []struct {
		cfg    app.BucketConfig
		body   string
		marker string
	}{
		{app.ConfigPolicy, policy, "s3:GetObject"},
		{app.ConfigCORS, `{"CORSRules":[{"AllowedMethods":["GET","PUT"],"AllowedOrigins":["https://example.test"],"AllowedHeaders":["*"],"MaxAgeSeconds":600}]}`, "https://example.test"},
		{app.ConfigLifecycle, `{"Rules":[{"ID":"expire-tmp","Status":"Enabled","Filter":{"Prefix":"tmp/"},"Expiration":{"Days":7}}]}`, "expire-tmp"},
		{app.ConfigTagging, `{"TagSet":[{"Key":"env","Value":"integration"}]}`, "integration"},
	} {
		t.Run(string(c.cfg), func(t *testing.T) {
			ctx := a.ctx
			if got, err := a.svc.GetBucketConfig(ctx, a.acct.ID, name, c.cfg); err != nil || got != "" {
				t.Fatalf("initial Get = %q, %v (want \"\", nil)", got, err)
			}
			if err := a.svc.PutBucketConfig(ctx, a.acct.ID, name, c.cfg, c.body); err != nil {
				if app.IsKind(err, app.ErrUnsupported) {
					t.Skipf("this MinIO does not implement the %s API; the app reports it as Unsupported: %v", c.cfg, err)
				}
				t.Fatalf("Put: %v", err)
			}
			got, err := a.svc.GetBucketConfig(ctx, a.acct.ID, name, c.cfg)
			if err != nil || !strings.Contains(got, c.marker) {
				t.Fatalf("Get after Put = %q, %v", got, err)
			}
			if err := a.svc.DeleteBucketConfig(ctx, a.acct.ID, name, c.cfg); err != nil {
				t.Fatalf("Delete: %v", err)
			}
			if got, err := a.svc.GetBucketConfig(ctx, a.acct.ID, name, c.cfg); err != nil || got != "" {
				t.Fatalf("Get after Delete = %q, %v", got, err)
			}
		})
	}

	// Seed versions, delete markers and an unfinished multipart upload, then DeleteBucket must refuse.
	a.put(t, name, "v/a.txt", []byte("1"))
	a.put(t, name, "v/a.txt", []byte("2"))
	a.put(t, name, "v/b.txt", []byte("3"))
	if _, err := a.sdk.S3().DeleteObject(a.ctx, &s3.DeleteObjectInput{Bucket: &name, Key: aws.String("v/b.txt")}); err != nil {
		t.Fatal(err)
	}
	if err := a.svc.PutBucketConfig(a.ctx, a.acct.ID, name, app.ConfigVersioning, `{"Status":"Enabled"}`); err != nil {
		t.Fatal(err)
	}
	a.put(t, name, "v/a.txt", []byte("3"))
	if _, err := a.sdk.S3().DeleteObject(a.ctx, &s3.DeleteObjectInput{Bucket: &name, Key: aws.String("v/a.txt")}); err != nil {
		t.Fatal(err)
	}
	if err := a.svc.DeleteBucket(a.ctx, a.acct.ID, name); err == nil {
		t.Fatal("DeleteBucket of a non-empty bucket must fail")
	} else {
		t.Logf("DeleteBucket(non-empty): %v", err)
	}

	// EmptyBucket without allVersions only hides the objects.
	id, err := a.svc.EmptyBucket(a.acct.ID, name, false)
	if err != nil {
		t.Fatal(err)
	}
	a.taskOK(t, id)
	if len(a.keys(t, name, "")) != 0 || len(a.versions(t, name, "")) == 0 {
		t.Errorf("EmptyBucket(allVersions=false): keys=%v versions=%d", a.keys(t, name, ""), len(a.versions(t, name, "")))
	}
	id, err = a.svc.EmptyBucket(a.acct.ID, name, true)
	if err != nil {
		t.Fatal(err)
	}
	a.taskOK(t, id)
	if v := a.versions(t, name, ""); len(v) != 0 {
		t.Fatalf("EmptyBucket(allVersions=true) left %+v", v)
	}
	if err := a.svc.DeleteBucket(a.ctx, a.acct.ID, name); err != nil {
		t.Fatalf("DeleteBucket: %v", err)
	}
	list, _ = a.svc.Buckets(a.ctx, a.acct.ID)
	for _, b := range list {
		if b.Name == name {
			t.Fatal("bucket still listed after DeleteBucket")
		}
	}
}

// ---- sync ---------------------------------------------------------------------------

func TestAppSyncUploadAndMirror(t *testing.T) {
	a := newApp(t)
	syncUploadAndMirror(t, a.target(t, false))
}

func TestAppSyncDownload(t *testing.T) {
	a := newApp(t)
	syncDownload(t, a.target(t, false))
}

func TestAppSyncChecksumMode(t *testing.T) {
	a := newApp(t)
	syncChecksumMode(t, a.target(t, false))
}
