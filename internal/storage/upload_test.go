package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// fakeDo is an HTTP client whose attempts each read up to read bytes of the
// request body and then answer status, or fail when status is 0.
type fakeDo struct {
	attempts []struct {
		read   int
		status int
	}
	bodies []io.Reader
}

func (f *fakeDo) Do(r *http.Request) (*http.Response, error) {
	a := f.attempts[0]
	f.attempts = f.attempts[1:]
	f.bodies = append(f.bodies, r.Body)
	if a.read < 0 {
		_, _ = io.Copy(io.Discard, r.Body)
	} else {
		_, _ = io.CopyN(io.Discard, r.Body, int64(a.read))
	}
	if a.status == 0 {
		return nil, errors.New("connection reset")
	}
	return &http.Response{StatusCode: a.status, Body: http.NoBody}, nil
}

func TestPartProgressCountsRetriesOnce(t *testing.T) {
	var reports []int64
	ctr := &counter{size: 10000, report: func(n int64) { reports = append(reports, n) }}
	p := &partProgress{ctr: ctr, length: 10000}
	f := &fakeDo{attempts: []struct{ read, status int }{{3000, 0}, {-1, 500}, {-1, 200}}}
	hc := &progressClient{base: f, p: p}
	do := func(extra int) {
		// The wire body may be longer than the payload (aws-chunked framing).
		req, _ := http.NewRequest(http.MethodPut, "http://x/", bytes.NewReader(make([]byte, 10000+extra)))
		_, _ = hc.Do(req)
	}
	do(0)   // reset after 3000 bytes
	do(0)   // whole body, then 500
	do(300) // success
	// A late read from the first, dead attempt changes nothing.
	_, _ = f.bodies[0].Read(make([]byte, 4096))
	p.finish(true)
	if got := ctr.total.Load(); got != 10000 {
		t.Fatalf("running total %d, want 10000", got)
	}
	checkProgress(t, reports, 10000)
	for i := 1; i < len(reports); i++ {
		if reports[i]-reports[i-1] > maxProgressStep {
			t.Fatalf("step %d", reports[i]-reports[i-1])
		}
	}
}

func TestPartProgressFailedCallTakesBytesBack(t *testing.T) {
	ctr := &counter{size: 1 << 20, report: func(int64) {}}
	p := &partProgress{ctr: ctr, length: 4096}
	hc := &progressClient{base: &fakeDo{attempts: []struct{ read, status int }{{-1, 200}}}, p: p}
	req, _ := http.NewRequest(http.MethodPut, "http://x/", bytes.NewReader(make([]byte, 4096)))
	_, _ = hc.Do(req)
	p.finish(false) // e.g. the response failed to deserialize after retries
	if ctr.total.Load() != 0 || ctr.last.Load() != 4096 {
		t.Fatalf("total=%d last=%d", ctr.total.Load(), ctr.last.Load())
	}
}

func TestUploadProgressIsByteLevel(t *testing.T) {
	for _, size := range []int{3 << 20, 11 << 20} { // single PutObject, multipart
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			var mu sync.Mutex
			stored := map[string][]byte{}
			c := testClient(t, multipartHandler(t, &mu, stored, nil))
			var reports []int64
			data := bytes.Repeat([]byte("pail"), size/4)
			_, err := c.Upload(context.Background(), &s3.PutObjectInput{Bucket: aws.String("b"), Key: aws.String("k"), Body: bytes.NewReader(data)},
				TransferOptions{PartSize: 5 << 20, Concurrency: 2, Progress: func(n int64) { mu.Lock(); reports = append(reports, n); mu.Unlock() }})
			if err != nil {
				t.Fatal(err)
			}
			checkProgress(t, reports, int64(size))
			if len(reports) < size/maxProgressStep {
				t.Fatalf("only %d progress reports for %d bytes", len(reports), size)
			}
			for i := 1; i < len(reports); i++ {
				if reports[i]-reports[i-1] > maxProgressStep {
					t.Fatalf("progress jumped by %d", reports[i]-reports[i-1])
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if !bytes.Equal(stored["object"], data) {
				t.Fatalf("stored %d bytes, want %d", len(stored["object"]), len(data))
			}
		})
	}
}

