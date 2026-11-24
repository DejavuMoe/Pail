package storage

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func testDPOPKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}))
}

func writeLoginCache(t *testing.T, cacheDir, session, accessKey, secretKey, sessionToken, signInRegion string, expiresAt time.Time) string {
	t.Helper()
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
		"refreshToken":  "refresh-tok-1",
		"identityToken": "id-tok-1",
		"clientId":      "arn:aws:signin:::devtools/same-device",
		"dpopKey":       testDPOPKeyPEM(t),
		"signInRegion":  signInRegion,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func setupLoginEnv(t *testing.T, configText string) (configPath, cacheDir string) {
	t.Helper()
	dir := t.TempDir()
	configPath = filepath.Join(dir, "config")
	credsPath := filepath.Join(dir, "credentials")
	cacheDir = filepath.Join(dir, "login-cache")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(configText), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_CONFIG_FILE", configPath)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", credsPath)
	t.Setenv("AWS_LOGIN_CACHE_DIRECTORY", cacheDir)
	t.Setenv("AWS_REGION", "")
	t.Setenv("AWS_DEFAULT_REGION", "")
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_SESSION_TOKEN", "")
	return configPath, cacheDir
}

func TestLoginProfileReadsCachedCredentialsAndProfileRegion(t *testing.T) {
	session := "arn:aws:sts::123456789012:assumed-role/Admin/dev"
	_, cacheDir := setupLoginEnv(t, fmt.Sprintf("[profile work]\nregion = ap-northeast-1\nlogin_session = %s\n", session))
	writeLoginCache(t, cacheDir, session, "ASIALOGINKEY", "login-secret", "login-session-token", "ap-northeast-1", time.Now().Add(15*time.Minute))

	var gotAuth, gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotToken = r.Header.Get("X-Amz-Security-Token")
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, `<ListAllMyBucketsResult><Buckets><Bucket><Name>my-bucket</Name><BucketRegion>ap-northeast-1</BucketRegion></Bucket></Buckets></ListAllMyBucketsResult>`)
	}))
	defer srv.Close()

	// Empty Config.Region must pick up the profile's region from ~/.aws/config.
	cfg := Config{
		Endpoint:              srv.URL,
		UsePathStyle:          true,
		UseDefaultCredentials: true,
		Profile:               "work",
		HTTPClient:            srv.Client(),
	}
	ac, err := cfg.AWSConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ac.Region != "ap-northeast-1" {
		t.Fatalf("AWSConfig region = %q, want ap-northeast-1 from profile", ac.Region)
	}

	c, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	bs, err := c.ListBuckets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(bs) != 1 || bs[0].Name != "my-bucket" {
		t.Fatalf("buckets = %+v", bs)
	}
	if !strings.Contains(gotAuth, "Credential=ASIALOGINKEY/") || !strings.Contains(gotAuth, "/ap-northeast-1/s3/aws4_request") {
		t.Fatalf("request not signed with login credentials and profile region: %q", gotAuth)
	}
	if gotToken != "login-session-token" {
		t.Fatalf("session token = %q, want login-session-token", gotToken)
	}

	// Explicit Config.Region still overrides the profile's region.
	cfg.Region = "eu-central-1"
	ac, err = cfg.AWSConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ac.Region != "eu-central-1" {
		t.Fatalf("explicit region override = %q, want eu-central-1", ac.Region)
	}
}

