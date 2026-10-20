package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/smithy-go"
)

// lgAPIError mimics an AWS SDK error: smithy.APIError + request id + status.
type lgAPIError struct{ msg string }

func (e *lgAPIError) Error() string                 { return e.msg }
func (e *lgAPIError) ErrorCode() string             { return "SlowDown" }
func (e *lgAPIError) ErrorMessage() string          { return e.msg }
func (e *lgAPIError) ErrorFault() smithy.ErrorFault { return smithy.FaultServer }
func (e *lgAPIError) ServiceRequestID() string      { return "REQ-12345" }
func (e *lgAPIError) HTTPStatusCode() int           { return 503 }

func lgLogOn(t *testing.T) *Service {
	t.Helper()
	s := newTestService(t, nil)
	st := s.Settings()
	st.LogToFile = true
	if err := s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	return s
}

// lgOff turns logging off (flushing the file) and returns the log text.
func lgOff(t *testing.T, s *Service) string {
	t.Helper()
	st := s.Settings()
	st.LogToFile = false
	if err := s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(s.LogDir(), logFileName))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func lgRunTask(t *testing.T, s *Service, title string, err error) Task {
	t.Helper()
	id := s.submit(taskSpec{Kind: KindOther, Title: title, Work: func(ctx context.Context, r *run) error {
		r.SetTotal(10, 1)
		r.AddBytes(10)
		return err
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tk, werr := s.waitTask(ctx, id)
	if werr != nil {
		t.Fatal(werr)
	}
	return tk
}

func TestLogToggleAtRuntime(t *testing.T) {
	s := newTestService(t, nil)
	if fi, err := os.Stat(s.LogDir()); err != nil || !fi.IsDir() || !filepath.IsAbs(s.LogDir()) {
		t.Fatalf("log dir: %v %v", fi, err)
	}
	lgRunTask(t, s, "before-enable", nil)
	if _, err := os.Stat(filepath.Join(s.LogDir(), logFileName)); err == nil {
		t.Fatal("log file exists while logging is off")
	}
	st := s.Settings()
	st.LogToFile = true
	if err := s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	lgRunTask(t, s, "while-on", nil)
	st.LogToFile = false
	st.Limits.Upload = 3
	if err := s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	lgRunTask(t, s, "after-off", nil)
	b, err := os.ReadFile(filepath.Join(s.LogDir(), logFileName))
	if err != nil {
		t.Fatal(err)
	}
	log := string(b)
	for _, want := range []string{"logging enabled", "while-on", "task finished", "logging disabled"} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
	for _, bad := range []string{"before-enable", "after-off"} {
		if strings.Contains(log, bad) {
			t.Errorf("log contains %q while logging was off", bad)
		}
	}
	// turn on again: appends
	st.LogToFile = true
	if err := s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log, "queue limits changed") {
		t.Error("no queue limit record")
	}
}

func TestLogFailedTaskFields(t *testing.T) {
	s := lgLogOn(t)
	tk := lgRunTask(t, s, "Upload big.bin", &lgAPIError{"slow down please"})
	if tk.Status != StatusFailed {
		t.Fatalf("status %v", tk.Status)
	}
	log := lgOff(t, s)
	var line string
	for _, l := range strings.Split(log, "\n") {
		if strings.Contains(l, "task finished") && strings.Contains(l, "big.bin") {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("no task finished record:\n%s", log)
	}
	for _, want := range []string{"level=WARN", "status=failed", "error_kind=Other", "error_code=SlowDown", "request_id=REQ-12345", "http_status=503", "bytes=10", "duration="} {
		if !strings.Contains(line, want) {
			t.Errorf("record lacks %q: %s", want, line)
		}
	}
}

func TestLogRedaction(t *testing.T) {
	lgAddSecret("bareLiteralSecret987")
	secrets := []string{
		"wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		"FwoGZXIvYXdzEJr//////////wEaDExampleSessionTokenValue",
		"c3NlY2tleWJhc2U2NA==",
		"AWS4-HMAC-SHA256 Credential=AKIAEXAMPLE/20260101/us-east-1/s3/aws4_request, SignedHeaders=host, Signature=deadbeef0123",
		"deadbeefsigvalue0123456789",
		"AKIAEXAMPLE/20260101/us-east-1/s3/aws4_request",
		"hunter2zip",
		"bareLiteralSecret987",
		"X-Amz-Security-Token-Value-XYZ",
		"presignedquerysecret",
	}
	messages := []string{
		"secret_access_key=" + secrets[0],
		"SecretAccessKey: " + secrets[0],
		`{"sessionToken":"` + secrets[1] + `"}`,
		"x-amz-server-side-encryption-customer-key: " + secrets[2],
		"Authorization: " + secrets[3],
		"GET https://b.s3.amazonaws.com/k?X-Amz-Signature=" + secrets[4] + "&X-Amz-Credential=" + secrets[5] + "&x=presignedquerysecret",
		"X-Amz-Credential=" + secrets[5],
		"zip password=" + secrets[6],
		"unexpected " + secrets[7] + " in text",
		"x-amz-security-token: " + secrets[8],
	}
	for _, m := range messages {
		got := redact(m)
		for _, sec := range secrets {
			if strings.Contains(got, sec) {
				t.Errorf("redact(%q) = %q still has %q", m, got, sec)
			}
		}
	}
	if got := redact("fetch https://h.example/path/obj?a=1#frag now"); got != "fetch https://h.example/path/obj now" {
		t.Errorf("url strip: %q", got)
	}

	// End to end: errors that embed secrets, plus sensitive attribute keys.
	s := lgLogOn(t)
	for i, m := range messages {
		lgRunTask(t, s, fmt.Sprintf("task %d", i), errors.New(m))
	}
	s.lgInfo("direct", "secretAccessKey", secrets[0], "sessionToken", secrets[1], "url", "https://x.example/o?X-Amz-Signature=presignedquerysecret", "note", "password="+secrets[6])
	log := lgOff(t, s)
	for _, sec := range secrets {
		if strings.Contains(log, sec) {
			t.Errorf("log contains %q:\n%s", sec, log)
		}
	}
	if !strings.Contains(log, redacted) || !strings.Contains(log, "https://x.example/o") {
		t.Errorf("expected redaction markers and stripped url:\n%s", log)
	}
}

func TestLogRotation(t *testing.T) {
	dir := t.TempDir()
	r := &lgRotator{path: filepath.Join(dir, logFileName), max: 1000, keep: 3}
	line := []byte(strings.Repeat("x", 99) + "\n")
	for i := 0; i < 100; i++ {
		if _, err := r.Write(line); err != nil {
			t.Fatal(err)
		}
	}
	r.Close()
	for _, n := range []string{"pail.log", "pail.1.log", "pail.2.log"} {
		fi, err := os.Stat(filepath.Join(dir, n))
		if err != nil {
			t.Fatalf("%s: %v", n, err)
		}
		if fi.Size() > 1000 {
			t.Errorf("%s is %d bytes", n, fi.Size())
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "pail.3.log")); err == nil {
		t.Error("a 4th file was kept")
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 3 {
		t.Errorf("%d files", len(ents))
	}
}

// lgSlowWriter blocks every write until release is closed.
type lgSlowWriter struct {
	release chan struct{}
	mu      sync.Mutex
	buf     bytes.Buffer
	closed  bool
}

func (w *lgSlowWriter) Write(p []byte) (int, error) {
	<-w.release
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}
func (w *lgSlowWriter) Close() error { w.mu.Lock(); w.closed = true; w.mu.Unlock(); return nil }

func TestLogBlockedWriterNeverBlocksTasks(t *testing.T) {
	s := newTestService(t, nil)
	w := &lgSlowWriter{release: make(chan struct{})}
	k := newLgSink(w, 4)
	s.lg.set(k)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 60; i++ {
			lgRunTask(t, s, fmt.Sprintf("slow-%d", i), nil)
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		close(w.release)
		t.Fatal("tasks blocked by the slow log writer")
	}
	if k.dropped() == 0 {
		t.Error("expected dropped records")
	}
	close(w.release)
	s.closeLogging()
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.closed || !strings.Contains(w.buf.String(), "log records dropped") {
		t.Errorf("closed=%v out=%q", w.closed, w.buf.String())
	}
}

func TestLogShutdownFlushesAndCloses(t *testing.T) {
	s, dir := newTestServiceDir(t, "", nil)
	st := s.Settings()
	st.LogToFile = true
	if err := s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	lgRunTask(t, s, "last-task", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "logs", logFileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "last-task") || !strings.Contains(string(b), "service shutdown") {
		t.Errorf("log not flushed:\n%s", b)
	}
	if s.lg.p.Load() != nil {
		t.Error("sink still installed")
	}
	s.lgInfo("after shutdown") // must not panic
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestLogAccountRecord(t *testing.T) {
	s := lgLogOn(t)
	a, err := s.SaveAccount(Account{Name: "minio", Provider: ProviderS3, Endpoint: "https://minio.local:9000", Region: "us-east-1", Credentials: CredStatic, AccessKeyID: "AKIAXYZ"},
		&Secrets{SecretAccessKey: "superSecretKey123456", SessionToken: "sessionTokenValue99"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAccount(a.ID); err != nil {
		t.Fatal(err)
	}
	log := lgOff(t, s)
	for _, want := range []string{"account saved", "account deleted", a.ID, "name=minio", "endpoint_host=minio.local:9000"} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
	for _, bad := range []string{"superSecretKey123456", "sessionTokenValue99"} {
		if strings.Contains(log, bad) {
			t.Errorf("log contains %q", bad)
		}
	}
}