// multipartHandler serves PutObject and multipart uploads into stored
// ("object" is the committed body). onRequest may inspect or fail a request.
func multipartHandler(t *testing.T, mu *sync.Mutex, stored map[string][]byte, onRequest func(r *http.Request, body []byte) bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		if onRequest != nil && !onRequest(r, body) {
			w.WriteHeader(500)
			fmt.Fprint(w, `<Error><Code>InternalError</Code><Message>injected</Message></Error>`)
			return
		}
		q := r.URL.Query()
		w.Header().Set("Content-Type", "application/xml")
		switch {
		case r.Method == "PUT" && q.Get("partNumber") != "":
			stored["part"+q.Get("partNumber")] = body
			w.Header().Set("ETag", `"etag-`+q.Get("partNumber")+`"`)
		case r.Method == "PUT":
			stored["object"] = body
			w.Header().Set("ETag", `"single"`)
			w.Header().Set("x-amz-version-id", "v-single")
		case r.Method == "POST" && q.Has("uploads"):
			fmt.Fprint(w, `<InitiateMultipartUploadResult><UploadId>up-1</UploadId></InitiateMultipartUploadResult>`)
		case r.Method == "POST" && q.Get("uploadId") != "":
			var all []byte
			for i := 1; ; i++ {
				p, ok := stored[fmt.Sprint("part", i)]
				if !ok {
					break
				}
				all = append(all, p...)
			}
			stored["object"] = all
			stored["complete"] = body
			w.Header().Set("x-amz-version-id", "v-multi")
			fmt.Fprint(w, `<CompleteMultipartUploadResult><Key>k</Key><ETag>"multi-2"</ETag></CompleteMultipartUploadResult>`)
		case r.Method == "DELETE" && q.Get("uploadId") != "":
			stored["aborted"] = []byte(r.Header.Get("x-amz-expected-bucket-owner") + "|" + r.Header.Get("x-amz-request-payer"))
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
		}
	}
}

func TestMultipartRetriedPartIsNotDoubleCounted(t *testing.T) {
	var mu sync.Mutex
	stored := map[string][]byte{}
	var reports []int64
	failed := false
	var seenAtRetry int64 = -1
	c := testClient(t, multipartHandler(t, &mu, stored, func(r *http.Request, body []byte) bool {
		if r.URL.Query().Get("partNumber") == "1" {
			if !failed {
				failed = true // the whole part was read, then the service failed
				return false
			}
			seenAtRetry = reports[len(reports)-1]
		}
		return true
	}))
	data := make([]byte, 6<<20)
	_, err := c.Upload(context.Background(), &s3.PutObjectInput{Bucket: aws.String("b"), Key: aws.String("k"), Body: bytes.NewReader(data)},
		TransferOptions{PartSize: 5 << 20, Concurrency: 1, Progress: func(n int64) { mu.Lock(); reports = append(reports, n); mu.Unlock() }})
	if err != nil {
		t.Fatal(err)
	}
	if !failed {
		t.Fatal("the fault was not injected")
	}
	// When the resent part 1 has been read in full, progress shows part 1
	// once; counting it twice would show the whole (clamped) 6 MiB before
	// part 2 started.
	if seenAtRetry != 5<<20 {
		t.Fatalf("progress after the resent part 1: %d", seenAtRetry)
	}
	checkProgress(t, reports, int64(len(data)))
}

