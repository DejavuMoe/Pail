package storage

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	smithymiddleware "github.com/aws/smithy-go/middleware"
)

// stallTimeout is how long an object transfer may move no body bytes before
// its HTTP attempt is abandoned (tests shorten it).
var stallTimeout = 60 * time.Second

type transferKey struct{}
type transferAttemptKey struct{}

// TransferContext marks ctx for S3 calls that move an object body (GetObject
// of the contents, PutObject, UploadPart). Only such requests follow the
// bandwidth limits and the stall watchdog. Upload and Download mark their own
// requests; a caller streaming an object through Client.S3() marks the call.
func TransferContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, transferKey{}, true)
}

// markTransferAttempt runs last in the finalize step, after the signer has
// fetched credentials, so the mark reaches the S3 request but not an STS or
// other credential request made on the same context.
var markTransferAttempt = smithymiddleware.FinalizeMiddlewareFunc("PailTransferMark",
	func(ctx context.Context, in smithymiddleware.FinalizeInput, next smithymiddleware.FinalizeHandler) (smithymiddleware.FinalizeOutput, smithymiddleware.Metadata, error) {
		if ctx.Value(transferKey{}) != nil {
			ctx = context.WithValue(ctx, transferAttemptKey{}, true)
		}
		return next.HandleFinalize(ctx, in)
	})

func addTransferMark(s *smithymiddleware.Stack) error {
	return s.Finalize.Add(markTransferAttempt, smithymiddleware.After)
}

func isTransfer(req *http.Request) bool { return req.Context().Value(transferAttemptKey{}) != nil }

// StallError reports an object transfer whose connection moved no body bytes
// for After. It is a timeout, so the SDK retries the request.
type StallError struct{ After time.Duration }

func (e *StallError) Error() string {
	return "the connection stalled: no data moved for " + strconv.FormatFloat(e.After.Seconds(), 'f', -1, 64) + " seconds"
}
func (e *StallError) Timeout() bool   { return true }
func (e *StallError) Temporary() bool { return true }

// throttledTransport applies the bandwidth limits and the stall watchdog to
// object transfers and passes every other request through untouched.
//
// Layering for a transfer request body: the transport reads sendBody, which
// reads limitedBody (upload limit), which reads the SDK body. The watchdog
// measures the time between reads, so the limiter's sleeps (inside a read)
// never count as a stall. For the response: the caller reads limitedBody
// (download limit), which reads recvBody, which times each read from the
// network; sleeps happen outside it.
type throttledTransport struct{ base http.RoundTripper }

func (t throttledTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !isTransfer(req) {
		return t.base.RoundTrip(req)
	}
	ctx, cancel := context.WithCancelCause(req.Context())
	w := newWatchdog(cancel)
	r := req.WithContext(ctx)
	var send *sendBody
	if req.Body != nil && req.Body != http.NoBody {
		send = &sendBody{rc: newLimitedBody(req.Body, ctx, upLimit), w: w, left: req.ContentLength}
		r.Body = send
	}
	resp, err := t.base.RoundTrip(r)
	if send != nil {
		send.finish()
	}
	if err != nil {
		w.disarm()
		if w.stalled.Load() {
			err = w.err
		}
		cancel(nil)
		return nil, err
	}
	resp.Body = newLimitedBody(&recvBody{rc: resp.Body, w: w, cancel: cancel}, req.Context(), downLimit)
	return resp, nil
}

// watchdog cancels an attempt when its timer, armed while body bytes are
// expected to move, runs out.
type watchdog struct {
	timer   *time.Timer
	timeout time.Duration
	stalled atomic.Bool
	err     *StallError
	cancel  context.CancelCauseFunc
}

func newWatchdog(cancel context.CancelCauseFunc) *watchdog {
	w := &watchdog{timeout: stallTimeout, cancel: cancel}
	w.err = &StallError{After: w.timeout}
	w.timer = time.AfterFunc(time.Hour, func() {
		w.stalled.Store(true)
		w.cancel(w.err)
	})
	w.timer.Stop()
	return w
}

func (w *watchdog) arm()    { w.timer.Reset(w.timeout) }
func (w *watchdog) disarm() { w.timer.Stop() }

// stallErr replaces err with the stall error when the watchdog fired.
func (w *watchdog) stallErr(err error) error {
	if err != nil && err != io.EOF && w.stalled.Load() {
		return w.err
	}
	return err
}

// sendBody watches a request body. The transport only reads when it can
// write more, so a gap between reads longer than the timeout means the
// connection stopped taking data. Watching ends at the last byte, EOF, Close
// or when the round trip returns.
type sendBody struct {
	rc   io.ReadCloser
	w    *watchdog
	mu   sync.Mutex
	left int64 // bytes still expected; -1 unknown
	done bool
}

func (b *sendBody) Read(p []byte) (int, error) {
	b.w.disarm()
	n, err := b.rc.Read(p)
	b.mu.Lock()
	if b.left > 0 {
		b.left -= int64(n)
	}
	if err == nil && b.left != 0 && !b.done {
		b.w.arm()
	}
	b.mu.Unlock()
	return n, b.w.stallErr(err)
}

func (b *sendBody) finish() {
	b.mu.Lock()
	b.done = true
	b.w.disarm()
	b.mu.Unlock()
}

func (b *sendBody) Close() error {
	b.finish()
	return b.rc.Close()
}

// recvBody watches a response body: only time blocked in the network read
// counts. The attempt's context is released at EOF, on an error or on Close.
type recvBody struct {
	rc       io.ReadCloser
	w        *watchdog
	cancel   context.CancelCauseFunc
	released atomic.Bool
}

func (b *recvBody) Read(p []byte) (int, error) {
	b.w.arm()
	n, err := b.rc.Read(p)
	b.w.disarm()
	if err != nil {
		err = b.w.stallErr(err)
		b.release()
	}
	return n, err
}

func (b *recvBody) Close() error {
	b.w.disarm()
	err := b.rc.Close()
	b.release()
	return err
}

func (b *recvBody) release() {
	if b.released.CompareAndSwap(false, true) {
		b.cancel(nil)
	}
}
