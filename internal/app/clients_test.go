package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DejavuMoe/Pail/internal/storage"
)

func TestStorageConfigMapping(t *testing.T) {
	cases := []struct {
		name string
		a    Account
		sec  Secrets
		want storage.Config
	}{
		{"static", Account{Endpoint: "http://h", Region: "r", PathStyle: true, Credentials: CredStatic, AccessKeyID: "AK", InsecureSkipVerify: true, ChecksumMode: "when_required",
			RoleARN: "arn:x", ExternalID: "e", RoleSessionName: "s"}, Secrets{"SK", "ST"},
			storage.Config{Endpoint: "http://h", Region: "r", UsePathStyle: true, AccessKeyID: "AK", SecretAccessKey: "SK", SessionToken: "ST",
				InsecureSkipVerify: true, ChecksumMode: "when_required", RoleARN: "arn:x", ExternalID: "e", RoleSessionName: "s"}},
		{"profile", Account{Credentials: CredProfile, Profile: "dev"}, Secrets{}, storage.Config{UseDefaultCredentials: true, Profile: "dev"}},
		{"default", Account{Credentials: CredDefault}, Secrets{}, storage.Config{UseDefaultCredentials: true}},
		{"anonymous", Account{Credentials: CredAnonymous}, Secrets{}, storage.Config{Anonymous: true}},
	}
	for _, c := range cases {
		if got := storageConfig(c.a, c.sec); got != c.want {
			t.Errorf("%s: got %+v want %+v", c.name, got, c.want)
		}
	}
}