func TestMultipartRequestFields(t *testing.T) {
	for _, mode := range []string{ChecksumWhenRequired, ChecksumWhenSupported} {
		t.Run(mode, func(t *testing.T) {
			var mu sync.Mutex
			stored := map[string][]byte{}
			headers := map[string]http.Header{}
			srv := multipartHandler(t, &mu, stored, func(r *http.Request, body []byte) bool {
				q := r.URL.Query()
				switch {
				case q.Has("uploads"):
					headers["create"] = r.Header.Clone()
				case q.Get("partNumber") == "1":
					headers["part"] = r.Header.Clone()
				case r.Method == "POST":
					headers["complete"] = r.Header.Clone()
				}
				return true
			})
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("partNumber") != "" {
					w.Header().Set("x-amz-checksum-crc32", "AAAAAA==")
				}
				srv(w, r)
			})
			c.reqChecksum = map[string]aws.RequestChecksumCalculation{ChecksumWhenRequired: aws.RequestChecksumCalculationWhenRequired, ChecksumWhenSupported: aws.RequestChecksumCalculationWhenSupported}[mode]
			in := &s3.PutObjectInput{
				Bucket: aws.String("b"), Key: aws.String("k"), Body: bytes.NewReader(make([]byte, 6<<20)),
				IfNoneMatch: aws.String("*"), ExpectedBucketOwner: aws.String("111122223333"),
				SSECustomerAlgorithm: aws.String("AES256"), SSECustomerKey: aws.String("a2V5"), SSECustomerKeyMD5: aws.String("bWQ1"),
				StorageClass: "STANDARD_IA", Tagging: aws.String("a=b"), Metadata: map[string]string{"pail-mtime": "1"}, ContentType: aws.String("text/plain"),
			}
			out, err := c.Upload(context.Background(), in, TransferOptions{PartSize: 5 << 20})
			if err != nil {
				t.Fatal(err)
			}
			if aws.ToString(out.VersionID) != "v-multi" || aws.ToString(out.ETag) != `"multi-2"` || len(out.CompletedParts) != 2 {
				t.Fatalf("output %+v", out)
			}
			mu.Lock()
			defer mu.Unlock()
			cr, part, done := headers["create"], headers["part"], headers["complete"]
			for name, want := range map[string]string{"X-Amz-Storage-Class": "STANDARD_IA", "X-Amz-Tagging": "a=b", "X-Amz-Meta-Pail-Mtime": "1", "Content-Type": "text/plain", "X-Amz-Server-Side-Encryption-Customer-Algorithm": "AES256", "X-Amz-Expected-Bucket-Owner": "111122223333"} {
				if cr.Get(name) != want {
					t.Errorf("create %s=%q", name, cr.Get(name))
				}
			}
			if part.Get("X-Amz-Server-Side-Encryption-Customer-Key") != "a2V5" || part.Get("X-Amz-Expected-Bucket-Owner") != "111122223333" {
				t.Errorf("part headers %v", part)
			}
			if done.Get("If-None-Match") != "*" || done.Get("X-Amz-Server-Side-Encryption-Customer-Key") != "a2V5" || done.Get("X-Amz-Mp-Object-Size") != fmt.Sprint(6<<20) {
				t.Errorf("complete headers %v", done)
			}
			partChecksum := part.Get("X-Amz-Checksum-Crc32") != "" || strings.Contains(strings.ToLower(part.Get("X-Amz-Trailer")), "crc32")
			completeChecksum := strings.Contains(string(stored["complete"]), "<ChecksumCRC32>AAAAAA==</ChecksumCRC32>")
			if mode == ChecksumWhenSupported {
				if cr.Get("X-Amz-Checksum-Algorithm") != "CRC32" || !partChecksum || !completeChecksum {
					t.Errorf("CRC32 not carried: create %q, part %v, complete %s", cr.Get("X-Amz-Checksum-Algorithm"), part, stored["complete"])
				}
			} else if cr.Get("X-Amz-Checksum-Algorithm") != "" || partChecksum || completeChecksum {
				t.Errorf("checksums sent although only required ones should be: create %v part %v complete %s", cr, part, stored["complete"])
			}
		})
	}
}

