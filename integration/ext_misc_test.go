package integration_test

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/DejavuMoe/Pail/internal/app"
)

// TestExtPresign: presigned GET (current object, unicode key) fetched with net/http, and a
// presigned PUT uploaded with net/http then verified through the app.
func TestExtPresign(t *testing.T) {
	x := newExt(t, "")
	body := payload(61, 200*1024)
	x.putk(t, "ps/get me.bin", body)
	x.putk(t, "ps/中文 文件.txt", []byte("unicode presign"))

	for key, want := range map[string][]byte{"ps/get me.bin": body, "ps/中文 文件.txt": []byte("unicode presign")} {
		u, err := x.svc.PresignURL(x.ctx, x.at("ps/"), x.k(key), "", "GET", 10*time.Minute)
		if err != nil {
			t.Fatalf("PresignURL GET %q: %v", key, err)
		}
		if strings.Contains(u, x.ecfg.SecretKey) {
			t.Fatal("presigned URL contains the secret key") // would be a serious leak; never print u
		}
		status, got, _, err := httpGet(u, 60*time.Second)
		if err != nil || status != http.StatusOK || !bytes.Equal(got, want) {
			t.Errorf("presigned GET %q: status=%d err=%v bytes-equal=%v", key, status, err, bytes.Equal(got, want))
		}
	}

	// HEAD URL.
	if u, err := x.svc.PresignURL(x.ctx, x.at("ps/"), x.k("ps/get me.bin"), "", "HEAD", 5*time.Minute); err != nil {
		t.Errorf("PresignURL HEAD: %v", err)
	} else {
		req, _ := http.NewRequest(http.MethodHead, u, nil)
		resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
		if err != nil || resp.StatusCode != http.StatusOK || resp.ContentLength != int64(len(body)) {
			t.Errorf("presigned HEAD: err=%v resp=%v", err, resp)
		}
		if resp != nil {
			resp.Body.Close()
		}
	}

	// PUT URL.
	put := []byte("uploaded through a presigned PUT")
	u, err := x.svc.PresignURL(x.ctx, x.at("ps/"), x.k("ps/put me.txt"), "", "PUT", 10*time.Minute)
	if err != nil {
		t.Fatalf("PresignURL PUT: %v", err)
	}
	req, _ := http.NewRequest(http.MethodPut, u, bytes.NewReader(put))
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("presigned PUT: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("presigned PUT status %d", resp.StatusCode)
	}
	info, err := x.svc.Head(x.ctx, x.at("ps/"), x.k("ps/put me.txt"), "")
	if err != nil || info.Size != int64(len(put)) {
		t.Fatalf("Head after presigned PUT = %+v, %v", info, err)
	}
	if string(x.get(t, x.bucket, x.k("ps/put me.txt"), "")) != string(put) {
		t.Error("presigned PUT content mismatch")
	}
	// An expired / tampered signature must be refused (tamper: change the key in the path).
	if bad, err := x.svc.PresignURL(x.ctx, x.at("ps/"), x.k("ps/get me.bin"), "", "GET", 10*time.Minute); err == nil {
		bad = strings.Replace(bad, "get%20me.bin", "put%20me.txt", 1)
		if status, _, _, err := httpGet(bad, 30*time.Second); err == nil && status == http.StatusOK {
			t.Error("a tampered presigned URL was accepted")
		} else {
			t.Logf("tampered presigned URL: status=%d", status)
		}
	}
}

// TestExtMultipartListAndAbort creates dangling uploads below the run prefix with the raw
// SDK, lists them through the app and aborts them through the app.
func TestExtMultipartListAndAbort(t *testing.T) {
	x := newExt(t, "")
	multipartListAndAbort(t, x.target)
}

// TestExtCompressionRules: GZIP / ZIP / AES-ZIP upload rules scoped to this test's prefix,
// then raw and auto-decoded downloads.
func TestExtCompressionRules(t *testing.T) {
	x := newExt(t, "")
	compressionRules(t, x.target)
}

