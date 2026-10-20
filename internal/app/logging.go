package app

// Redacted diagnostic logging (opt-in through Settings.LogToFile).
//
// Every record goes through one pipeline: slog text handler -> redact
// (ReplaceAttr + string scrubber, applied again to the finished line) ->
// bounded channel -> one writer goroutine -> size-rotating file. Callers never
// touch the disk: when the channel is full the record is dropped and counted,
// so a slow disk can neither stall the UI nor task progress.
//
// Operation errors are logged from classify, which has no Service; they are
// broadcast to every Service that currently has logging enabled (an
// application has exactly one).

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aws/smithy-go"
)

const (
	logFileName   = "pail.log"
	logMaxBytes   = 5 << 20
	logKeepFiles  = 3
	logQueueLen   = 1024
	logCloseWait  = 3 * time.Second
	logMaxMessage = 300
	redacted      = "[REDACTED]"
)

// ---- redaction ---------------------------------------------------------------

var (
	// A URL: everything after "?" (and any fragment) is removed.
	reURL = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://[^\s"'<>?#\\]*)[?#][^\s"'<>]*`)
	// Authorization headers and raw SigV4 header values, up to end of line.
	reAuth = regexp.MustCompile(`(?i)(authorization["']?\s*[:=]\s*)[^\r\n]*|\bAWS4-HMAC-SHA256\b[^\r\n]*`)
	// Labelled values: key=value, key: value, "key":"value".
	reLabel = regexp.MustCompile(`(?i)(x-amz-(?:security-token|signature|credential|algorithm|server-side-encryption-customer-key(?:-md5)?)|secret[_ -]?access[_ -]?key|secretaccesskey|session[_ -]?token|sse-?c[_ -]?key|customer[_ -]?key|zip[_ -]?password|password|passwd|pwd|secret|token|signature|credential)(\\?["']?\s*[:=]\s*(?:\\?["'])?)[^\s&"',;\\]+`)
)

var lgSecretVals struct {
	sync.RWMutex
	list []string
}

// lgAddSecret registers a literal secret value (keyring contents, secrets that
// were typed in) so that it is removed even where no label gives it away.
func lgAddSecret(v string) {
	if len(v) < 6 {
		return
	}
	lgSecretVals.Lock()
	defer lgSecretVals.Unlock()
	for _, x := range lgSecretVals.list {
		if x == v {
			return
		}
	}
	if len(lgSecretVals.list) >= 256 {
		lgSecretVals.list = lgSecretVals.list[1:]
	}
	lgSecretVals.list = append(lgSecretVals.list, v)
}

// redact is the single string scrubber.
func redact(s string) string {
	if s == "" {
		return s
	}
	s = reURL.ReplaceAllString(s, "$1")
	s = reAuth.ReplaceAllStringFunc(s, func(m string) string {
		if i := strings.IndexAny(m, ":="); i >= 0 && strings.HasPrefix(strings.ToLower(m), "authorization") {
			return m[:i+1] + " " + redacted
		}
		return "Authorization: " + redacted
	})
	s = reLabel.ReplaceAllString(s, "${1}${2}"+redacted)
	lgSecretVals.RLock()
	for _, v := range lgSecretVals.list {
		if strings.Contains(s, v) {
			s = strings.ReplaceAll(s, v, redacted)
		}
	}
	lgSecretVals.RUnlock()
	return s
}

func lgSensitiveKey(k string) bool {
	k = strings.ToLower(k)
	for _, w := range []string{"secret", "password", "passwd", "token", "authorization", "sse", "signature", "credential", "body"} {
		if strings.Contains(k, w) {
			return true
		}
	}
	return false
}

