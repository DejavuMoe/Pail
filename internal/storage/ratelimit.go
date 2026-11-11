package storage

import (
	"context"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Bandwidth limits, shared by every client of the process: the upload limit
// throttles request bodies, the download limit response bodies. 0 means no
// limit. A change applies to requests that start afterwards; every part of a
// multipart transfer is a request of its own, so a running transfer follows
// within one part.
var (
	upLimit   = &rateLimiter{}
	downLimit = &rateLimiter{}
)

// SetBandwidth sets the upload and download limits in bytes per second.
func SetBandwidth(upBytesPerSec, downBytesPerSec int64) {
	upLimit.setRate(upBytesPerSec)
	downLimit.setRate(downBytesPerSec)
}

// rateLimiter is a token bucket. Readers take what they read and sleep while
// the bucket is in debt.
type rateLimiter struct {
	rate   atomic.Int64 // bytes per second, 0 = unlimited
	mu     sync.Mutex
	tokens float64
	last   time.Time
}

func (l *rateLimiter) setRate(bps int64) {
	if bps < 0 {
		bps = 0
	}
	l.mu.Lock()
	l.rate.Store(bps)
	l.tokens, l.last = 0, time.Time{}
	l.mu.Unlock()
}

// burst is how far ahead the bucket may fill: a quarter of a second, at least 16 KiB.
func burst(rate int64) float64 { return max(float64(rate)/4, 16<<10) }

// take accounts for n bytes and sleeps until the limit allows them.
func (l *rateLimiter) take(ctx context.Context, n int) error {
	rate := l.rate.Load()
	if rate <= 0 || n <= 0 {
		return nil
	}
	l.mu.Lock()
	now := time.Now()
	if l.last.IsZero() {
		l.last = now
	}
	l.tokens = min(l.tokens+now.Sub(l.last).Seconds()*float64(rate), burst(rate))
	l.last = now
	l.tokens -= float64(n)
	debt := -l.tokens
	l.mu.Unlock()
	if debt <= 0 {
		return nil
	}
	t := time.NewTimer(time.Duration(debt / float64(rate) * float64(time.Second)))
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// limitedBody throttles a request or response body.
type limitedBody struct {
	io.ReadCloser
	ctx context.Context
	l   *rateLimiter
}

// maxThrottledRead keeps each wait short, so throttled streams stay smooth.
const maxThrottledRead = 32 << 10

func (b *limitedBody) Read(p []byte) (int, error) {
	if len(p) > maxThrottledRead && b.l.rate.Load() > 0 {
		p = p[:maxThrottledRead]
	}
	n, err := b.ReadCloser.Read(p)
	if werr := b.l.take(b.ctx, n); werr != nil && err == nil {
		err = werr
	}
	return n, err
}

// throttledTransport applies the bandwidth limits to object bodies.
type throttledTransport struct{ base http.RoundTripper }

func (t throttledTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil && req.Body != http.NoBody && upLimit.rate.Load() > 0 {
		r := req.Clone(req.Context())
		r.Body = &limitedBody{ReadCloser: req.Body, ctx: req.Context(), l: upLimit}
		req = r
	}
	resp, err := t.base.RoundTrip(req)
	if err == nil && resp.Body != nil && downLimit.rate.Load() > 0 {
		resp.Body = &limitedBody{ReadCloser: resp.Body, ctx: req.Context(), l: downLimit}
	}
	return resp, err
}
