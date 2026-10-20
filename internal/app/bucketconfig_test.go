package app

import (
	"net/http"
	"strings"
	"testing"

	"github.com/DejavuMoe/Pail/internal/s3test"
)

var bmConfigSamples = []struct {
	cfg       BucketConfig
	json      string
	marker    string
	deletable bool
	startsNil bool // Get on a fresh bucket returns ""
}{
	{ConfigVersioning, `{"Status":"Enabled"}`, "Enabled", false, true},
	{ConfigPolicy, `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::cfg/*"}]}`, "s3:GetObject", true, true},
	{ConfigCORS, `{"CORSRules":[{"AllowedMethods":["GET"],"AllowedOrigins":["*"]}]}`, "AllowedOrigins", true, true},
	{ConfigLifecycle, `{"Rules":[{"ID":"r","Status":"Enabled","Filter":{"Prefix":"logs/"},"Expiration":{"Days":30}}]}`, "logs/", true, true},
	{ConfigEncryption, `{"Rules":[{"ApplyServerSideEncryptionByDefault":{"SSEAlgorithm":"AES256"}}]}`, "AES256", true, true},
	{ConfigLogging, `{"LoggingEnabled":{"TargetBucket":"tb","TargetPrefix":"l/"}}`, "TargetBucket", false, true},
	{ConfigTagging, `{"TagSet":[{"Key":"env","Value":"test"}]}`, "env", true, true},
	{ConfigWebsite, `{"IndexDocument":{"Suffix":"index.html"}}`, "index.html", true, true},
	{ConfigPublicAccessBlock, `{"BlockPublicAcls":true,"IgnorePublicAcls":true,"BlockPublicPolicy":true,"RestrictPublicBuckets":true}`, "BlockPublicAcls", true, true},
	{ConfigOwnership, `{"Rules":[{"ObjectOwnership":"BucketOwnerEnforced"}]}`, "BucketOwnerEnforced", true, true},
	{ConfigObjectLock, `{"ObjectLockEnabled":"Enabled","Rule":{"DefaultRetention":{"Mode":"GOVERNANCE","Days":1}}}`, "GOVERNANCE", false, true},
	{ConfigRequestPayment, `{"Payer":"Requester"}`, "Requester", false, false},
	{ConfigAccelerate, `{"Status":"Enabled"}`, "Enabled", false, true},
	{ConfigReplication, `{"Role":"arn:aws:iam::1:role/r","Rules":[{"Status":"Enabled","Priority":1,"Filter":{"Prefix":""},"Destination":{"Bucket":"arn:aws:s3:::dest"}}]}`, "arn:aws:s3:::dest", true, true},
}