func TestSinglePutCarriesConditionsAndVersion(t *testing.T) {
	var mu sync.Mutex
	stored := map[string][]byte{}
	var hdr http.Header
	c := testClient(t, multipartHandler(t, &mu, stored, func(r *http.Request, _ []byte) bool { hdr = r.Header.Clone(); return true }))
	body := bytes.NewReader([]byte("0123456789"))
	_, _ = body.Seek(4, io.SeekStart) // the body is sent from its current position
	out, err := c.Upload(context.Background(), &s3.PutObjectInput{Bucket: aws.String("b"), Key: aws.String("k"), Body: body, IfMatch: aws.String(`"e"`)}, TransferOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if aws.ToString(out.VersionID) != "v-single" || hdr.Get("If-Match") != `"e"` || string(stored["object"]) != "456789" {
		t.Fatalf("version %q, If-Match %q, body %q", aws.ToString(out.VersionID), hdr.Get("If-Match"), stored["object"])
	}
}

// onlyReader hides every interface but io.Reader.
type onlyReader struct{ io.Reader }

func TestStreamedUpload(t *testing.T) {
	data := make([]byte, 11<<20+7)
	for i := range data {
		data[i] = byte(i * 31)
	}
	for _, known := range []bool{true, false} {
		t.Run(fmt.Sprint("known=", known), func(t *testing.T) {
			var mu sync.Mutex
			stored := map[string][]byte{}
			c := testClient(t, multipartHandler(t, &mu, stored, nil))
			in := &s3.PutObjectInput{Bucket: aws.String("b"), Key: aws.String("k"), Body: onlyReader{bytes.NewReader(data)}}
			if known {
				in.ContentLength = aws.Int64(int64(len(data)))
			}
			var reports []int64
			_, err := c.Upload(context.Background(), in, TransferOptions{PartSize: 5 << 20, Concurrency: 3, Progress: func(n int64) { mu.Lock(); reports = append(reports, n); mu.Unlock() }})
			if err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if !bytes.Equal(stored["object"], data) || len(stored["part3"]) != 1<<20+7 {
				t.Fatalf("stored %d bytes", len(stored["object"]))
			}
			if reports[len(reports)-1] != int64(len(data)) {
				t.Fatalf("progress ended at %d", reports[len(reports)-1])
			}
		})
	}
	t.Run("short", func(t *testing.T) {
		var mu sync.Mutex
		stored := map[string][]byte{}
		c := testClient(t, multipartHandler(t, &mu, stored, nil))
		for _, n := range []int{100, 7 << 20} {
			_, err := c.Upload(context.Background(), &s3.PutObjectInput{Bucket: aws.String("b"), Key: aws.String("k"), Body: onlyReader{bytes.NewReader(make([]byte, n))}, ContentLength: aws.Int64(int64(n) + 1)}, TransferOptions{PartSize: 5 << 20})
			if err == nil || !strings.Contains(err.Error(), "the body ended after") {
				t.Fatalf("%d bytes for %d: %v", n, n+1, err)
			}
		}
		mu.Lock()
		defer mu.Unlock()
		if _, ok := stored["object"]; ok {
			t.Fatal("a short body was committed")
		}
		if _, ok := stored["aborted"]; !ok {
			t.Fatal("the multipart upload of a short body was not aborted")
		}
	})
}

func TestAbortKeepsOwnerAndPayer(t *testing.T) {
	var mu sync.Mutex
	stored := map[string][]byte{}
	c := testClient(t, multipartHandler(t, &mu, stored, func(r *http.Request, _ []byte) bool { return r.URL.Query().Get("partNumber") != "2" }))
	_, err := c.Upload(context.Background(), &s3.PutObjectInput{Bucket: aws.String("b"), Key: aws.String("k"), Body: bytes.NewReader(make([]byte, 6<<20)), ExpectedBucketOwner: aws.String("o"), RequestPayer: "requester"}, TransferOptions{PartSize: 5 << 20})
	if err == nil {
		t.Fatal("a failed part did not fail the upload")
	}
	mu.Lock()
	defer mu.Unlock()
	if string(stored["aborted"]) != "o|requester" {
		t.Fatalf("abort headers %q", stored["aborted"])
	}
	if _, ok := stored["object"]; ok {
		t.Fatal("completed after a failed part")
	}
}

func TestPartSizeGrowsToTenThousandParts(t *testing.T) {
	u := &uploader{partSize: 5 << 20}
	u.fitParts(100 << 30) // 100 GiB / 5 MiB = 20480 parts
	if (100<<30+u.partSize-1)/u.partSize > maxUploadParts {
		t.Fatalf("part size %d needs too many parts", u.partSize)
	}
}
