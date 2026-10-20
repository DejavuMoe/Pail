package app

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

const listBucketsXML = `<?xml version="1.0" encoding="UTF-8"?><ListAllMyBucketsResult><Owner><ID>o</ID></Owner><Buckets>` +
	`<Bucket><Name>alpha</Name><CreationDate>2020-01-02T03:04:05.000Z</CreationDate><BucketRegion>eu-west-1</BucketRegion></Bucket>` +
	`<Bucket><Name>beta</Name><CreationDate>2021-02-03T04:05:06.000Z</CreationDate></Bucket>` +
	`</Buckets></ListAllMyBucketsResult>`

// stubS3 starts an httptest server whose GET / answers ListBuckets; other
// requests go to h (may be nil).
func stubS3(t testing.TB, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/" {
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprint(w, listBucketsXML)
			return
		}
		if h != nil {
			h(w, r)
			return
		}
		http.Error(w, "unexpected", 500)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func staticAccount(name, endpoint string) Account {
	return Account{Name: name, Provider: ProviderS3, Endpoint: endpoint, Region: "us-east-1", PathStyle: true,
		Credentials: CredStatic, AccessKeyID: "AKIATEST"}
}

func TestNormalizeAccountValidation(t *testing.T) {
	ok := staticAccount("a", "http://localhost:9000")
	cases := []struct {
		name string
		mod  func(*Account)
		want string // substring of the error; "" = valid
	}{
		{"valid", func(a *Account) {}, ""},
		{"valid https path endpoint", func(a *Account) { a.Endpoint = "https://s3.example.com/base/" }, ""},
		{"missing name", func(a *Account) { a.Name = "  " }, "name"},
		{"bad id", func(a *Account) { a.ID = "../x" }, "id"},
		{"unknown provider", func(a *Account) { a.Provider = "gcs" }, "provider"},
		{"s3 needs endpoint", func(a *Account) { a.Endpoint = "" }, "endpoint is required"},
		{"ftp endpoint", func(a *Account) { a.Endpoint = "ftp://x" }, "endpoint"},
		{"no scheme", func(a *Account) { a.Endpoint = "localhost:9000" }, "endpoint"},
		{"userinfo", func(a *Account) { a.Endpoint = "https://u:p@host" }, "endpoint"},
		{"query", func(a *Account) { a.Endpoint = "https://host/?a=b" }, "endpoint"},
		{"empty query marker", func(a *Account) { a.Endpoint = "https://host/?" }, "endpoint"},
		{"fragment", func(a *Account) { a.Endpoint = "https://host/#f" }, "endpoint"},
		{"static needs key id", func(a *Account) { a.AccessKeyID = "" }, "access key ID"},
		{"profile needs name", func(a *Account) { a.Credentials = CredProfile; a.AccessKeyID = "" }, "profile"},
		{"unknown credentials", func(a *Account) { a.Credentials = "magic" }, "credentials"},
		{"empty credentials", func(a *Account) { a.Credentials = "" }, "credentials"},
		{"anonymous + role", func(a *Account) { a.Credentials = CredAnonymous; a.RoleARN = "arn:aws:iam::1:role/x" }, "anonymous"},
		{"bad role arn", func(a *Account) { a.RoleARN = "role" }, "ARN"},
		{"external id without role", func(a *Account) { a.ExternalID = "x" }, "role ARN"},
		{"role ok", func(a *Account) { a.RoleARN = "arn:aws:iam::1:role/x"; a.ExternalID = "e"; a.RoleSessionName = "s" }, ""},
		{"bad checksum", func(a *Account) { a.ChecksumMode = "never" }, "checksum"},
		{"cdn base", func(a *Account) { a.CDNURL = " https://cdn.example.com/ " }, ""},
		{"cdn base with path", func(a *Account) { a.CDNURL = "https://h/cdn/" }, ""},
		{"cdn template", func(a *Account) { a.CDNURL = "https://{bucket}.cdn.example.com/{key}" }, ""},
		{"cdn template key only", func(a *Account) { a.CDNURL = "https://cdn.example.com/static/{key}" }, ""},
		{"cdn ftp", func(a *Account) { a.CDNURL = "ftp://cdn.example.com" }, "CDN URL"},
		{"cdn no scheme", func(a *Account) { a.CDNURL = "cdn.example.com" }, "CDN URL"},
		{"cdn no host", func(a *Account) { a.CDNURL = "https:///x" }, "CDN URL"},
		{"cdn userinfo", func(a *Account) { a.CDNURL = "https://u:p@cdn.example.com" }, "CDN URL"},
		{"cdn query", func(a *Account) { a.CDNURL = "https://cdn.example.com/?a=b" }, "CDN URL"},
		{"cdn template query", func(a *Account) { a.CDNURL = "https://cdn.example.com/{key}?v=1" }, "CDN URL"},
		{"cdn fragment", func(a *Account) { a.CDNURL = "https://cdn.example.com/#f" }, "CDN URL"},
		{"cdn bucket without key", func(a *Account) { a.CDNURL = "https://{bucket}.cdn.example.com" }, "{key}"},
		{"cdn buckets ok", func(a *Account) { a.CDNBuckets = []string{" smoji ", "", "smoji", "my.bucket-2"} }, ""},
		{"cdn buckets invalid", func(a *Account) { a.CDNBuckets = []string{"UPPER"} }, "CDN buckets"},
		{"cdn buckets too short", func(a *Account) { a.CDNBuckets = []string{"ab"} }, "CDN buckets"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := ok
			c.mod(&a)
			_, err := normalizeAccount(a)
			switch {
			case c.want == "" && err != nil:
				t.Fatalf("unexpected error %v", err)
			case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
				t.Fatalf("want error containing %q, got %v", c.want, err)
			}
		})
	}
	// Normalisation: trimmed, defaults, stale fields cleared.
	a, err := normalizeAccount(Account{Name: " n ", Endpoint: " https://h/ ", Credentials: CredDefault, AccessKeyID: "stale", Profile: "stale"})
	if err != nil || a.Name != "n" || a.Endpoint != "https://h" || a.Provider != ProviderS3 || a.ChecksumMode != "auto" || a.AccessKeyID != "" || a.Profile != "" {
		t.Fatalf("%+v %v", a, err)
	}
	if a, _ := normalizeAccount(Account{Name: "n", Credentials: CredAnonymous}); a.Provider != ProviderAWS {
		t.Fatalf("provider default %q", a.Provider)
	}
	a, err = normalizeAccount(Account{Name: "n", Credentials: CredAnonymous, CDNURL: " https://c.example.com// ", CDNBuckets: []string{" bbb ", "", "bbb", "ccc"}})
	if err != nil || a.CDNURL != "https://c.example.com" || !reflect.DeepEqual(a.CDNBuckets, []string{"bbb", "ccc"}) {
		t.Fatalf("cdn normalisation: %+v %v", a, err)
	}
	if a, _ := normalizeAccount(Account{Name: "n", Credentials: CredAnonymous, CDNBuckets: []string{" ", ""}}); a.CDNBuckets != nil {
		t.Fatalf("empty cdn buckets should be nil: %#v", a.CDNBuckets)
	}
}

