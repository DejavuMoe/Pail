package app

import (
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestAccount makes exactly one attempt so a wrong or refused endpoint fails
// fast instead of spinning through the SDK retryer.
func TestTestAccountSingleAttempt(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	s := newTestService(t, nil)
	err := s.TestAccount(testCtx(t), staticAccount("x", srv.URL), &Secrets{SecretAccessKey: "k"})
	if err == nil {
		t.Fatal("want an error from a 503 endpoint")
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("ListBuckets attempts = %d, want 1", n)
	}
}

func TestTestAccountRefusedFailsFast(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // nothing listens here any more
	s := newTestService(t, nil)
	start := time.Now()
	err = s.TestAccount(testCtx(t), staticAccount("x", "http://"+addr), &Secrets{SecretAccessKey: "k"})
	if !IsKind(err, ErrNetwork) {
		t.Fatalf("want Network, got %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("refused connection took %v", d)
	}
}
