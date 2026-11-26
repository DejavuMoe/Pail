package storage

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// Bandwidth limits, shared by every client of the process: the upload limit
// throttles the request bodies of object transfers (see TransferContext), the
// download limit their response bodies. Listings, HEAD, deletes, STS and every
// other request are never throttled. 0 means no limit. A change applies at
// once, also to reads waiting under the old limit.
var (
	upLimit   = &rateLimiter{}
	downLimit = &rateLimiter{}
)

// SetBandwidth sets the upload and download limits in bytes per second.
func SetBandwidth(upBytesPerSec, downBytesPerSec int64) {
	upLimit.setRate(upBytesPerSec)
	downLimit.setRate(downBytesPerSec)
}

const (
	// maxThrottleWait is the turn every throttled body gets: one read takes
	// at most rate×maxThrottleWait shared among the active bodies, so no
	// wait is much longer than this (unless minThrottledRead dominates).
	maxThrottleWait  = 250 * time.Millisecond
	maxThrottledRead = 32 << 10
	minThrottledRead = 1 << 10
)

// rateLimiter is a token bucket. Readers take what they read and sleep while
// the bucket is in debt; a rate change wakes them.
type rateLimiter struct {
	rate   atomic.Int64 // bytes per second, 0 = unlimited
	active atomic.Int64 // open throttled bodies
	mu     sync.Mutex
	tokens float64
	last   time.Time
	// changed is closed (and replaced) by setRate.
	changed chan struct{}
}

func (l *rateLimiter) setRate(bps int64) {
	if bps < 0 {
		bps = 0
	}
	l.mu.Lock()
	l.rate.Store(bps)
	l.tokens, l.last = 0, time.Time{}
	if l.changed != nil {
		close(l.changed)
	}
	l.changed = make(chan struct{})
	l.mu.Unlock()
}

// burst is how far ahead the bucket may fill: a quarter of a second, at least 16 KiB.
func burst(rate int64) float64 { return max(float64(rate)/4, 16<<10) }

// quantum is the most one read may take at rate, so that every active body
// gets a turn within about maxThrottleWait.
func (l *rateLimiter) quantum(rate int64) int {
	share := int64(float64(rate)*maxThrottleWait.Seconds()) / max(l.active.Load(), 1)
	return int(min(max(share, minThrottledRead), maxThrottledRead))
}

// take accounts for n bytes and sleeps until the limit allows them. A rate
// change ends the sleep: the bucket restarts at the new rate, so the next
// read is held to it.
func (l *rateLimiter) take(ctx context.Context, n int) error {
	if n <= 0 || l.rate.Load() <= 0 {
		return nil
	}
	l.mu.Lock()
	rate := l.rate.Load()
	if rate <= 0 {
		l.mu.Unlock()
		return nil
	}
	now := time.Now()
	if l.last.IsZero() {
		l.last = now
	}
	l.tokens = min(l.tokens+now.Sub(l.last).Seconds()*float64(rate), burst(rate))
	l.last = now
	l.tokens -= float64(n)
	debt := -l.tokens
	changed := l.changed
	l.mu.Unlock()
	if debt <= 0 {
		return nil
	}
	t := time.NewTimer(time.Duration(debt / float64(rate) * float64(time.Second)))
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-changed:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// limitedBody throttles a request or response body. It counts as active from
// creation until EOF, an error or Close.
type limitedBody struct {
	rc   io.ReadCloser
	ctx  context.Context
	l    *rateLimiter
	left atomic.Bool
}

func newLimitedBody(rc io.ReadCloser, ctx context.Context, l *rateLimiter) *limitedBody {
	l.active.Add(1)
	return &limitedBody{rc: rc, ctx: ctx, l: l}
}

func (b *limitedBody) Read(p []byte) (int, error) {
	if rate := b.l.rate.Load(); rate > 0 {
		if q := b.l.quantum(rate); len(p) > q {
			p = p[:q]
		}
	}
	n, err := b.rc.Read(p)
	if err != nil {
		b.leave()
	}
	if werr := b.l.take(b.ctx, n); werr != nil && err == nil {
		err = werr
	}
	return n, err
}

func (b *limitedBody) Close() error {
	b.leave()
	return b.rc.Close()
}

func (b *limitedBody) leave() {
	if b.left.CompareAndSwap(false, true) {
		b.l.active.Add(-1)
	}
}
