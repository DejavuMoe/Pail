package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// directClient is a client on the production transport (bandwidth limits and
// stall watchdog), talking to h.
func directClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(context.Background(), Config{Endpoint: srv.URL, Region: "us-east-1", AccessKeyID: "a", SecretAccessKey: "s", UsePathStyle: true, ProxyURL: DirectConnection})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func shortStall(t *testing.T, d time.Duration) {
	old := stallTimeout
	stallTimeout = d
	t.Cleanup(func() { stallTimeout = old })
}

// releaseOnCleanup returns a channel closed at cleanup. Cleanups run last
// first: call it before directClient only when handlers also leave on their
// request context, otherwise the server's Close waits for them forever.
func releaseOnCleanup(t *testing.T) chan struct{} {
	ch := make(chan struct{})
	t.Cleanup(func() { close(ch) })
	return ch
}

func serveRange(w http.ResponseWriter, r *http.Request, data []byte) (start, end int) {
	end = len(data) - 1
	if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end); err == nil {
		end = min(end, len(data)-1)
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)))
		w.Header().Set("Content-Length", fmt.Sprint(end-start+1))
		w.WriteHeader(http.StatusPartialContent)
	} else {
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
	}
	return start, end
}

func TestStalledDownloadIsRetried(t *testing.T) {
	shortStall(t, 200*time.Millisecond)
	data := bytes.Repeat([]byte("0123456789abcdef"), 16<<10) // 256 KiB
	release := releaseOnCleanup(t)
	var gets atomic.Int32
	c := directClient(t, func(w http.ResponseWriter, r *http.Request) {
		start, end := serveRange(w, r, data)
		if gets.Add(1) == 1 {
			_, _ = w.Write(data[start : start+64<<10])
			w.(http.Flusher).Flush()
			select { // stop sending, keep the connection open
			case <-release:
			case <-r.Context().Done():
			}
			return
		}
		_, _ = w.Write(data[start : end+1])
	})
	dst := &memoryWriterAt{data: make([]byte, len(data))}
	start := time.Now()
	n, err := c.Download(context.Background(), &s3.GetObjectInput{Bucket: aws.String("b"), Key: aws.String("k")}, dst, TransferOptions{Size: int64(len(data))})
	if err != nil || n != int64(len(data)) || !bytes.Equal(dst.data, data) {
		t.Fatalf("download after a stall: n=%d err=%v", n, err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("recovery took %v", d)
	}
	if gets.Load() < 2 {
		t.Fatalf("the stalled body was not retried (%d GETs)", gets.Load())
	}
}

func TestStalledDownloadFails(t *testing.T) {
	shortStall(t, 150*time.Millisecond)
	data := make([]byte, 128<<10)
	release := releaseOnCleanup(t)
	c := directClient(t, func(w http.ResponseWriter, r *http.Request) {
		start, _ := serveRange(w, r, data)
		_, _ = w.Write(data[start : start+1024])
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	dst := &memoryWriterAt{data: make([]byte, len(data))}
	_, err := c.Download(context.Background(), &s3.GetObjectInput{Bucket: aws.String("b"), Key: aws.String("k")}, dst, TransferOptions{Size: int64(len(data))})
	if err == nil || !strings.Contains(err.Error(), "the connection stalled: no data moved for 0.15 seconds") {
		t.Fatalf("err=%v", err)
	}
}

func TestStalledUploadIsRetried(t *testing.T) {
	shortStall(t, 300*time.Millisecond)
	const size = 32 << 20 // more than the loopback socket buffers hold
	release := make(chan struct{})
	var puts atomic.Int32
	var got atomic.Int64
	c := directClient(t, func(w http.ResponseWriter, r *http.Request) {
		if puts.Add(1) == 1 {
			_, _ = io.CopyN(io.Discard, r.Body, 1<<20)
			<-release // stop reading; the connection stays open
			return
		}
		n, _ := io.Copy(io.Discard, r.Body)
		got.Store(n)
		w.Header().Set("ETag", `"ok"`)
	})
	t.Cleanup(func() { close(release) }) // runs before the server closes
	var mu sync.Mutex
	var reports []int64
	start := time.Now()
	_, err := c.Upload(context.Background(), &s3.PutObjectInput{Bucket: aws.String("b"), Key: aws.String("k"), Body: bytes.NewReader(make([]byte, size))},
		TransferOptions{PartSize: 64 << 20, Progress: func(n int64) { mu.Lock(); reports = append(reports, n); mu.Unlock() }})
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 20*time.Second {
		t.Fatalf("recovery took %v", d)
	}
	if puts.Load() != 2 || got.Load() != size {
		t.Fatalf("puts=%d, second attempt read %d bytes", puts.Load(), got.Load())
	}
	checkProgress(t, reports, size)
}

func TestStalledUploadError(t *testing.T) {
	shortStall(t, 100*time.Millisecond)
	release := make(chan struct{})
	c := directClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.CopyN(io.Discard, r.Body, 1<<20)
		<-release
	})
	t.Cleanup(func() { close(release) }) // runs before the server closes
	_, err := c.Upload(context.Background(), &s3.PutObjectInput{Bucket: aws.String("b"), Key: aws.String("k"), Body: bytes.NewReader(make([]byte, 32<<20))}, TransferOptions{PartSize: 64 << 20})
	var stall *StallError
	if !errors.As(err, &stall) || !strings.Contains(err.Error(), "stalled") {
		t.Fatalf("err=%v", err)
	}
}

func TestSlowTransfersDoNotTripTheWatchdog(t *testing.T) {
	shortStall(t, 300*time.Millisecond)
	data := bytes.Repeat([]byte("z"), 48<<10)
	c := directClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("ETag", `"ok"`)
			return
		}
		start, end := serveRange(w, r, data)
		// Trickle: 4 KiB every 100 ms, well under the stall timeout.
		for off := start; off <= end; off += 4 << 10 {
			_, _ = w.Write(data[off:min(off+4<<10, end+1)])
			w.(http.Flusher).Flush()
			time.Sleep(100 * time.Millisecond)
		}
	})
	dst := &memoryWriterAt{data: make([]byte, len(data))}
	if _, err := c.Download(context.Background(), &s3.GetObjectInput{Bucket: aws.String("b"), Key: aws.String("k")}, dst, TransferOptions{Size: int64(len(data))}); err != nil {
		t.Fatalf("trickled download: %v", err)
	}
	// Throttled to 32 KiB/s both ways, each transfer runs for more than a
	// second; the limiter's sleeps are not stalls.
	SetBandwidth(32<<10, 32<<10)
	defer SetBandwidth(0, 0)
	start := time.Now()
	if _, err := c.Upload(context.Background(), &s3.PutObjectInput{Bucket: aws.String("b"), Key: aws.String("k"), Body: bytes.NewReader(make([]byte, 64<<10))}, TransferOptions{}); err != nil {
		t.Fatalf("throttled upload: %v", err)
	}
	if d := time.Since(start); d < time.Second {
		t.Fatalf("upload was not throttled: %v", d)
	}
}

