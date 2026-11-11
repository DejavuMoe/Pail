package storage

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBandwidthLimitThrottlesBodies(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), 96<<10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = w.Write(payload)
	}))
	defer srv.Close()
	hc := NewHTTPClient(false, "")
	measure := func() (up, down time.Duration) {
		start := time.Now()
		resp, err := hc.Post(srv.URL, "application/octet-stream", bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		up = time.Since(start)
		start = time.Now()
		n, err := io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if err != nil || n != int64(len(payload)) {
			t.Fatalf("read %d: %v", n, err)
		}
		return up, time.Since(start)
	}
	if up, down := measure(); up > 2*time.Second || down > 2*time.Second {
		t.Fatalf("unlimited transfer is slow: %v %v", up, down)
	}
	// 64 KiB/s with a 16 KiB burst: 96 KiB takes at least about 1.25 s each way.
	SetBandwidth(64<<10, 64<<10)
	defer SetBandwidth(0, 0)
	up, down := measure()
	if up < time.Second || down < time.Second {
		t.Fatalf("limit not applied: upload %v, download %v", up, down)
	}
}

func TestRateLimiterHonoursCancel(t *testing.T) {
	l := &rateLimiter{}
	l.setRate(1024)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := l.take(ctx, 1<<20); err == nil {
		t.Fatal("a canceled wait returned nil")
	}
	l.setRate(0)
	if err := l.take(context.Background(), 1<<30); err != nil {
		t.Fatal(err)
	}
}
