package storage

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// transferCtx is what the S3 client's finalize middleware puts on the
// request of a call made with TransferContext.
func transferCtx() context.Context {
	return context.WithValue(context.Background(), transferAttemptKey{}, true)
}

func TestBandwidthLimitThrottlesBodies(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), 96<<10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = w.Write(payload)
	}))
	defer srv.Close()
	hc := NewHTTPClient(false, "")
	measure := func(ctx context.Context) (up, down time.Duration) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		resp, err := hc.Do(req)
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
	if up, down := measure(transferCtx()); up > 2*time.Second || down > 2*time.Second {
		t.Fatalf("unlimited transfer is slow: %v %v", up, down)
	}
	// 64 KiB/s with a 16 KiB burst: 96 KiB takes at least about 1.25 s each way.
	SetBandwidth(64<<10, 64<<10)
	defer SetBandwidth(0, 0)
	up, down := measure(transferCtx())
	if up < time.Second || down < time.Second {
		t.Fatalf("limit not applied: upload %v, download %v", up, down)
	}
	// Requests that do not move an object body are never throttled.
	if up, down := measure(context.Background()); up > time.Second || down > time.Second {
		t.Fatalf("an unmarked request was throttled: upload %v, download %v", up, down)
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

func TestRateChangeWakesSleepers(t *testing.T) {
	l := &rateLimiter{}
	l.setRate(1024)
	done := make(chan time.Duration)
	go func() {
		start := time.Now()
		_ = l.take(context.Background(), 1<<20) // about 17 minutes at 1 KiB/s
		done <- time.Since(start)
	}()
	time.Sleep(50 * time.Millisecond)
	l.setRate(0)
	select {
	case d := <-done:
		if d > 2*time.Second {
			t.Fatalf("woke after %v", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a reader sleeping under the old limit was not woken by the change")
	}
}

// endless is a body that never ends.
type endless struct{}

func (endless) Read(p []byte) (int, error) { return len(p), nil }
func (endless) Close() error               { return nil }

func TestThrottleTurnsStayShortWithManyStreams(t *testing.T) {
	l := &rateLimiter{}
	l.setRate(1 << 20)
	const streams = 40
	bodies := make([]*limitedBody, streams)
	for i := range bodies {
		bodies[i] = newLimitedBody(endless{}, context.Background(), l)
	}
	var longest atomic.Int64
	var total atomic.Int64
	var wg sync.WaitGroup
	stop := time.Now().Add(1500 * time.Millisecond)
	for _, b := range bodies {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, 64<<10)
			for time.Now().Before(stop) {
				start := time.Now()
				n, _ := b.Read(buf)
				total.Add(int64(n))
				d := int64(time.Since(start))
				for prev := longest.Load(); d > prev && !longest.CompareAndSwap(prev, d); prev = longest.Load() {
				}
			}
		}()
	}
	wg.Wait()
	for _, b := range bodies {
		_ = b.Close()
	}
	if l.active.Load() != 0 {
		t.Fatalf("active bodies after close: %d", l.active.Load())
	}
	// A fixed 32 KiB read would make the last of 40 readers wait about 1.25 s.
	if d := time.Duration(longest.Load()); d > 450*time.Millisecond {
		t.Fatalf("a throttled read waited %v", d)
	}
	// Long-run accuracy: 1.5 s at 1 MiB/s plus the burst, with slack.
	if got := total.Load(); got > 2<<20 || got < 1<<20 {
		t.Fatalf("moved %d bytes in 1.5 s at 1 MiB/s", got)
	}
}