func TestClientCacheReadsKeyringOnceAndInvalidates(t *testing.T) {
	srv := stubS3(t, nil)
	kr := newTestKeyring()
	s := newTestService(t, kr)
	a, err := s.SaveAccount(staticAccount("c", srv.URL), &Secrets{SecretAccessKey: "s0", SessionToken: "t0"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		bs, err := s.Buckets(testCtx(t), a.ID)
		if err != nil || len(bs) != 2 {
			t.Fatalf("%v %v", bs, err)
		}
	}
	if n := kr.getCount(); n != 2 { // secret + session, once
		t.Fatalf("keyring read %d times; secrets must be cached after the first read", n)
	}
	c1, _ := s.storageClient(testCtx(t), a.ID)
	c2, _ := s.storageClient(testCtx(t), a.ID)
	if c1 != c2 {
		t.Fatal("storage client not cached")
	}
	// Saving the account invalidates the cache.
	if _, err := s.SaveAccount(a, &Secrets{SecretAccessKey: "s1"}); err != nil {
		t.Fatal(err)
	}
	c3, err := s.storageClient(testCtx(t), a.ID)
	if err != nil || c3 == c1 {
		t.Fatalf("client not rebuilt after SaveAccount (%v)", err)
	}
	// Deleting it makes the account unknown.
	if err := s.DeleteAccount(a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Buckets(testCtx(t), a.ID); !IsKind(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
}

func TestCloudClientLazyAndCached(t *testing.T) {
	s := newTestService(t, nil)
	a, _ := s.SaveAccount(staticAccount("c", "http://localhost:1"), &Secrets{SecretAccessKey: "s"})
	c1, err := s.cloudClient(testCtx(t), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	c2, _ := s.cloudClient(testCtx(t), a.ID)
	if c1 != c2 {
		t.Fatal("cloud client not cached")
	}
}

func TestMissingKeyringSecretIsReported(t *testing.T) {
	kr := newTestKeyring()
	s := newTestService(t, kr)
	a, _ := s.SaveAccount(staticAccount("c", "http://localhost:1"), &Secrets{SecretAccessKey: "s"})
	kr.Keyring.Delete("account/" + a.ID + "/secret")
	_, err := s.Buckets(testCtx(t), a.ID)
	if !IsKind(err, ErrNotFound) {
		t.Fatalf("want NotFound pointing at the account, got %v", err)
	}
}

func TestConcurrentFirstUseSharesOneKeyringRead(t *testing.T) {
	srv := stubS3(t, nil)
	kr := newTestKeyring()
	s := newTestService(t, kr)
	a, _ := s.SaveAccount(staticAccount("c", srv.URL), &Secrets{SecretAccessKey: "s0"})
	// Drop the cache entry created by nothing yet, then make Get slow so all callers overlap.
	release := make(chan struct{})
	kr.onGet = func(string) error { <-release; return nil }
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Buckets(testCtx(t), a.ID); err != nil {
				t.Error(err)
			}
		}()
	}
	time.Sleep(100 * time.Millisecond)
	close(release)
	wg.Wait()
	if n := kr.getCount(); n != 1 {
		t.Fatalf("keyring read %d times for 8 concurrent first uses", n)
	}
}

func TestInvalidationDuringBuildDiscardsStaleEntry(t *testing.T) {
	srv := stubS3(t, nil)
	kr := newTestKeyring()
	s := newTestService(t, kr)
	a, _ := s.SaveAccount(staticAccount("c", srv.URL), &Secrets{SecretAccessKey: "old"})
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	kr.onGet = func(k string) error {
		once.Do(func() { close(entered); <-release })
		return nil
	}
	got := make(chan *clientEntry, 1)
	go func() {
		e, err := s.entry(testCtx(t), a.ID)
		if err != nil {
			t.Error(err)
		}
		got <- e
	}()
	<-entered
	// Replace the secret while the first build is stuck reading the old one.
	if _, err := s.SaveAccount(a, &Secrets{SecretAccessKey: "new"}); err != nil {
		t.Fatal(err)
	}
	close(release)
	e := <-got
	if e == nil || e.cfg.SecretAccessKey != "new" {
		t.Fatalf("stale configuration was used: %+v", e)
	}
}

// TestBlockedKeyringDoesNotFreezeReaders is the regression test for the audit
// finding "keyring called under the account mutex": while a keyring call is
// stuck (an OS dialog), UI-facing reads must still return immediately.
func TestBlockedKeyringDoesNotFreezeReaders(t *testing.T) {
	srv := stubS3(t, nil)
	kr := newTestKeyring()
	s := newTestService(t, kr)
	a, _ := s.SaveAccount(staticAccount("c", srv.URL), &Secrets{SecretAccessKey: "s0"})

	block := make(chan struct{})
	entered := make(chan string, 8)
	kr.onGet = func(k string) error { entered <- "get"; <-block; return nil }
	kr.onSet = func(k string) error { entered <- "set"; <-block; return nil }
	kr.onDel = func(k string) error { entered <- "del"; <-block; return nil }
	defer func() { kr.onGet, kr.onSet, kr.onDel = nil, nil, nil }()

	var wg sync.WaitGroup
	start := func(f func()) {
		wg.Add(1)
		go func() { defer wg.Done(); f() }()
	}
	start(func() { s.Buckets(context.Background(), a.ID) })                                 // stuck in Get
	start(func() { s.SaveAccount(a, &Secrets{SecretAccessKey: "s1"}) })                     // stuck in Set
	start(func() { s.SetSecret("ref", "v") })                                               // waits on the writer lock
	start(func() { s.TestAccount(context.Background(), staticAccount("t", srv.URL), nil) }) // invalid (no secret) -> returns
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("keyring calls were never entered")
		}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			s.Tasks()
			s.Accounts()
			s.Settings()
			s.SecretRefs()
			s.PublicURL(Location{AccountID: a.ID, Bucket: "b"}, "k")
			s.TaskItems("x")
		}
		id := s.submit(taskSpec{Kind: KindOther, Title: "while blocked", Work: func(context.Context, *run) error { return nil }})
		if _, err := s.waitTask(testCtx(t), id); err != nil {
			t.Error(err)
		}
		if err := s.SaveSettings(DefaultSettings()); err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Tasks/Accounts/Settings blocked while a keyring call was stuck")
	}
	close(block)
	wg.Wait()
}

func TestBucketsHonoursContextWhileKeyringBlocked(t *testing.T) {
	srv := stubS3(t, nil)
	kr := newTestKeyring()
	s := newTestService(t, kr)
	a, _ := s.SaveAccount(staticAccount("c", srv.URL), &Secrets{SecretAccessKey: "s0"})
	block := make(chan struct{})
	kr.onGet = func(string) error { <-block; return nil }
	defer close(block)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := s.Buckets(ctx, a.ID)
	// A deadline is a timeout (Network, retryable), not a user cancellation.
	if !IsKind(err, ErrNetwork) || !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("want a Network \"timed out\" error, got %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("did not return promptly")
	}
}