// TestExtGzipEncodingBehaviour records how the gateway/proxy treats an object stored with
// Content-Encoding: gzip (raw SDK calls): HEAD, GET with identity, GET asking for gzip.
func TestExtGzipEncodingBehaviour(t *testing.T) {
	x := newExt(t, "")
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	plain := []byte(strings.Repeat("gzip behaviour probe line\n", 2000))
	_, _ = zw.Write(plain)
	_ = zw.Close()
	key := x.k("gzip/probe.txt")
	x.spend(t, int64(buf.Len()))
	_, err := x.sdk.S3().PutObject(x.ctx, &s3.PutObjectInput{Bucket: &x.bucket, Key: &key, Body: bytes.NewReader(buf.Bytes()), ContentEncoding: aws.String("gzip"), ContentType: aws.String("text/plain")})
	if err != nil {
		t.Fatal(err)
	}
	h := x.head(t, x.bucket, key)
	t.Logf("stored %d gzip bytes (original %d); HEAD: Content-Length=%d Content-Encoding=%q", buf.Len(), len(plain), aws.ToInt64(h.ContentLength), aws.ToString(h.ContentEncoding))
	raw := x.get(t, x.bucket, key, "")
	t.Logf("SDK GET (Accept-Encoding: identity): %d bytes, equals original=%v, equals stored gzip=%v", len(raw), bytes.Equal(raw, plain), bytes.Equal(raw, buf.Bytes()))
	u, err := x.svc.PresignURL(x.ctx, x.at("gzip/"), key, "", "GET", 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := (&http.Client{Timeout: 60 * time.Second, Transport: &http.Transport{DisableCompression: true}}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	t.Logf("presigned GET asking for gzip: status=%d Content-Encoding=%q %d bytes, equals stored gzip=%v", resp.StatusCode, resp.Header.Get("Content-Encoding"), len(b), bytes.Equal(b, buf.Bytes()))
}

// ---- sync ---------------------------------------------------------------------------

func TestExtSyncUploadAndMirror(t *testing.T) {
	x := newExt(t, "")
	syncUploadAndMirror(t, x.target)
}

func TestExtSyncDownload(t *testing.T) {
	x := newExt(t, "")
	syncDownload(t, x.target)
}

func TestExtSyncChecksumMode(t *testing.T) {
	x := newExt(t, "")
	syncChecksumMode(t, x.target)
}

// ---- CDN -----------------------------------------------------------------------------

// TestExtCDN: the public smoji reference (one read-only GET) and a unique object of this run
// fetched through the CDN base URL. The CDN link comes from the app (Account.CDNURL).
func TestExtCDN(t *testing.T) {
	x := newExt(t, "")
	if x.ecfg.CDN == "" {
		t.Skip("PAIL_EXT_CDN not set")
	}
	acct := x.acct
	acct.CDNURL = x.ecfg.CDN
	acct.CDNBuckets = []string{x.bucket, "smoji"}
	saved, err := x.svc.SaveAccount(acct, nil)
	if err != nil {
		t.Fatalf("SaveAccount with CDN: %v", err)
	}
	x.acct = saved

	t.Run("smoji reference", func(t *testing.T) {
		// Pure URL computation through the app for the reference bucket, then ONE GET.
		u := x.svc.CDNURL(app.Location{AccountID: x.acct.ID, Bucket: "smoji"}, "smoji.json")
		if u != x.ecfg.CDN+"/smoji/smoji.json" {
			t.Fatalf("CDNURL = %q", u)
		}
		status, body, hdr, err := httpGet(u, 20*time.Second)
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		t.Logf("GET %s -> %d, %d bytes, content-type=%q cache-status=%q server=%q", u, status, len(body), hdr.Get("Content-Type"), hdr.Get("Cf-Cache-Status"), hdr.Get("Server"))
		if status != http.StatusOK {
			t.Fatalf("reference object not reachable: %d", status)
		}
		if !json.Valid(body) {
			t.Errorf("reference body is not valid JSON (%d bytes)", len(body))
		}
	})

	t.Run("run object", func(t *testing.T) {
		var rnd [8]byte
		_, _ = rand.Read(rnd[:])
		rel := "cdn/unique-" + hex.EncodeToString(rnd[:]) + " é.txt"
		want := []byte("cdn check " + hex.EncodeToString(rnd[:]))
		x.putk(t, rel, want)
		u := x.svc.CDNURL(x.at(""), x.k(rel))
		if u == "" || !strings.HasPrefix(u, x.ecfg.CDN+"/"+x.bucket+"/pail-e2e/") || strings.Contains(u, " ") {
			t.Fatalf("CDNURL = %q", u)
		}
		var status int
		var body []byte
		var hdr http.Header
		for attempt := 0; attempt < 3; attempt++ { // propagation / first-fetch grace, never retries a cached result
			status, body, hdr, err = httpGet(u, 20*time.Second)
			if err == nil && status == http.StatusOK {
				break
			}
			time.Sleep(2 * time.Second)
		}
		if err != nil {
			t.Fatalf("GET %s: %v", u, err)
		}
		t.Logf("GET <cdn>/%s/<run key> -> %d, %d bytes, cache-status=%q", x.bucket, status, len(body), hdr.Get("Cf-Cache-Status"))
		switch {
		case status == http.StatusOK && bytes.Equal(body, want):
			t.Logf("CDN MAPPING VERIFIED: a unique object of this run is served through the CDN with identical bytes")
		case status == http.StatusOK:
			t.Errorf("CDN answered 200 with different bytes (%d bytes)", len(body))
		case status == http.StatusForbidden || status == http.StatusNotFound:
			t.Skipf("CDN answered %d for bucket %q: the bucket is not public via the CDN (not a test failure)", status, x.bucket)
		default:
			t.Errorf("unexpected CDN status %d", status)
		}
	})
}
