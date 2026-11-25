package app

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func writeTestLoginCache(t *testing.T, cacheDir, session, accessKey, secretKey, sessionToken, region string, expiresAt time.Time) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	kpem := string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}))
	sum := sha256.Sum256([]byte(strings.TrimSpace(session)))
	path := filepath.Join(cacheDir, strings.ToLower(hex.EncodeToString(sum[:]))+".json")
	payload := map[string]any{
		"accessToken": map[string]any{
			"accessKeyId":     accessKey,
			"secretAccessKey": secretKey,
			"sessionToken":    sessionToken,
			"accountId":       "123456789012",
			"expiresAt":       expiresAt.UTC().Format(time.RFC3339),
		},
		"tokenType":     "aws_sigv4",
		"refreshToken":  "refresh-token",
		"identityToken": "id-token",
		"clientId":      "arn:aws:signin:::devtools/same-device",
		"dpopKey":       kpem,
		"signInRegion":  region,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func setupAWSProfileEnv(t *testing.T, configText string) (configPath, cacheDir string) {
	t.Helper()
	dir := t.TempDir()
	configPath = filepath.Join(dir, "config")
	credsPath := filepath.Join(dir, "credentials")
	cacheDir = filepath.Join(dir, "login-cache")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if configText != "" {
		if err := os.WriteFile(configPath, []byte(configText), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("AWS_CONFIG_FILE", configPath)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", credsPath)
	t.Setenv("AWS_LOGIN_CACHE_DIRECTORY", cacheDir)
	t.Setenv("AWS_REGION", "")
	t.Setenv("AWS_DEFAULT_REGION", "")
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_DEFAULT_PROFILE", "")
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_SESSION_TOKEN", "")
	return configPath, cacheDir
}

func TestAWSLoginProfileTestAccountBucketsAndReload(t *testing.T) {
	session1 := "arn:aws:sts::123456789012:assumed-role/Admin/one"
	session2 := "arn:aws:sts::123456789012:assumed-role/PowerUser/two"
	cfgPath, cacheDir := setupAWSProfileEnv(t, fmt.Sprintf("[profile work]\nregion = ap-northeast-1\nlogin_session = %s\n", session1))
	writeTestLoginCache(t, cacheDir, session1, "ASIAFIRSTKEY", "secret-1", "token-1", "ap-northeast-1", time.Now().Add(15*time.Minute))

	local, err := LocalAWS()
	if err != nil {
		t.Fatal(err)
	}
	if profs := local.Profiles; len(profs) != 1 || profs[0].Name != "work" || profs[0].Region != "ap-northeast-1" || profs[0].Auth != ProfileAuthLogin {
		t.Fatalf("LocalAWS().Profiles = %+v", profs)
	}

	var mu sync.Mutex
	var auths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auths = append(auths, r.Header.Get("Authorization"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, listBucketsXML)
	}))
	defer srv.Close()

	s := newTestService(t, nil)
	acct := Account{
		Name:        "Work AWS",
		Provider:    ProviderS3,
		Endpoint:    srv.URL,
		PathStyle:   true,
		Credentials: CredProfile,
		Profile:     "work",
	}
	if err := s.TestAccount(testCtx(t), acct, nil); err != nil {
		t.Fatalf("TestAccount with aws login profile failed: %v", err)
	}
	saved, err := s.SaveAccount(acct, nil)
	if err != nil {
		t.Fatal(err)
	}
	bs, err := s.Buckets(testCtx(t), saved.ID)
	if err != nil || len(bs) != 2 {
		t.Fatalf("Buckets = %+v %v", bs, err)
	}

	mu.Lock()
	firstAuth := auths[len(auths)-1]
	mu.Unlock()
	if !strings.Contains(firstAuth, "Credential=ASIAFIRSTKEY/") || !strings.Contains(firstAuth, "/ap-northeast-1/s3/aws4_request") {
		t.Fatalf("first auth = %q", firstAuth)
	}

	// Simulate running `aws login --profile work` again with a new role ARN and region.
	writeTestLoginCache(t, cacheDir, session2, "ASIASECONDKEY", "secret-2", "token-2", "eu-west-1", time.Now().Add(15*time.Minute))
	newCfg := fmt.Sprintf("[profile work]\nregion = eu-west-1\nlogin_session = %s\n# updated\n", session2)
	if err := os.WriteFile(cfgPath, []byte(newCfg), 0o600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(cfgPath, future, future)

	if _, err := s.Buckets(testCtx(t), saved.ID); err != nil {
		t.Fatalf("Buckets after aws login refresh failed: %v", err)
	}
	mu.Lock()
	secondAuth := auths[len(auths)-1]
	mu.Unlock()
	if !strings.Contains(secondAuth, "Credential=ASIASECONDKEY/") || !strings.Contains(secondAuth, "/eu-west-1/s3/aws4_request") {
		t.Fatalf("client did not reload after ~/.aws/config changed: auth=%q", secondAuth)
	}
}

func TestAWSLoginProfileErrors(t *testing.T) {
	session := "arn:aws:sts::123456789012:assumed-role/Admin/missing-cache"
	setupAWSProfileEnv(t, fmt.Sprintf("[profile work]\nregion = us-east-1\nlogin_session = %s\n", session))

	srv := stubS3(t, nil)
	s := newTestService(t, nil)

	// Missing cache file -> ErrLoginRequired + ErrAccessDenied.
	err := s.TestAccount(testCtx(t), Account{
		Name:        "Work",
		Provider:    ProviderS3,
		Endpoint:    srv.URL,
		PathStyle:   true,
		Credentials: CredProfile,
		Profile:     "work",
	}, nil)
	if !errors.Is(err, ErrLoginRequired) || !IsKind(err, ErrAccessDenied) {
		t.Fatalf("missing login cache: want ErrLoginRequired/AccessDenied, got %v (kind=%v)", err, KindOf(err))
	}

	// Missing profile -> ErrInvalid with profile name.
	err = s.TestAccount(testCtx(t), Account{
		Name:        "Missing",
		Provider:    ProviderS3,
		Endpoint:    srv.URL,
		PathStyle:   true,
		Credentials: CredProfile,
		Profile:     "does-not-exist",
	}, nil)
	if !IsKind(err, ErrInvalid) || !strings.Contains(err.Error(), "does-not-exist") {
		t.Fatalf("missing profile: want ErrInvalid naming profile, got %v (kind=%v)", err, KindOf(err))
	}
}

// awsRecorder answers every request with an empty 200 and records where it
// went, standing in for Amazon S3 (an AWS account has no endpoint to point at
// a local server).
type awsRecorder struct {
	mu   sync.Mutex
	reqs []awsRequest
}

type awsRequest struct{ host, auth, body string }

func (r *awsRecorder) Do(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
	}
	r.mu.Lock()
	r.reqs = append(r.reqs, awsRequest{req.URL.Host, req.Header.Get("Authorization"), string(body)})
	r.mu.Unlock()
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
}

func (r *awsRecorder) last(t *testing.T) awsRequest {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.reqs) == 0 {
		t.Fatal("no request was sent")
	}
	return r.reqs[len(r.reqs)-1]
}