// redactAttr is the slog ReplaceAttr of every log handler.
func redactAttr(_ []string, a slog.Attr) slog.Attr {
	if a.Key == slog.TimeKey || a.Key == slog.LevelKey {
		return a
	}
	if a.Key != slog.MessageKey && lgSensitiveKey(a.Key) {
		return slog.String(a.Key, redacted)
	}
	v := a.Value.Resolve()
	var str string
	switch v.Kind() {
	case slog.KindString:
		str = v.String()
	case slog.KindAny:
		switch x := v.Any().(type) {
		case error:
			str = x.Error()
		case fmt.Stringer:
			str = x.String()
		default:
			str = fmt.Sprint(x)
		}
	default:
		return slog.Attr{Key: a.Key, Value: v}
	}
	lk := strings.ToLower(a.Key)
	if strings.HasSuffix(lk, "url") || strings.HasSuffix(lk, "uri") || strings.HasSuffix(lk, "endpoint") {
		if i := strings.IndexAny(str, "?#"); i >= 0 {
			str = str[:i]
		}
	}
	return slog.String(a.Key, redact(str))
}

// ---- rotating file -----------------------------------------------------------

type lgRotator struct {
	path string
	max  int64
	keep int
	f    *os.File
	size int64
}

func (r *lgRotator) open() error {
	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(r.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	_ = f.Chmod(0o600)
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	r.f, r.size = f, st.Size()
	return nil
}

// rotName returns "pail.N.log" for "pail.log".
func (r *lgRotator) rotName(n int) string {
	ext := filepath.Ext(r.path)
	return strings.TrimSuffix(r.path, ext) + "." + strconv.Itoa(n) + ext
}

func (r *lgRotator) rotate() error {
	if r.f != nil {
		r.f.Close()
		r.f = nil
	}
	os.Remove(r.rotName(r.keep - 1))
	for i := r.keep - 2; i >= 1; i-- {
		os.Rename(r.rotName(i), r.rotName(i+1))
	}
	if r.keep > 1 {
		os.Rename(r.path, r.rotName(1))
	} else {
		os.Remove(r.path)
	}
	return r.open()
}

func (r *lgRotator) Write(p []byte) (int, error) {
	if r.f == nil {
		if err := r.open(); err != nil {
			return 0, err
		}
	}
	if r.size > 0 && r.size+int64(len(p)) > r.max {
		if err := r.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

func (r *lgRotator) Close() error {
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}

// ---- sink --------------------------------------------------------------------

// lgSink owns the bounded queue and the writer goroutine.
type lgSink struct {
	log   *slog.Logger
	ch    chan []byte
	done  chan struct{}
	w     io.Writer
	mu    sync.RWMutex // guards closed vs. sends
	shut  bool
	drops atomic.Int64
}

// newLgSink starts a sink writing to w (closed when the sink is closed, if it
// is an io.Closer). queue is the channel capacity.
func newLgSink(w io.Writer, queue int) *lgSink {
	k := &lgSink{ch: make(chan []byte, queue), done: make(chan struct{}), w: w}
	k.log = slog.New(slog.NewTextHandler(lgChanWriter{k}, &slog.HandlerOptions{Level: slog.LevelInfo, ReplaceAttr: redactAttr}))
	go k.loop()
	return k
}

type lgChanWriter struct{ k *lgSink }

// Write is called by the slog handler once per record; it never blocks.
func (c lgChanWriter) Write(p []byte) (int, error) {
	line := []byte(redact(string(p))) // belt and braces: scrub the finished line too
	k := c.k
	k.mu.RLock()
	defer k.mu.RUnlock()
	if k.shut {
		return len(p), nil
	}
	select {
	case k.ch <- line:
	default:
		k.drops.Add(1)
	}
	return len(p), nil
}

func (k *lgSink) loop() {
	defer close(k.done)
	var reported int64
	notice := func() {
		if d := k.drops.Load(); d > reported {
			fmt.Fprintf(k.w, "time=%s level=WARN msg=\"log records dropped\" count=%d\n", time.Now().Format(time.RFC3339Nano), d-reported)
			reported = d
		}
	}
	for b := range k.ch {
		notice()
		k.w.Write(b)
	}
	notice()
	if c, ok := k.w.(io.Closer); ok {
		c.Close()
	}
}

// dropped returns the number of records discarded because the queue was full.
func (k *lgSink) dropped() int64 { return k.drops.Load() }

// close stops accepting records, flushes the queue and closes the writer. It
// gives up waiting after wait (a hung disk must not hang Shutdown); the writer
// goroutine then finishes in the background. It reports whether it flushed.
func (k *lgSink) close(wait time.Duration) bool {
	k.mu.Lock()
	if !k.shut {
		k.shut = true
		close(k.ch)
	}
	k.mu.Unlock()
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-k.done:
		return true
	case <-t.C:
		return false
	}
}

// ---- Service integration -------------------------------------------------------

// lgHolder is embedded in Service: the current sink, nil when logging is off.
type lgHolder struct {
	p atomic.Pointer[lgSink]
	// active are the holders with a sink, for logging from classify.
}

var lgActive struct {
	sync.Mutex
	list []*lgHolder
}

func lgRegister(h *lgHolder, on bool) {
	lgActive.Lock()
	defer lgActive.Unlock()
	for i, x := range lgActive.list {
		if x == h {
			if !on {
				lgActive.list = append(lgActive.list[:i:i], lgActive.list[i+1:]...)
			}
			return
		}
	}
	if on {
		lgActive.list = append(lgActive.list, h)
	}
}

// lgSet installs k (nil = off) and returns the previous sink.
func (h *lgHolder) set(k *lgSink) *lgSink {
	old := h.p.Swap(k)
	lgRegister(h, k != nil)
	return old
}

func (h *lgHolder) emit(level slog.Level, msg string, args []any) {
	if k := h.p.Load(); k != nil {
		k.log.Log(context.Background(), level, msg, args...)
	}
}

// LogDir returns the absolute path of the directory that holds the log files.
func (s *Service) LogDir() string {
	d := filepath.Join(s.dir, "logs")
	if a, err := filepath.Abs(d); err == nil {
		return a
	}
	return d
}

func (s *Service) lgInfo(msg string, args ...any) { s.lg.emit(slog.LevelInfo, msg, args) }
func (s *Service) lgWarn(msg string, args ...any) { s.lg.emit(slog.LevelWarn, msg, args) }

// applyLogging opens or closes the log file to match on. Failure to open the
// file is reported once through the returned error but never blocks callers.
func (s *Service) applyLogging(on bool) error {
	s.lgMu.Lock()
	defer s.lgMu.Unlock()
	if on == (s.lg.p.Load() != nil) {
		return nil
	}
	if !on {
		s.lgInfo("logging disabled")
		if old := s.lg.set(nil); old != nil {
			old.close(logCloseWait)
		}
		return nil
	}
	dir := s.LogDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	k := newLgSink(&lgRotator{path: filepath.Join(dir, logFileName), max: logMaxBytes, keep: logKeepFiles}, logQueueLen)
	s.lg.set(k)
	s.lgInfo("logging enabled", lgBuildAttrs()...)
	return nil
}

// closeLogging flushes and closes the log (Shutdown).
func (s *Service) closeLogging() {
	s.lgMu.Lock()
	defer s.lgMu.Unlock()
	if s.lg.p.Load() == nil {
		return
	}
	s.lgInfo("service shutdown")
	if old := s.lg.set(nil); old != nil {
		old.close(logCloseWait)
	}
}

func lgBuildAttrs() []any {
	a := []any{"goos", runtime.GOOS, "goarch", runtime.GOARCH, "go", runtime.Version()}
	if bi, ok := debug.ReadBuildInfo(); ok {
		if bi.Main.Version != "" {
			a = append(a, "version", bi.Main.Version)
		}
		for _, kv := range bi.Settings {
			switch kv.Key {
			case "vcs.revision":
				if len(kv.Value) > 12 {
					kv.Value = kv.Value[:12]
				}
				a = append(a, "revision", kv.Value)
			case "vcs.modified":
				a = append(a, "modified", kv.Value)
			}
		}
	}
	return a
}

// lgAccountAttrs identifies an account without any credential material.
func lgAccountAttrs(a Account) []any {
	host := ""
	if a.Endpoint != "" {
		if u, err := url.Parse(a.Endpoint); err == nil {
			host = u.Host
		}
		if host == "" {
			host = "(unparsed)"
		}
	}
	return []any{"account", a.ID, "name", a.Name, "provider", a.Provider, "endpoint_host", host}
}

// lgErrFields extracts kind, code, status and request id from err.
func lgErrFields(err error) []any {
	if err == nil {
		return nil
	}
	var kind ErrKind
	var code, rid string
	status := 0
	var oe *OpError
	if errors.As(err, &oe) {
		kind, code, rid = oe.Kind, oe.Code, oe.RequestID
	} else {
		switch {
		case errors.Is(err, context.Canceled):
			kind = ErrCanceled
		case errors.Is(err, context.DeadlineExceeded):
			kind = ErrNetwork
		}
	}
	var api smithy.APIError
	if code == "" && errors.As(err, &api) {
		code = api.ErrorCode()
	}
	var ri interface{ ServiceRequestID() string }
	if rid == "" && errors.As(err, &ri) {
		rid = ri.ServiceRequestID()
	}
	var hs interface{ HTTPStatusCode() int }
	if errors.As(err, &hs) {
		status = hs.HTTPStatusCode()
	}
	if oe == nil && kind == ErrOther {
		if k, ok := kindFromCode(code); ok {
			kind = k
		} else if k, ok := kindFromStatus(status); ok {
			kind = k
		} else if isNetworkError(err) {
			kind = ErrNetwork
		}
	}
	msg := err.Error()
	if len(msg) > logMaxMessage {
		msg = msg[:logMaxMessage] + "..."
	}
	return []any{"error_kind", kind.String(), "error_code", code, "http_status", status, "request_id", rid, "error", msg}
}

// lgOpError logs an operation error created by classify to every active log.
func lgOpError(oe *OpError, status int) {
	if oe.Kind == ErrCanceled || oe.Kind == ErrNotFound || oe.Kind == ErrInvalid || oe.Kind == ErrLocked {
		return // expected outcomes, not diagnostics
	}
	msg := oe.Message
	if len(msg) > logMaxMessage {
		msg = msg[:logMaxMessage] + "..."
	}
	lgActive.Lock()
	hs := append([]*lgHolder(nil), lgActive.list...)
	lgActive.Unlock()
	for _, h := range hs {
		h.emit(slog.LevelWarn, "operation failed", []any{"op", oe.Op, "error_kind", oe.Kind.String(),
			"http_status", status, "error_code", oe.Code, "request_id", oe.RequestID, "error", msg})
	}
}

// lgTaskDone logs the final state of t. Caller holds q.mu.
func (s *Service) lgTaskDone(t *task) {
	if s.lg.p.Load() == nil {
		return
	}
	a := lgTaskAttrs(t.snapshot())
	if t.err != nil || t.pub.Err != "" {
		if t.err != nil {
			a = append(a, lgErrFields(t.err)...)
		} else {
			a = append(a, "error", t.pub.Err)
		}
	}
	level := slog.LevelInfo
	if t.pub.Status == StatusFailed || t.pub.Status == StatusPartiallyFailed {
		level = slog.LevelWarn
	}
	s.lg.emit(level, "task finished", a)
}

// lgTaskAttrs describes a task in a log record.
func lgTaskAttrs(t Task) []any {
	a := []any{"task", t.ID, "kind", t.Kind.String(), "title", t.Title, "status", t.Status.String(),
		"bytes", t.BytesDone, "bytes_total", t.BytesTotal, "items", t.Items, "items_done", t.ItemsDone, "items_failed", t.ItemsFailed}
	if !t.Started.IsZero() && !t.Finished.IsZero() {
		a = append(a, "duration", t.Finished.Sub(t.Started).Round(time.Millisecond).String())
	}
	return a
}