func TestSaveAccountStoresSecretsInKeyringOnly(t *testing.T) {
	kr := newTestKeyring()
	s, dir := newTestServiceDir(t, "", kr)
	saved, err := s.SaveAccount(staticAccount("minio", "http://localhost:9000"), &Secrets{SecretAccessKey: "TOPSECRET", SessionToken: "TOKEN123"})
	if err != nil {
		t.Fatal(err)
	}
	if saved.ID == "" || saved.ChecksumMode != "auto" {
		t.Fatalf("saved %+v", saved)
	}
	if !kr.has("account/"+saved.ID+"/secret") || !kr.has("account/"+saved.ID+"/session") {
		t.Fatal("secrets not in keyring")
	}
	b, _ := os.ReadFile(s.path("accounts.json"))
	if strings.Contains(string(b), "TOPSECRET") || strings.Contains(string(b), "TOKEN123") {
		t.Fatalf("secret leaked into accounts.json:\n%s", b)
	}
	s2, _ := newTestServiceDir(t, dir, kr)
	if got := s2.Accounts(); len(got) != 1 || !reflect.DeepEqual(got[0], saved) {
		t.Fatalf("reload: %+v", got)
	}
	// Accounts() returns copies.
	s.Accounts()[0].Name = "mutated"
	if s.Accounts()[0].Name != "minio" {
		t.Fatal("Accounts() leaks internal state")
	}
}

