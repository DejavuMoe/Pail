package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type memoryWriterAt struct {
	mu   sync.Mutex
	data []byte
}

func (w *memoryWriterAt) WriteAt(p []byte, off int64) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return copy(w.data[int(off):], p), nil
}

func TestCounterIsMonotonicAndClamped(t *testing.T) {
	var got []int64
	c := &counter{size: 20, report: func(n int64) { got = append(got, n) }}
	for _, n := range []int64{10, 0, -3, 5, 5, 8, 4} { // retried range => total exceeds size
		c.add(n)
	}
	if fmt.Sprint(got) != "[10 15 20]" {
		t.Fatalf("progress=%v", got)
	}
}

func TestCounterConcurrent(t *testing.T) {
	var last atomic.Int64
	c := &counter{report: func(n int64) {
		for {
			p := last.Load()
			if n < p || last.CompareAndSwap(p, n) {
				return
			}
		}
	}}
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				c.add(1)
			}
		}()
	}
	wg.Wait()
	if last.Load() != 64000 {
		t.Fatalf("lost increments: %d", last.Load())
	}
}

func TestTransferOptionsValidation(t *testing.T) {
	for _, o := range []TransferOptions{{PartSize: 1024}, {PartSize: 6 << 30}, {Concurrency: -1}, {Concurrency: 129}} {
		if o.validate() == nil {
			t.Errorf("%+v accepted", o)
		}
	}
	if (TransferOptions{PartSize: 5 << 20, Concurrency: 128}).validate() != nil {
		t.Error("valid options rejected")
	}
}

func TestUploadProgressOnSuccessfulParts(t *testing.T) {
	var progress int64
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { w.Header().Set("ETag", `"test"`) })
	_, err := c.Upload(context.Background(), &s3.PutObjectInput{Bucket: aws.String("bucket"), Key: aws.String("key"), Body: bytes.NewReader([]byte("hello"))}, TransferOptions{Progress: func(n int64) { progress = n }})
	if err != nil {
		t.Fatal(err)
	}
	if progress != 5 {
		t.Fatalf("progress=%d, want 5", progress)
	}
}

func TestMultipartUploadAndAbort(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			var mu sync.Mutex
			parts := map[string]int{}
			aborted, completed := false, false
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				q := r.URL.Query()
				w.Header().Set("Content-Type", "application/xml")
				switch {
				case r.Method == "POST" && q.Has("uploads"):
					fmt.Fprint(w, `<InitiateMultipartUploadResult><UploadId>upload-1</UploadId></InitiateMultipartUploadResult>`)
				case r.Method == "PUT" && q.Get("partNumber") != "":
					if fail {
						w.WriteHeader(403)
						fmt.Fprint(w, `<Error><Code>AccessDenied</Code><Message>denied</Message></Error>`)
						return
					}
					b, _ := io.ReadAll(r.Body)
					parts[q.Get("partNumber")] = len(b)
					w.Header().Set("ETag", `"part-etag"`)
				case r.Method == "POST" && q.Get("uploadId") != "":
					completed = true
					fmt.Fprint(w, `<CompleteMultipartUploadResult><ETag>"c"</ETag></CompleteMultipartUploadResult>`)
				case r.Method == "DELETE" && q.Get("uploadId") != "":
					aborted = true
					w.WriteHeader(204)
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
				}
			})
			var last int64
			_, err := c.Upload(context.Background(), &s3.PutObjectInput{Bucket: aws.String("bucket"), Key: aws.String("large"), Body: bytes.NewReader(make([]byte, 6*1024*1024))}, TransferOptions{PartSize: 5 << 20, Concurrency: 1, Progress: func(n int64) { last = n }})
			if fail {
				if err == nil || !aborted || completed {
					t.Fatalf("failed upload not aborted: err=%v aborted=%v complete=%v", err, aborted, completed)
				}
			} else if err != nil || !completed || aborted || parts["1"] != 5<<20 || parts["2"] != 1<<20 || last != 6<<20 {
				t.Fatalf("bad multipart: %v parts=%v progress=%d", err, parts, last)
			}
		})
	}
}

func TestCancelledUploadStillAborts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var aborted atomic.Bool
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		w.Header().Set("Content-Type", "application/xml")
		switch {
		case r.Method == "POST" && q.Has("uploads"):
			fmt.Fprint(w, `<InitiateMultipartUploadResult><UploadId>cancel-id</UploadId></InitiateMultipartUploadResult>`)
		case r.Method == "PUT":
			cancel()
			w.WriteHeader(403)
			fmt.Fprint(w, `<Error><Code>AccessDenied</Code><Message>cancelled</Message></Error>`)
		case r.Method == "DELETE":
			aborted.Store(true)
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
		}
	})
	_, err := c.Upload(ctx, &s3.PutObjectInput{Bucket: aws.String("bucket"), Key: aws.String("key"), Body: bytes.NewReader(make([]byte, 6<<20))}, TransferOptions{Concurrency: 1})
	if err == nil || !errors.Is(ctx.Err(), context.Canceled) || !aborted.Load() {
		t.Fatalf("cancel did not clean multipart: %v aborted=%v", err, aborted.Load())
	}
}

func TestDownloadRangedRoundTrip(t *testing.T) {
	data := make([]byte, 12<<20+123)
	for i := range data {
		data[i] = byte(i * 7)
	}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var start, end int
		if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end); err != nil {
			t.Errorf("unranged request %q", r.Header.Get("Range"))
			return
		}
		end = min(end, len(data)-1)
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)))
		w.Header().Set("Content-Length", strconv.Itoa(end-start+1))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(data[start : end+1])
	})
	dst := &memoryWriterAt{data: make([]byte, len(data))}
	var last int64
	n, err := c.Download(context.Background(), &s3.GetObjectInput{Bucket: aws.String("b"), Key: aws.String("k")}, dst, TransferOptions{PartSize: 5 << 20, Concurrency: 3, Size: int64(len(data)), Progress: func(v int64) {
		if v > atomic.LoadInt64(&last) {
			atomic.StoreInt64(&last, v)
		}
	}})
	if err != nil || n != int64(len(data)) || !bytes.Equal(dst.data, data) {
		t.Fatalf("download wrong: n=%d err=%v", n, err)
	}
	if last != int64(len(data)) {
		t.Fatalf("progress %d, want %d", last, len(data))
	}
	if _, err = c.Download(context.Background(), nil, dst, TransferOptions{}); err == nil || !strings.Contains(err.Error(), "requires") {
		t.Fatalf("nil input accepted: %v", err)
	}
}
