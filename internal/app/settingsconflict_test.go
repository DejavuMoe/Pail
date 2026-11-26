package app

import (
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/DejavuMoe/Pail/internal/s3test"
)

// Another instance (the command line) switches the secret backend while this
// one keeps its settings in memory: no settings write of this instance may put
// the old backend back.
func TestSettingsWritesRefuseAStaleSecretBackend(t *testing.T) {
	writes := map[string]func(*Service) error{
		"SaveSettings": func(s *Service) error {
			st := s.Settings()
			st.Theme = "dark"
			return s.SaveSettings(st)
		},
		"SetUpdateCheck": func(s *Service) error { return s.SetUpdateCheck(time.Now()) },
		"SaveProxy": func(s *Service) error {
			return s.SaveProxy(ProxySettings{Scheme: "http", Host: "127.0.0.1", Port: 3128}, nil)
		},
	}
	for name, write := range writes {
		t.Run(name, func(t *testing.T) {
			kr := NewMemoryKeyring()
			desktop, dir := newTestServiceDir(t, "", kr)
			cli, _ := newTestServiceDir(t, dir, kr)
			if err := cli.SetSecretBackend(testCtx(t), SecretBackendVault, testPW); err != nil {
				t.Fatal(err)
			}
			if err := write(desktop); !IsKind(err, ErrConflict) {
				t.Fatalf("stale write: %v", err)
			}
			var disk Settings
			if _, err := readJSON(filepath.Join(dir, "settings.json"), &disk); err != nil {
				t.Fatal(err)
			}
			if disk.SecretBackend != SecretBackendVault || disk.Theme == "dark" || disk.Proxy.Host != "" {
				t.Fatalf("settings.json changed: %+v", disk)
			}
			// The instance that switched writes normally.
			if err := write(cli); err != nil {
				t.Fatal(err)
			}
			reloaded, _ := newTestServiceDir(t, dir, kr)
			if reloaded.SecretBackend() != SecretBackendVault {
				t.Fatal("backend lost")
			}
		})
	}
}

func TestTestAccountReadsTheProxyPasswordOnlyForTheGlobalProxy(t *testing.T) {
	srv := s3test.New()
	t.Cleanup(srv.Close)
	s := newTestService(t, nil)
	pw := "proxy-pw-7Hq2"
	if err := s.SaveProxy(ProxySettings{Scheme: "http", Host: "127.0.0.1", Port: 9, Username: "u"}, &pw); err != nil {
		t.Fatal(err)
	}
	if err := s.kr.Delete(proxyPasswordKey); err != nil { // the password is gone from the store
		t.Fatal(err)
	}
	direct := staticAccount("direct", srv.URL)
	direct.ProxyMode = ProxyDirect
	if err := s.TestAccount(testCtx(t), direct, &Secrets{SecretAccessKey: "sk"}); err != nil {
		t.Fatalf("a direct account needs no proxy password: %v", err)
	}
	global := staticAccount("global", srv.URL)
	if err := s.TestAccount(testCtx(t), global, &Secrets{SecretAccessKey: "sk"}); !IsKind(err, ErrNotFound) {
		t.Fatalf("global proxy without its password: %v", err)
	}
}

// A 403 on the GET that compares an existing destination is AccessDenied, so
// write-only-for-reads credentials fall back to create-only writes instead of
// failing with the raw SDK error.
func TestUploadIdentityCheckDeniedIsClassified(t *testing.T) {
	e := newTrEnv(t)
	local := filepath.Join(t.TempDir(), "a.txt")
	trWrite(t, local, []byte("hello"))
	trExpect(t, e.upload(UploadRequest{Dest: e.loc("b", ""), Paths: []string{local}, Conflict: ConflictRename}), StatusSucceeded)
	e.srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "" {
			return &s3test.Fault{Status: 403, Code: "AccessDenied", Message: "no reads"}
		}
		return nil
	})
	tasks := e.upload(UploadRequest{Dest: e.loc("b", ""), Paths: []string{local}, Conflict: ConflictRename})
	trExpect(t, tasks, StatusSucceeded)
	if got := trObject(t, e, "a (1).txt"); got != "hello" {
		t.Fatal(got)
	}
	if got := trObject(t, e, "a.txt"); got != "hello" {
		t.Fatal(got)
	}
	// Without conditional writes the fallback is not safe: the task fails
	// with a classified error.
	e.s.markCondWritesUnsupported(e.acc.ID)
	g, err := e.s.Upload(UploadRequest{Dest: e.loc("b", ""), Paths: []string{local}, Conflict: ConflictRename})
	if err != nil {
		t.Fatal(err)
	}
	for _, tk := range trKind(trWaitGroup(t, e.s, g), KindUpload) {
		if tk.Status != StatusFailed {
			t.Fatalf("%+v", tk)
		}
		if err := e.s.lastTaskErr(tk.ID); !IsKind(err, ErrAccessDenied) {
			t.Fatalf("task error %v (%T)", err, err)
		}
	}
}

// lastTaskErr returns the error Work returned for task id.
func (s *Service) lastTaskErr(id string) error {
	s.q.mu.Lock()
	defer s.q.mu.Unlock()
	if t := s.q.tasks[id]; t != nil {
		return t.err
	}
	return nil
}