func TestLoginProfileDefaultRegionFallback(t *testing.T) {
	session := "arn:aws:iam::123456789012:user/alice"
	_, cacheDir := setupLoginEnv(t, fmt.Sprintf("[profile noregion]\nlogin_session = %s\n", session))
	writeLoginCache(t, cacheDir, session, "ASIAFALLBACK", "secret", "tok", "us-east-1", time.Now().Add(15*time.Minute))

	ac, err := (Config{UseDefaultCredentials: true, Profile: "noregion"}).AWSConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ac.Region != "us-east-1" {
		t.Fatalf("fallback region = %q, want us-east-1", ac.Region)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestLoginProfileRefreshesExpiredTokenWithDPoP(t *testing.T) {
	session := "arn:aws:sts::123456789012:assumed-role/Admin/refresh"
	_, cacheDir := setupLoginEnv(t, fmt.Sprintf("[profile work]\nregion = eu-west-1\nlogin_session = %s\n", session))
	cachePath := writeLoginCache(t, cacheDir, session, "ASIAOLD", "old-secret", "old-tok", "eu-west-1", time.Now().Add(-time.Minute))

	var refreshURL, dpopHeader, s3Auth, s3Token string
	hc := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Host, "signin.aws.amazon.com") {
			refreshURL = r.URL.String()
			dpopHeader = r.Header.Get("DPoP")
			body := `{"accessToken":{"accessKeyId":"ASIAREFRESHED","secretAccessKey":"new-secret","sessionToken":"new-tok"},"tokenType":"aws_sigv4","expiresIn":900,"refreshToken":"refresh-tok-2"}`
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(body)),
				Request:    r,
			}, nil
		}
		s3Auth = r.Header.Get("Authorization")
		s3Token = r.Header.Get("X-Amz-Security-Token")
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"application/xml"}},
			Body:       io.NopCloser(strings.NewReader(`<ListAllMyBucketsResult><Buckets/></ListAllMyBucketsResult>`)),
			Request:    r,
		}, nil
	})}

	c, err := New(context.Background(), Config{
		UseDefaultCredentials: true,
		Profile:               "work",
		HTTPClient:            hc,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListBuckets(context.Background()); err != nil {
		t.Fatal(err)
	}
	if refreshURL != "https://eu-west-1.signin.aws.amazon.com/v1/token" {
		t.Fatalf("refresh URL = %q", refreshURL)
	}
	if len(strings.Split(dpopHeader, ".")) != 3 {
		t.Fatalf("expected DPoP JWT header, got %q", dpopHeader)
	}
	if !strings.Contains(s3Auth, "Credential=ASIAREFRESHED/") || s3Token != "new-tok" {
		t.Fatalf("S3 not signed with refreshed credentials: auth=%q token=%q", s3Auth, s3Token)
	}
	raw, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "ASIAREFRESHED") || !strings.Contains(string(raw), "refresh-tok-2") {
		t.Fatalf("refreshed token was not persisted to cache: %s", raw)
	}
}

func TestListAWSProfilesAndSharedConfigStamp(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config")
	credsPath := filepath.Join(dir, "credentials")
	t.Setenv("AWS_CONFIG_FILE", cfgPath)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", credsPath)

	// Missing files return empty list without error.
	got, err := ListAWSProfiles()
	if err != nil || len(got) != 0 {
		t.Fatalf("missing files: got=%+v err=%v", got, err)
	}
	stamp0 := SharedConfigStamp()

	cfgText := "\uFEFF" + `# comment
[sso-session corp]
sso_start_url = https://corp.awsapps.com/start
sso_region = us-east-1

[services local]
s3 =
  endpoint_url = http://localhost:9000

[profile work]
region = ap-northeast-1
login_session = arn:aws:sts::123456789012:assumed-role/Admin/work
s3 =
  max_concurrent_requests = 10

[default]
region = us-east-1
login_session = arn:aws:iam::123456789012:user/me

[profile sso-dev]
sso_session = corp
region = eu-west-1

[profile assume]
source_profile = work
role_arn = arn:aws:iam::123456789012:role/ReadOnly

[profile proc]
credential_process = /usr/local/bin/get-creds

[profile web]
web_identity_token_file = /tmp/token
role_arn = arn:aws:iam::123456789012:role/Web
`
	credsText := `[static-only]
aws_access_key_id = AKIAEXAMPLE
aws_secret_access_key = SECRETEXAMPLE

[profile ignored-in-creds]
aws_access_key_id = AKIAIGNORED
aws_secret_access_key = SECRETIGNORED
`
	if err := os.WriteFile(cfgPath, []byte(cfgText), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credsPath, []byte(credsText), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err = ListAWSProfiles()
	if err != nil {
		t.Fatal(err)
	}
	want := []AWSProfile{
		{Name: "default", Region: "us-east-1", Auth: ProfileAuthLogin},
		{Name: "assume", Region: "", Auth: ProfileAuthRole},
		{Name: "proc", Region: "", Auth: ProfileAuthProcess},
		{Name: "sso-dev", Region: "eu-west-1", Auth: ProfileAuthSSO},
		{Name: "static-only", Region: "", Auth: ProfileAuthStatic},
		{Name: "web", Region: "", Auth: ProfileAuthWebIdentity},
		{Name: "work", Region: "ap-northeast-1", Auth: ProfileAuthLogin},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("profiles:\ngot  %+v\nwant %+v", got, want)
	}

	stamp1 := SharedConfigStamp()
	if stamp1 == stamp0 {
		t.Fatal("SharedConfigStamp did not change after creating config files")
	}
	if err := os.WriteFile(cfgPath, []byte(cfgText+"\n[profile added]\nregion = us-west-2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if stamp2 := SharedConfigStamp(); stamp2 == stamp1 {
		t.Fatal("SharedConfigStamp did not change after updating config file")
	}
}
