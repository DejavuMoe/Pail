package storage

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// noRangeServer answers every GET with the whole object, as some NAS firmware does.
func noRangeServer(t *testing.T, data []byte, ranges *[]string) *Client {
	t.Helper()
	var mu sync.Mutex
	return testClient(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*ranges = append(*ranges, r.Header.Get("Range"))
		mu.Unlock()
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		_, _ = w.Write(data)
	})
}

func TestDownloadFallsBackWhenRangeIsIgnored(t *testing.T) {
	data := make([]byte, 11<<20+5)
	for i := range data {
		data[i] = byte(i * 13)
	}
	var ranges []string
	c := noRangeServer(t, data, &ranges)
	dst := &memoryWriterAt{data: make([]byte, len(data))}
	var last int64
	n, err := c.Download(context.Background(), &s3.GetObjectInput{Bucket: aws.String("b"), Key: aws.String("k")}, dst,
		TransferOptions{PartSize: 5 << 20, Concurrency: 3, Size: int64(len(data)), Progress: func(v int64) { last = v }})
	if err != nil || n != int64(len(data)) || !bytes.Equal(dst.data, data) {
		t.Fatalf("download: n=%d err=%v", n, err)
	}
	if last != int64(len(data)) {
		t.Fatalf("progress %d, want %d", last, len(data))
	}
	if len(ranges) != 2 || ranges[0] != "bytes=0-5242879" || ranges[1] != "" {
		t.Fatalf("requests = %q, want one ranged probe and one plain GET", ranges)
	}
}

func TestDownloadSmallObjectWithoutContentRangeIsNotRetried(t *testing.T) {
	data := []byte("small object")
	var ranges []string
	c := noRangeServer(t, data, &ranges)
	dst := &memoryWriterAt{data: make([]byte, len(data))}
	n, err := c.Download(context.Background(), &s3.GetObjectInput{Bucket: aws.String("b"), Key: aws.String("k")}, dst, TransferOptions{PartSize: 5 << 20})
	if err != nil || n != int64(len(data)) || !bytes.Equal(dst.data, data) {
		t.Fatalf("download: n=%d err=%v", n, err)
	}
	if len(ranges) != 1 {
		t.Fatalf("requests = %q, want one", ranges)
	}
}

func TestDownloadCanceledBeforeFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	data := make([]byte, 11<<20)
	var mu sync.Mutex
	requests := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		cancel()
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		_, _ = w.Write(data)
	})
	dst := &memoryWriterAt{data: make([]byte, len(data))}
	_, err := c.Download(ctx, &s3.GetObjectInput{Bucket: aws.String("b"), Key: aws.String("k")}, dst, TransferOptions{PartSize: 5 << 20, Concurrency: 1})
	if err == nil {
		t.Fatal("canceled download succeeded")
	}
	if errors.Is(err, errRangeIgnored) {
		t.Fatalf("canceled download reported %v", err)
	}
	if requests != 1 {
		t.Fatalf("%d requests, want 1", requests)
	}
}