func TestBucketConfigRoundTrip(t *testing.T) {
	e := newS3Env(t)
	ctx := testCtx(t)
	e.srv.CreateBucket("cfg", false)
	if len(bmConfigSamples) != 14 {
		t.Fatal("every BucketConfig must be covered")
	}
	for _, tc := range bmConfigSamples {
		t.Run(string(tc.cfg), func(t *testing.T) {
			js, err := e.s.GetBucketConfig(ctx, e.acc.ID, "cfg", tc.cfg)
			if err != nil {
				t.Fatalf("get fresh: %v", err)
			}
			if tc.startsNil && js != "" {
				t.Fatalf("fresh bucket must report not configured, got %q", js)
			}
			if err := e.s.PutBucketConfig(ctx, e.acc.ID, "cfg", tc.cfg, tc.json); err != nil {
				t.Fatalf("put: %v", err)
			}
			js, err = e.s.GetBucketConfig(ctx, e.acc.ID, "cfg", tc.cfg)
			if err != nil || !strings.Contains(js, tc.marker) || !strings.Contains(js, "\n") {
				t.Fatalf("get after put: %q %v", js, err)
			}
			// What Get returns must be accepted by Put (UI edit loop).
			if err := e.s.PutBucketConfig(ctx, e.acc.ID, "cfg", tc.cfg, js); err != nil {
				t.Fatalf("re-put of own output: %v\n%s", err, js)
			}
			err = e.s.DeleteBucketConfig(ctx, e.acc.ID, "cfg", tc.cfg)
			if !tc.deletable {
				if !IsKind(err, ErrInvalid) {
					t.Fatalf("delete must be Invalid, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("delete: %v", err)
			}
			if js, err := e.s.GetBucketConfig(ctx, e.acc.ID, "cfg", tc.cfg); err != nil || js != "" {
				t.Fatalf("after delete: %q %v", js, err)
			}
		})
	}
}

func TestBucketConfigRejectsBadInput(t *testing.T) {
	e := newS3Env(t)
	ctx := testCtx(t)
	e.srv.CreateBucket("cfg", false)
	bad := []struct {
		cfg  BucketConfig
		json string
	}{
		{ConfigCORS, `{"CORSRules":[],"Bogus":1}`},
		{ConfigCORS, `{"CORSRules":[{"AllowedMethods":["GET"],"AllowedOrigins":["*"],"Nope":true}]}`},
		{ConfigCORS, `{} {}`},
		{ConfigCORS, ``},
		{ConfigCORS, `not json`},
		{ConfigVersioning, `{"Status":"Disabled"}`},
		{ConfigVersioning, `{}`},
		{ConfigPolicy, `[1]`},
		{ConfigPolicy, `"x"`},
		{ConfigPolicy, `null`},
		{ConfigRequestPayment, `{"Payer":"Nobody"}`},
		{ConfigAccelerate, `{"Status":"Suspend"}`},
		{BucketConfig("bogus"), `{}`},
	}
	for _, tc := range bad {
		if err := e.s.PutBucketConfig(ctx, e.acc.ID, "cfg", tc.cfg, tc.json); !IsKind(err, ErrInvalid) {
			t.Errorf("%s %q: want Invalid, got %v", tc.cfg, tc.json, err)
		}
	}
	if err := e.s.DeleteBucketConfig(ctx, e.acc.ID, "cfg", BucketConfig("bogus")); !IsKind(err, ErrInvalid) {
		t.Error(err)
	}
	if _, err := e.s.GetBucketConfig(ctx, e.acc.ID, "cfg", BucketConfig("bogus")); !IsKind(err, ErrInvalid) {
		t.Error(err)
	}
	for _, r := range e.srv.Requests() {
		if r.Method == http.MethodPut {
			t.Fatalf("a rejected configuration reached the service: %+v", r)
		}
	}
}

func TestBucketConfigVersioningSuspendedAndPolicyPretty(t *testing.T) {
	e := newS3Env(t)
	ctx := testCtx(t)
	e.srv.CreateBucket("cfg", false)
	if err := e.s.PutBucketConfig(ctx, e.acc.ID, "cfg", ConfigVersioning, `{"Status":"Suspended"}`); err != nil {
		t.Fatal(err)
	}
	if js, _ := e.s.GetBucketConfig(ctx, e.acc.ID, "cfg", ConfigVersioning); !strings.Contains(js, "Suspended") {
		t.Fatal(js)
	}
	if err := e.s.PutBucketConfig(ctx, e.acc.ID, "cfg", ConfigPolicy, `{"Version":"2012-10-17","Statement":[]}`); err != nil {
		t.Fatal(err)
	}
	js, _ := e.s.GetBucketConfig(ctx, e.acc.ID, "cfg", ConfigPolicy)
	if !strings.HasPrefix(js, "{\n  \"Version\"") {
		t.Fatalf("policy not pretty-printed: %q", js)
	}
}

func TestBucketConfigUnsupportedAndAccessDenied(t *testing.T) {
	e := newS3Env(t)
	ctx := testCtx(t)
	e.srv.CreateBucket("cfg", false)
	e.srv.SetUnsupported("cors")
	if _, err := e.s.GetBucketConfig(ctx, e.acc.ID, "cfg", ConfigCORS); !IsKind(err, ErrUnsupported) {
		t.Fatalf("get: want Unsupported, got %v", err)
	}
	if err := e.s.PutBucketConfig(ctx, e.acc.ID, "cfg", ConfigCORS, `{"CORSRules":[{"AllowedMethods":["GET"],"AllowedOrigins":["*"]}]}`); !IsKind(err, ErrUnsupported) {
		t.Fatalf("put: want Unsupported, got %v", err)
	}
	if err := e.s.DeleteBucketConfig(ctx, e.acc.ID, "cfg", ConfigCORS); !IsKind(err, ErrUnsupported) {
		t.Fatalf("delete: want Unsupported, got %v", err)
	}
	e.srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.URL.Query().Has("policy") || r.URL.Query().Has("lifecycle") {
			return &s3test.Fault{Status: 403, Code: "AccessDenied", Message: "no"}
		}
		return nil
	})
	for _, c := range []BucketConfig{ConfigPolicy, ConfigLifecycle} {
		js, err := e.s.GetBucketConfig(ctx, e.acc.ID, "cfg", c)
		if !IsKind(err, ErrAccessDenied) || js != "" {
			t.Fatalf("%s: 403 must be AccessDenied, got %q %v", c, js, err)
		}
	}
}
