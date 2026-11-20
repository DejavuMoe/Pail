package integration_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
)

// The TestApp* transfer tests run the shared scenarios of shared_transfer_test.go
// against a test-owned MinIO bucket.

func TestAppUploadDownloadRoundTrip(t *testing.T) {
	a := newApp(t)
	uploadDownloadRoundTrip(t, a.target(t, false), 11*1024*1024+17, 3) // three 5 MiB parts
}

func TestAppUploadConflictPolicies(t *testing.T) {
	a := newApp(t)
	uploadConflictPolicies(t, a.target(t, false))
}

func TestAppDownloadConflictPolicies(t *testing.T) {
	a := newApp(t)
	downloadConflictPolicies(t, a.target(t, false))
}

func mustReadDir(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

// TestAppCreateOnlyRace: MinIO honours the create-only commit (If-None-Match: *),
// so the race outcome is asserted, not only recorded.
func TestAppCreateOnlyRace(t *testing.T) {
	a := newApp(t)
	createOnlyRace(t, a.target(t, false), 48*1024*1024, true) // ten parts: a wide window between HEAD and commit
}

// ---- compression rules --------------------------------------------------------------

func TestAppCompressionRulesRoundTrip(t *testing.T) {
	a := newApp(t)
	compressionRules(t, a.target(t, false))
}

func anyMetaValue(m map[string]string, want string) bool {
	for _, v := range m {
		if v == want {
			return true
		}
	}
	return false
}

// ---- checksum compatibility ---------------------------------------------------------

// recordingProxy forwards to the MinIO endpoint and records which checksum
// related headers the SDK sent. Requests are signed for the proxy's host, which
// the forwarded Host header preserves.
type recordingProxy struct {
	srv  *httptest.Server
	mu   sync.Mutex
	seen map[string]int
}

func newRecordingProxy(t *testing.T, target string) *recordingProxy {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	rp := &recordingProxy{seen: map[string]int{}}
	proxy := httputil.NewSingleHostReverseProxy(u)
	rp.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut || r.Method == http.MethodPost {
			rp.mu.Lock()
			for name := range r.Header {
				ln := strings.ToLower(name)
				if strings.HasPrefix(ln, "x-amz-checksum") || ln == "x-amz-sdk-checksum-algorithm" || ln == "x-amz-trailer" {
					rp.seen[ln]++
				}
			}
			if r.Header.Get("X-Amz-Content-Sha256") == "STREAMING-UNSIGNED-PAYLOAD-TRAILER" {
				rp.seen["streaming-trailer-payload"]++
			}
			rp.mu.Unlock()
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(rp.srv.Close)
	return rp
}

func (rp *recordingProxy) summary() string {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	if len(rp.seen) == 0 {
		return "no checksum headers"
	}
	var parts []string
	for k, v := range rp.seen {
		parts = append(parts, fmt.Sprintf("%s x%d", k, v))
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

// TestAppChecksumModesCustomEndpoint uploads small and multipart payloads through
// the app with every account ChecksumMode (the default "" = auto included) and
// logs which x-amz-checksum headers the SDK sent. MinIO must accept all of them.
func TestAppChecksumModesCustomEndpoint(t *testing.T) {
	cfg := localConfig(t)
	small, big := []byte("checksum compatibility"), payload(3, 11*1024*1024)
	for _, mode := range []string{"", "auto", "when_required", "when_supported"} {
		name := mode
		if name == "" {
			name = "default"
		}
		t.Run(name, func(t *testing.T) {
			rp := newRecordingProxy(t, cfg.Endpoint)
			a := newAppEnvAt(t, rp.srv.URL, mode)
			checksumModeRoundTrip(t, a.target(t, false), small, big)
			t.Logf("ChecksumMode %q: request headers seen by the server: %s", mode, rp.summary())
		})
	}
}

// ---- multipart upload management ---------------------------------------------------

func TestAppMultipartUploadsListAndAbort(t *testing.T) {
	a := newApp(t)
	multipartListAndAbort(t, a.target(t, false))
}