func TestOnlyTransfersAreThrottled(t *testing.T) {
	data := make([]byte, 64<<10)
	listing := `<?xml version="1.0" encoding="UTF-8"?><ListBucketResult><Name>b</Name><KeyCount>1</KeyCount><IsTruncated>false</IsTruncated><Contents><Key>` + strings.Repeat("k", 900) + `</Key><Size>1</Size></Contents>` + strings.Repeat("<!-- padding -->", 4000) + `</ListBucketResult>`
	c := directClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("list-type") == "2" {
			w.Header().Set("Content-Type", "application/xml")
			_, _ = io.WriteString(w, listing)
			return
		}
		start, end := serveRange(w, r, data)
		_, _ = w.Write(data[start : end+1])
	})
	SetBandwidth(16<<10, 16<<10)
	defer SetBandwidth(0, 0)
	start := time.Now()
	if _, err := c.S3().ListObjectsV2(context.Background(), &s3.ListObjectsV2Input{Bucket: aws.String("b")}); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("a %d-byte listing took %v under a 16 KiB/s limit", len(listing), d)
	}
	start = time.Now()
	out, err := c.S3().GetObject(TransferContext(context.Background()), &s3.GetObjectInput{Bucket: aws.String("b"), Key: aws.String("k")})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, out.Body)
	out.Body.Close()
	if d := time.Since(start); d < 2*time.Second {
		t.Fatalf("a marked 64 KiB GET took %v under a 16 KiB/s limit", d)
	}
}

// checkProgress asserts monotonic reports that end at size and never pass it.
func checkProgress(t *testing.T, reports []int64, size int64) {
	t.Helper()
	if len(reports) == 0 || reports[len(reports)-1] != size {
		t.Fatalf("progress did not end at %d: %d reports, last %v", size, len(reports), reports[max(0, len(reports)-1):])
	}
	for i, v := range reports {
		if v > size || i > 0 && v <= reports[i-1] {
			t.Fatalf("report %d = %d after %d (size %d)", i, v, reports[max(0, i-1)], size)
		}
	}
}
