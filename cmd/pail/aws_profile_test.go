package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeCLILoginCache(t *testing.T, cacheDir, session, accessKey, secretKey, sessionToken, region string, expiresAt time.Time) {
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
		"refreshToken":  "refresh-tok",
		"identityToken": "id-tok",
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

func setupCLILoginEnv(t *testing.T, cfgText string) (cacheDir string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config")
	credsPath := filepath.Join(dir, "credentials")
	cacheDir = filepath.Join(dir, "login-cache")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if cfgText != "" {
		if err := os.WriteFile(cfgPath, []byte(cfgText), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("AWS_CONFIG_FILE", cfgPath)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", credsPath)
	t.Setenv("AWS_LOGIN_CACHE_DIRECTORY", cacheDir)
	t.Setenv("AWS_REGION", "")
	t.Setenv("AWS_DEFAULT_REGION", "")
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_SESSION_TOKEN", "")
	return cacheDir
}

func TestAccountProfilesAndAWSLoginAdd(t *testing.T) {
	h := newHarness(t)

	// Empty config dir -> no profiles found.
	setupCLILoginEnv(t, "")
	r := h.want(h.run("account", "profiles"), 0)
	if !strings.Contains(r.errOut, "no local AWS profiles") {
		t.Fatalf("empty profiles stderr = %q", r.errOut)
	}
	h.want(h.run("account", "profiles", "extra"), 2)

	// With an aws login profile in ~/.aws/config.
	session := "arn:aws:sts::123456789012:assumed-role/Admin/cli"
	cacheDir := setupCLILoginEnv(t, fmt.Sprintf("[profile work]\nregion = ap-northeast-1\nlogin_session = %s\n", session))

	r = h.want(h.run("account", "profiles"), 0)
	if !strings.Contains(r.out, "NAME") || !strings.Contains(r.out, "work") || !strings.Contains(r.out, "ap-northeast-1") || !strings.Contains(r.out, "login") {
		t.Fatalf("account profiles output:\n%s", r.out)
	}
	ev := jsonLines(t, h.want(h.run("--json", "account", "profiles"), 0).out)
	if len(ev) != 2 || ev[0]["type"] != "profile" || ev[0]["name"] != "work" || ev[0]["region"] != "ap-northeast-1" || ev[0]["auth"] != "login" {
		t.Fatalf("account profiles json: %v", ev)
	}
	if ev[1]["type"] != "summary" || ev[1]["count"] != float64(1) {
		t.Fatalf("account profiles summary: %v", ev[1])
	}

	// Without a login cache file, adding with connection test fails and mentions aws login.
	r = h.want(h.run("account", "add", "work", "--endpoint", h.srv.URL, "--path-style", "--profile", "work"), 1)
	if !strings.Contains(r.errOut, "aws login") {
		t.Fatalf("missing login cache error = %q", r.errOut)
	}

	// With a valid cached login session, adding without --region succeeds and works for buckets.
	writeCLILoginCache(t, cacheDir, session, "ASIACLILOGIN", "cli-secret", "cli-token", "ap-northeast-1", time.Now().Add(15*time.Minute))
	h.want(h.run("account", "add", "work", "--endpoint", h.srv.URL, "--path-style", "--profile", "work"), 0)
	r = h.want(h.run("--account", "work", "buckets"), 0)
	if !strings.Contains(r.out, "bkt") {
		t.Fatalf("buckets with login profile = %q", r.out)
	}
}