func TestSaveAccountValidationErrors(t *testing.T) {
	s := newTestService(t, nil)
	a := staticAccount("x", "http://h")
	if _, err := s.SaveAccount(a, nil); !IsKind(err, ErrInvalid) {
		t.Fatalf("static without secret: %v", err)
	}
	if _, err := s.SaveAccount(a, &Secrets{}); !IsKind(err, ErrInvalid) {
		t.Fatalf("empty secret: %v", err)
	}
	d := a
	d.Credentials = CredDefault
	if _, err := s.SaveAccount(d, &Secrets{SecretAccessKey: "x"}); !IsKind(err, ErrInvalid) {
		t.Fatalf("secrets with default creds: %v", err)
	}
	if _, err := s.SaveAccount(Account{}, nil); !IsKind(err, ErrInvalid) {
		t.Fatalf("empty account: %v", err)
	}
	if len(s.Accounts()) != 0 {
		t.Fatal("invalid accounts must not be saved")
	}
}

func TestSaveAccountUpdateOrdering(t *testing.T) {
	kr := newTestKeyring()
	s := newTestService(t, kr)
	a, err := s.SaveAccount(staticAccount("one", "http://h"), &Secrets{SecretAccessKey: "s0", SessionToken: "t0"})
	if err != nil {
		t.Fatal(err)
	}
	// Update without secrets keeps them and writes nothing to the keyring.
	kr.mu.Lock()
	kr.sets, kr.dels = nil, nil
	kr.mu.Unlock()
	a.Name = "renamed"
	a.PathStyle = false
	if _, err := s.SaveAccount(a, nil); err != nil {
		t.Fatal(err)
	}
	if len(kr.sets) != 0 || len(kr.dels) != 0 {
		t.Fatalf("keep-secret update touched keyring: sets=%v dels=%v", kr.sets, kr.dels)
	}
	if got := s.Accounts()[0]; got.Name != "renamed" || got.PathStyle {
		t.Fatalf("%+v", got)
	}

	// Replacing secrets: the new entry is written BEFORE accounts.json is persisted
	// and the old entries are removed only AFTER it.
	var order []string
	kr.onSet = func(k string) error {
		b, _ := os.ReadFile(s.path("accounts.json"))
		order = append(order, "set "+k+" (json has gen: "+fmt.Sprint(strings.Contains(string(b), `"secretGen": 1`))+")")
		return nil
	}
	kr.onDel = func(k string) error {
		b, _ := os.ReadFile(s.path("accounts.json"))
		order = append(order, "del "+k+" (json has gen: "+fmt.Sprint(strings.Contains(string(b), `"secretGen": 1`))+")")
		return nil
	}
	if _, err := s.SaveAccount(a, &Secrets{SecretAccessKey: "s1"}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"set account/" + a.ID + "/secret.1 (json has gen: false)",
		"del account/" + a.ID + "/secret (json has gen: true)",
		"del account/" + a.ID + "/session (json has gen: true)",
	}
	if strings.Join(order, "|") != strings.Join(want, "|") {
		t.Fatalf("order:\n%s\nwant:\n%s", strings.Join(order, "\n"), strings.Join(want, "\n"))
	}
	kr.onSet, kr.onDel = nil, nil
	if kr.has("account/"+a.ID+"/secret") || kr.has("account/"+a.ID+"/session") || !kr.has("account/"+a.ID+"/secret.1") || kr.has("account/"+a.ID+"/session.1") {
		t.Fatal("keyring state after replacing secrets is wrong")
	}
	// The cached configuration was invalidated: next use reads the new secret.
	rec, _ := s.account(a.ID)
	sec, err := s.readSecrets(testCtx(t), rec)
	if err != nil || sec.SecretAccessKey != "s1" || sec.SessionToken != "" {
		t.Fatalf("secrets %+v %v", sec, err)
	}
}