// An AWS profile account without a region of its own uses the profile's region
// for new buckets and public URLs, not us-east-1 (PR #9 review).
func TestAWSProfileRegionForCreateBucketAndPublicURL(t *testing.T) {
	setupAWSProfileEnv(t, "[profile eu]\nregion = eu-west-1\naws_access_key_id = AKIDEXAMPLE\naws_secret_access_key = secret\n")
	s := newTestService(t, nil)
	acct, err := s.SaveAccount(Account{
		Name:        "EU",
		Provider:    ProviderAWS,
		Credentials: CredProfile,
		Profile:     "eu",
		Buckets:     []AccountBucket{{Name: "named"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := testCtx(t)
	e, err := s.entry(ctx, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	rec := &awsRecorder{}
	e.cfg.HTTPClient = rec

	if r := s.AccountRegion(acct.ID); r != "" {
		t.Fatalf("AccountRegion before the client is built = %q, want \"\"", r)
	}
	// Named buckets: no ListBuckets, so no learnt bucket region either.
	if _, err := s.Buckets(ctx, acct.ID); err != nil {
		t.Fatal(err)
	}
	if r := s.AccountRegion(acct.ID); r != "eu-west-1" {
		t.Fatalf("AccountRegion = %q, want the profile's eu-west-1", r)
	}
	if got, want := s.PublicURL(Location{AccountID: acct.ID, Bucket: "named"}, "k"), "https://named.s3.eu-west-1.amazonaws.com/k"; got != want {
		t.Fatalf("PublicURL = %q, want %q", got, want)
	}

	for _, tc := range []struct {
		bucket, region, host, scope string
		constraint                  bool
	}{
		// Empty: the profile's region, with its LocationConstraint.
		{"in-profile-region", "", "in-profile-region.s3.eu-west-1.amazonaws.com", "/eu-west-1/s3/", true},
		// us-east-1 sends no LocationConstraint, so it must go to us-east-1.
		{"in-us-east", "us-east-1", "in-us-east.s3.us-east-1.amazonaws.com", "/us-east-1/s3/", false},
		{"in-tokyo", "ap-northeast-1", "in-tokyo.s3.ap-northeast-1.amazonaws.com", "/ap-northeast-1/s3/", true},
	} {
		if err := s.CreateBucket(ctx, acct.ID, tc.bucket, tc.region, false); err != nil {
			t.Fatalf("%s: %v", tc.bucket, err)
		}
		got := rec.last(t)
		if got.host != tc.host || !strings.Contains(got.auth, tc.scope) {
			t.Errorf("%s: sent to %s signed %q, want %s and scope %s", tc.bucket, got.host, got.auth, tc.host, tc.scope)
		}
		if has := strings.Contains(got.body, "<LocationConstraint>"); has != tc.constraint {
			t.Errorf("%s: body %q, want LocationConstraint %v", tc.bucket, got.body, tc.constraint)
		}
		want := tc.region
		if want == "" {
			want = "eu-west-1"
		}
		if tc.constraint && !strings.Contains(got.body, ">"+want+"<") {
			t.Errorf("%s: body %q does not name %s", tc.bucket, got.body, want)
		}
	}
}

func TestLocalAWSEnvironment(t *testing.T) {
	setupAWSProfileEnv(t, "[default]\nregion = us-west-2\n[profile work]\nregion = ap-northeast-1\n")
	local, err := LocalAWS()
	if err != nil {
		t.Fatal(err)
	}
	if local.DefaultProfile != "default" || local.EnvRegion != "" {
		t.Fatalf("LocalAWS = %+v", local)
	}
	if r := local.Region(""); r != "us-west-2" {
		t.Fatalf("default chain region = %q, want us-west-2", r)
	}
	t.Setenv("AWS_PROFILE", "work")
	if local, _ = LocalAWS(); local.DefaultProfile != "work" || local.Region("") != "ap-northeast-1" {
		t.Fatalf("AWS_PROFILE=work: %+v region %q", local, local.Region(""))
	}
	t.Setenv("AWS_DEFAULT_REGION", "eu-central-1")
	if local, _ = LocalAWS(); local.Region("") != "eu-central-1" || local.Region("work") != "eu-central-1" {
		t.Fatalf("AWS_DEFAULT_REGION must beat profile regions: %+v", local)
	}
	if local.Region("missing") != "eu-central-1" {
		t.Fatal("AWS_DEFAULT_REGION applies to any profile")
	}
}