func TestSaveAccountFailureRollsBack(t *testing.T) {
	kr := newTestKeyring()
	s := newTestService(t, kr)
	a, err := s.SaveAccount(staticAccount("one", "http://h"), &Secrets{SecretAccessKey: "s0"})
	if err != nil {
		t.Fatal(err)
	}
	before := s.Accounts()

	t.Run("session write fails", func(t *testing.T) {
		kr.onSet = func(k string) error {
			if strings.Contains(k, "session") {
				return errors.New("disk full")
			}
			return nil
		}
		defer func() { kr.onSet = nil }()
		if _, err := s.SaveAccount(a, &Secrets{SecretAccessKey: "s1", SessionToken: "t1"}); err == nil {
			t.Fatal("expected error")
		}
		if kr.has("account/" + a.ID + "/secret.1") {
			t.Fatal("secret written before the failure was not rolled back")
		}
	})

	t.Run("persist fails", func(t *testing.T) {
		if err := os.Remove(s.path("accounts.json")); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(s.path("accounts.json"), 0o700); err != nil { // rename onto a directory fails
			t.Fatal(err)
		}
		defer func() {
			os.Remove(s.path("accounts.json"))
		}()
		if _, err := s.SaveAccount(a, &Secrets{SecretAccessKey: "s2", SessionToken: "t2"}); err == nil {
			t.Fatal("expected error")
		}
		if kr.has("account/"+a.ID+"/secret.1") || kr.has("account/"+a.ID+"/session.1") {
			t.Fatal("new keyring entries not rolled back after persist failure")
		}
		if v, _ := kr.Get("account/" + a.ID + "/secret"); v != "s0" {
			t.Fatal("old secret must be untouched")
		}
		if got := s.Accounts(); len(got) != 1 || !reflect.DeepEqual(got[0], before[0]) {
			t.Fatalf("in-memory accounts changed: %+v", got)
		}
		if len(kr.dels) > 0 && kr.dels[len(kr.dels)-1] == "account/"+a.ID+"/secret" {
			t.Fatal("old secret was deleted")
		}
	})
}

func TestSaveAccountOldSecretCleanupFailureReportsButSaves(t *testing.T) {
	kr := newTestKeyring()
	s := newTestService(t, kr)
	a, _ := s.SaveAccount(staticAccount("one", "http://h"), &Secrets{SecretAccessKey: "s0"})
	kr.onDel = func(string) error { return errors.New("keyring busy") }
	got, err := s.SaveAccount(a, &Secrets{SecretAccessKey: "s1"})
	if err == nil || got.ID != a.ID {
		t.Fatalf("want account+error, got %+v %v", got, err)
	}
	if !strings.Contains(err.Error(), "saved") {
		t.Fatalf("error should say the account was saved: %v", err)
	}
	if !kr.has("account/" + a.ID + "/secret.1") {
		t.Fatal("new secret missing")
	}
}

func TestSwitchingAwayFromStaticDeletesSecrets(t *testing.T) {
	kr := newTestKeyring()
	s := newTestService(t, kr)
	a, _ := s.SaveAccount(staticAccount("one", "http://h"), &Secrets{SecretAccessKey: "s0", SessionToken: "t0"})
	a.Credentials = CredDefault
	got, err := s.SaveAccount(a, nil)
	if err != nil || got.AccessKeyID != "" {
		t.Fatalf("%+v %v", got, err)
	}
	if kr.has("account/"+a.ID+"/secret") || kr.has("account/"+a.ID+"/session") {
		t.Fatal("secrets of a non-static account must be removed")
	}
	// Back to static without secrets is invalid.
	a.Credentials, a.AccessKeyID = CredStatic, "AK"
	if _, err := s.SaveAccount(a, nil); !IsKind(err, ErrInvalid) {
		t.Fatalf("static without secret after switch: %v", err)
	}
}

func TestDeleteAccount(t *testing.T) {
	kr := newTestKeyring()
	s, dir := newTestServiceDir(t, "", kr)
	a, _ := s.SaveAccount(staticAccount("one", "http://h"), &Secrets{SecretAccessKey: "s0", SessionToken: "t0"})
	b, _ := s.SaveAccount(staticAccount("two", "http://h"), &Secrets{SecretAccessKey: "s1"})
	if err := s.DeleteAccount("nope"); !IsKind(err, ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	if err := s.DeleteAccount(a.ID); err != nil {
		t.Fatal(err)
	}
	if kr.has("account/"+a.ID+"/secret") || kr.has("account/"+a.ID+"/session") || !kr.has("account/"+b.ID+"/secret") {
		t.Fatal("keyring entries wrong after delete")
	}
	s2, _ := newTestServiceDir(t, dir, kr)
	if got := s2.Accounts(); len(got) != 1 || got[0].ID != b.ID {
		t.Fatalf("persisted accounts %+v", got)
	}
	// Keyring cleanup failure: account is gone, error says so.
	kr.onDel = func(string) error { return errors.New("busy") }
	if err := s.DeleteAccount(b.ID); err == nil || len(s.Accounts()) != 0 {
		t.Fatalf("err=%v accounts=%v", err, s.Accounts())
	}
}

func TestTestAccount(t *testing.T) {
	var auth atomic.Value
	var failing atomic.Bool
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth.Store(r.Header.Get("Authorization"))
		if failing.Load() {
			w.WriteHeader(403)
			fmt.Fprint(w, `<Error><Code>InvalidAccessKeyId</Code><Message>bad key</Message><RequestId>R1</RequestId></Error>`)
			return
		}
		fmt.Fprint(w, listBucketsXML)
	}))
	defer srv2.Close()

	kr := newTestKeyring()
	s := newTestService(t, kr)
	a := staticAccount("t", srv2.URL)
	if err := s.TestAccount(testCtx(t), a, &Secrets{SecretAccessKey: "sk"}); err != nil {
		t.Fatal(err)
	}
	if v, _ := auth.Load().(string); !strings.HasPrefix(v, "AWS4-HMAC-SHA256 Credential=AKIATEST/") {
		t.Fatalf("request not signed with the supplied key: %q", v)
	}
	if len(s.Accounts()) != 0 || len(kr.sets) != 0 {
		t.Fatal("TestAccount must not save anything")
	}
	// Without secrets and without a saved account: invalid.
	if err := s.TestAccount(testCtx(t), a, nil); !IsKind(err, ErrInvalid) {
		t.Fatalf("no secret: %v", err)
	}
	// With a saved account and nil secrets the stored secret is used.
	saved, _ := s.SaveAccount(a, &Secrets{SecretAccessKey: "stored"})
	if err := s.TestAccount(testCtx(t), saved, nil); err != nil {
		t.Fatal(err)
	}
	// Server rejects: classified error with code and request id.
	failing.Store(true)
	err := s.TestAccount(testCtx(t), saved, nil)
	var oe *OpError
	if !errors.As(err, &oe) || oe.Kind != ErrAccessDenied || oe.Code != "InvalidAccessKeyId" || oe.RequestID != "R1" {
		t.Fatalf("got %#v", err)
	}
	if err := s.TestAccount(testCtx(t), Account{}, nil); !IsKind(err, ErrInvalid) {
		t.Fatalf("invalid account: %v", err)
	}
}
