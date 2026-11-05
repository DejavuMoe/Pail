package app

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// ---- proxy setting -----------------------------------------------------------

func clearProxyEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy"} {
		t.Setenv(k, "")
	}
}

func TestProxyProblems(t *testing.T) {
	ok := ProxySettings{Scheme: ProxySOCKS5, Host: "127.0.0.1", Port: 7890}
	for name, c := range map[string]struct {
		mod         func(*ProxySettings)
		newPassword bool
		want        ProxyProblem
	}{
		"valid":                    {func(*ProxySettings) {}, false, ProxyOK},
		"none":                     {func(p *ProxySettings) { *p = ProxySettings{} }, false, ProxyOK},
		"none ignores the rest":    {func(p *ProxySettings) { *p = ProxySettings{Host: "!!", Port: -4} }, false, ProxyOK},
		"http":                     {func(p *ProxySettings) { p.Scheme = ProxyHTTP }, false, ProxyOK},
		"https host name":          {func(p *ProxySettings) { p.Scheme, p.Host = ProxyHTTPS, "proxy.example.com" }, false, ProxyOK},
		"upper case scheme":        {func(p *ProxySettings) { p.Scheme = "SOCKS5" }, false, ProxyOK},
		"ipv6":                     {func(p *ProxySettings) { p.Host = "::1" }, false, ProxyOK},
		"ipv6 in brackets":         {func(p *ProxySettings) { p.Host = "[::1]" }, false, ProxyOK},
		"unknown scheme":           {func(p *ProxySettings) { p.Scheme = "ftp" }, false, ProxyBadScheme},
		"no host":                  {func(p *ProxySettings) { p.Host = "  " }, false, ProxyHostMissing},
		"host with a scheme":       {func(p *ProxySettings) { p.Host = "http://127.0.0.1" }, false, ProxyHostInvalid},
		"host with a port":         {func(p *ProxySettings) { p.Host = "127.0.0.1:7890" }, false, ProxyHostInvalid},
		"host with a space":        {func(p *ProxySettings) { p.Host = "my proxy" }, false, ProxyHostInvalid},
		"host with a path":         {func(p *ProxySettings) { p.Host = "proxy/x" }, false, ProxyHostInvalid},
		"port 0":                   {func(p *ProxySettings) { p.Port = 0 }, false, ProxyPortInvalid},
		"port too big":             {func(p *ProxySettings) { p.Port = 65536 }, false, ProxyPortInvalid},
		"user name":                {func(p *ProxySettings) { p.Username = "robin" }, false, ProxyOK},
		"user name and password":   {func(p *ProxySettings) { p.Username = "robin" }, true, ProxyOK},
		"password without user":    {func(p *ProxySettings) {}, true, ProxyUserMissing},
		"stored password, no user": {func(p *ProxySettings) { p.HasPassword = true }, false, ProxyUserMissing},
		"control characters":       {func(p *ProxySettings) { p.Username = "ro\nbin" }, false, ProxyUserInvalid},
		"long user name":           {func(p *ProxySettings) { p.Username = strings.Repeat("u", 256) }, false, ProxyUserInvalid},
	} {
		p := ok
		c.mod(&p)
		if got := p.Problem(c.newPassword); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
}

func TestProxyAddress(t *testing.T) {
	for name, c := range map[string]struct {
		p    ProxySettings
		pw   string
		want string
	}{
		"none":                {ProxySettings{}, "", ""},
		"plain":               {ProxySettings{Scheme: "socks5", Host: "127.0.0.1", Port: 7890}, "", "socks5://127.0.0.1:7890"},
		"ipv6":                {ProxySettings{Scheme: "http", Host: "::1", Port: 3128}, "", "http://[::1]:3128"},
		"user":                {ProxySettings{Scheme: "http", Host: "proxy.lan", Port: 8080, Username: "robin"}, "", "http://robin@proxy.lan:8080"},
		"password is encoded": {ProxySettings{Scheme: "https", Host: "proxy.lan", Port: 443, Username: "ro bin", HasPassword: true}, "p@ss:w/rd", "https://ro%20bin:p%40ss%3Aw%2Frd@proxy.lan:443"},
	} {
		if got := c.p.address(c.pw); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
}

func readSettingsFile(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestProxySettingsSaveReloadAndClear(t *testing.T) {
	s, dir := newTestServiceDir(t, "", nil)
	if got := s.Settings().Proxy; got != (ProxySettings{}) {
		t.Fatalf("the default proxy is %+v; it must follow the environment", got)
	}
	want := ProxySettings{Scheme: ProxySOCKS5, Host: "127.0.0.1", Port: 7890}
	if err := s.SaveProxy(ProxySettings{Scheme: " SOCKS5 ", Host: " [127.0.0.1] ", Port: 7890}, nil); err != nil {
		t.Fatal(err)
	}
	if got := s.Settings().Proxy; got != want {
		t.Fatalf("saved %+v, want it cleaned to %+v", got, want)
	}
	s2, _ := newTestServiceDir(t, dir, nil)
	if got := s2.Settings().Proxy; got != want {
		t.Fatalf("reloaded %+v", got)
	}
	// SaveSettings never changes it, whatever it is given.
	st := s.Settings()
	st.Proxy = ProxySettings{Scheme: ProxyHTTP, Host: "elsewhere", Port: 1}
	st.Theme = "dark"
	if err := s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	if got := s.Settings(); got.Proxy != want || got.Theme != "dark" {
		t.Fatalf("after SaveSettings: %+v", got)
	}
	// No scheme removes everything and the key from the file.
	if err := s.SaveProxy(ProxySettings{Host: "ignored", Port: 5, Username: "ignored"}, nil); err != nil {
		t.Fatal(err)
	}
	if got := s.Settings().Proxy; got != (ProxySettings{}) {
		t.Fatalf("after clearing: %+v", got)
	}
	if f := readSettingsFile(t, dir); strings.Contains(f, "proxy") {
		t.Fatalf("settings.json keeps an empty proxy: %s", f)
	}
}

func TestInvalidProxyIsRejectedAndKeepsTheOldSetting(t *testing.T) {
	s := newTestService(t, nil)
	good := ProxySettings{Scheme: ProxyHTTP, Host: "127.0.0.1", Port: 7890, Username: "robin"}
	pw := "hunter2"
	if err := s.SaveProxy(good, &pw); err != nil {
		t.Fatal(err)
	}
	want := s.Settings().Proxy
	for _, bad := range []ProxySettings{
		{Scheme: "ftp", Host: "h", Port: 1},
		{Scheme: ProxyHTTP, Port: 1},
		{Scheme: ProxyHTTP, Host: "h"},
		{Scheme: ProxyHTTP, Host: "h:1", Port: 1},
		{Scheme: ProxyHTTP, Host: "h", Port: 99999},
	} {
		err := s.SaveProxy(bad, nil)
		if err == nil || KindOf(err) != ErrInvalid {
			t.Errorf("%+v: want Invalid, got %v", bad, err)
		}
	}
	// A password with no user name is refused, and does not reach the store.
	empty := ""
	long := strings.Repeat("p", 256)
	for _, c := range []struct {
		p  ProxySettings
		pw *string
	}{
		{ProxySettings{Scheme: ProxyHTTP, Host: "h", Port: 1}, &pw},
		{ProxySettings{}, &pw},
		{good, &long},
	} {
		if err := s.SaveProxy(c.p, c.pw); KindOf(err) != ErrInvalid {
			t.Errorf("%+v: want Invalid, got %v", c.p, err)
		}
	}
	_ = empty
	if got := s.Settings().Proxy; got != want {
		t.Fatalf("rejected proxies changed the setting to %+v", got)
	}
}

func TestProxyPasswordLivesInTheSecretStore(t *testing.T) {
	kr := newTestKeyring()
	s, dir := newTestServiceDir(t, "", kr)
	p := ProxySettings{Scheme: ProxySOCKS5, Host: "proxy.lan", Port: 1080, Username: "robin"}
	pw := "correct horse battery staple"
	if err := s.SaveProxy(p, &pw); err != nil {
		t.Fatal(err)
	}
	if got := s.Settings().Proxy; !got.HasPassword || got.Username != "robin" {
		t.Fatalf("saved %+v", got)
	}
	if !kr.has(proxyPasswordKey) {
		t.Fatal("the password is not in the secret store")
	}
	if f := readSettingsFile(t, dir); strings.Contains(f, pw) || strings.Contains(f, "horse") || !strings.Contains(f, `"hasPassword": true`) {
		t.Fatalf("settings.json: %s", f)
	}

	// nil keeps it, even when the address changes.
	p.Port = 1081
	if err := s.SaveProxy(p, nil); err != nil {
		t.Fatal(err)
	}
	if !s.Settings().Proxy.HasPassword || !kr.has(proxyPasswordKey) {
		t.Fatal("changing the address lost the password")
	}
	addr, err := s.proxyAddress(testCtx(t), s.Settings().Proxy)
	if err != nil || addr != "socks5://robin:correct%20horse%20battery%20staple@proxy.lan:1081" {
		t.Fatalf("address %q, %v", addr, err)
	}

	// A new value replaces it.
	pw2 := "another"
	if err := s.SaveProxy(p, &pw2); err != nil {
		t.Fatal(err)
	}
	if addr, _ := s.proxyAddress(testCtx(t), s.Settings().Proxy); !strings.Contains(addr, ":another@") {
		t.Fatalf("address after replacing: %q", addr)
	}

	// An empty password removes it but keeps the user name.
	none := ""
	if err := s.SaveProxy(p, &none); err != nil {
		t.Fatal(err)
	}
	if s.Settings().Proxy.HasPassword || kr.has(proxyPasswordKey) {
		t.Fatal("an empty password was kept")
	}
	if addr, _ := s.proxyAddress(testCtx(t), s.Settings().Proxy); addr != "socks5://robin@proxy.lan:1081" {
		t.Fatalf("address without password: %q", addr)
	}

	// Clearing the user name removes the password; so does removing the proxy.
	for name, next := range map[string]ProxySettings{
		"clear the user name": {Scheme: ProxySOCKS5, Host: "proxy.lan", Port: 1081},
		"remove the proxy":    {},
	} {
		if err := s.SaveProxy(ProxySettings{Scheme: ProxySOCKS5, Host: "proxy.lan", Port: 1081, Username: "robin"}, &pw); err != nil {
			t.Fatal(err)
		}
		if err := s.SaveProxy(next, nil); err != nil {
			t.Fatal(err)
		}
		if s.Settings().Proxy.HasPassword || kr.has(proxyPasswordKey) {
			t.Fatalf("%s: the password is still stored", name)
		}
	}

	// The password never reaches the log.
	s.applyLogging(true)
	if err := s.SaveProxy(p, &pw); err != nil {
		t.Fatal(err)
	}
	s.applyLogging(false)
	if logs, _ := filepath.Glob(filepath.Join(dir, "logs", "*")); len(logs) > 0 {
		for _, f := range logs {
			if b, _ := os.ReadFile(f); strings.Contains(string(b), "battery") {
				t.Fatalf("%s contains the proxy password", f)
			}
		}
	}
}

func TestProxyPasswordFailuresLeaveTheOldState(t *testing.T) {
	kr := newTestKeyring()
	s, dir := newTestServiceDir(t, "", kr)
	p := ProxySettings{Scheme: ProxyHTTP, Host: "proxy.lan", Port: 3128, Username: "robin"}
	old := "old password"
	if err := s.SaveProxy(p, &old); err != nil {
		t.Fatal(err)
	}
	before := s.Settings().Proxy

	// The store refuses the write: nothing changes.
	kr.onSet = func(string) error { return errors.New("the keychain refused") }
	fresh := "new password"
	p2 := p
	p2.Port = 9999
	if err := s.SaveProxy(p2, &fresh); err == nil {
		t.Fatal("a refused write was reported as saved")
	}
	kr.onSet = nil
	if s.Settings().Proxy != before {
		t.Fatalf("settings changed: %+v", s.Settings().Proxy)
	}
	if v, _ := kr.Keyring.Get(proxyPasswordKey); v != old {
		t.Fatalf("the stored password is %q", v)
	}

	// settings.json cannot be written: the new password is taken back.
	cfg := filepath.Join(dir, "settings.json")
	if err := os.Remove(cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cfg, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveProxy(p2, &fresh); err == nil {
		t.Fatal("a failed write of settings.json was reported as saved")
	}
	if v, _ := kr.Keyring.Get(proxyPasswordKey); v != old {
		t.Fatalf("the new password was left in the store: %q", v)
	}
	if s.Settings().Proxy != before {
		t.Fatalf("settings changed: %+v", s.Settings().Proxy)
	}
}

func TestMissingProxyPasswordIsReported(t *testing.T) {
	kr := newTestKeyring()
	s := newTestService(t, kr)
	a, err := s.SaveAccount(staticAccount("remote", "http://s3.unreachable.test:9000"), &Secrets{SecretAccessKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	pw := "pw"
	if err := s.SaveProxy(ProxySettings{Scheme: ProxyHTTP, Host: "127.0.0.1", Port: 1, Username: "u"}, &pw); err != nil {
		t.Fatal(err)
	}
	kr.Keyring.Delete(proxyPasswordKey)
	if _, err := s.Buckets(testCtx(t), a.ID); !IsKind(err, ErrNotFound) || !strings.Contains(err.Error(), "proxy password") {
		t.Fatalf("want NotFound naming the proxy password, got %v", err)
	}
}

func TestProxyPasswordFollowsTheSecretBackendAndReset(t *testing.T) {
	s := newTestService(t, nil)
	p := ProxySettings{Scheme: ProxySOCKS5, Host: "proxy.lan", Port: 1080, Username: "robin"}
	pw := "moves with the others"
	if err := s.SaveProxy(p, &pw); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSecretBackend(testCtx(t), SecretBackendVault, testPW); err != nil {
		t.Fatal(err)
	}
	if addr, err := s.proxyAddress(testCtx(t), s.Settings().Proxy); err != nil || !strings.Contains(addr, "moves%20with%20the%20others") {
		t.Fatalf("after moving to the vault: %q, %v", addr, err)
	}
	if err := s.SetSecretBackend(testCtx(t), SecretBackendKeyring, ""); err != nil {
		t.Fatal(err)
	}
	if addr, err := s.proxyAddress(testCtx(t), s.Settings().Proxy); err != nil || !strings.Contains(addr, "moves%20with%20the%20others") {
		t.Fatalf("after moving back: %q, %v", addr, err)
	}

	// A locked vault refuses to save a password, and the settings stay as they were.
	if err := s.SetSecretBackend(testCtx(t), SecretBackendVault, testPW); err != nil {
		t.Fatal(err)
	}
	s.LockVault()
	next := "x"
	if err := s.SaveProxy(p, &next); !IsKind(err, ErrLocked) {
		t.Fatalf("saving a password while locked: %v", err)
	}
	// Forgetting the vault password loses the proxy password and says so.
	res, err := s.ResetVault()
	if err != nil {
		t.Fatal(err)
	}
	if !res.Proxy || s.Settings().Proxy.HasPassword || s.Settings().Proxy.Username != "robin" {
		t.Fatalf("reset: %+v, proxy %+v", res, s.Settings().Proxy)
	}
	if addr, err := s.proxyAddress(testCtx(t), s.Settings().Proxy); err != nil || addr != "socks5://robin@proxy.lan:1080" {
		t.Fatalf("after the reset: %q, %v", addr, err)
	}
}

func TestCorruptProxyInSettingsFileFailsStartup(t *testing.T) {
	for _, body := range []string{
		`{"proxy":{"scheme":"gopher","host":"x","port":1}}`,
		`{"proxy":{"scheme":"http","host":"x"}}`,
		`{"proxy":{"scheme":"http","host":"x","port":1,"hasPassword":true}}`, // a password needs a user name
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := New(Options{ConfigDir: dir, Keyring: NewMemoryKeyring()}); err == nil {
			t.Errorf("%s was accepted", body)
		}
	}
}

// An account's requests go through the proxy, with its login, and a change of
// the proxy reaches clients that were already built.
func TestProxyReachesRequestsAndRebuildsClients(t *testing.T) {
	clearProxyEnv(t)
	var viaProxy atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := (&http.Request{Header: http.Header{"Authorization": {r.Header.Get("Proxy-Authorization")}}}).BasicAuth(); !ok || u != "robin" || p != "p@ss:w/rd" {
			w.Header().Set("Proxy-Authenticate", `Basic realm="proxy"`)
			http.Error(w, "login required", http.StatusProxyAuthRequired)
			return
		}
		viaProxy.Add(1)
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, listBucketsXML)
	}))
	defer proxy.Close()
	host, port := splitHostPort(t, proxy.URL)

	s := newTestService(t, nil)
	// The endpoint's name does not resolve: only the proxy can answer for it.
	a, err := s.SaveAccount(staticAccount("remote", "http://s3.unreachable.test:9000"), &Secrets{SecretAccessKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.storageClient(testCtx(t), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := s.storageClient(testCtx(t), a.ID); again != before {
		t.Fatal("the client is not cached")
	}

	pw := "p@ss:w/rd"
	if err := s.SaveProxy(ProxySettings{Scheme: ProxyHTTP, Host: host, Port: port, Username: "robin"}, &pw); err != nil {
		t.Fatal(err)
	}
	after, err := s.storageClient(testCtx(t), a.ID)
	if err != nil || after == before {
		t.Fatalf("the client was not rebuilt after the proxy changed (%v)", err)
	}
	bs, err := s.Buckets(testCtx(t), a.ID)
	if err != nil || len(bs) != 2 {
		t.Fatalf("Buckets through the proxy: %v %v", bs, err)
	}
	if viaProxy.Load() == 0 {
		t.Fatal("the request did not go through the proxy")
	}

	// Saving unrelated settings keeps the cached client.
	st := s.Settings()
	st.Theme = "dark"
	if err := s.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	if kept, _ := s.storageClient(testCtx(t), a.ID); kept != after {
		t.Fatal("an unrelated setting rebuilt the clients")
	}

	// A wrong password is refused by the proxy, and the connection test says so.
	wrong := "wrong"
	if err := s.SaveProxy(ProxySettings{Scheme: ProxyHTTP, Host: host, Port: port, Username: "robin"}, &wrong); err != nil {
		t.Fatal(err)
	}
	if err := s.TestAccount(testCtx(t), staticAccount("remote", "http://s3.unreachable.test:9000"), &Secrets{SecretAccessKey: "k"}); err == nil {
		t.Fatal("the connection test passed with a wrong proxy password")
	}
	if err := s.SaveProxy(ProxySettings{Scheme: ProxyHTTP, Host: host, Port: port, Username: "robin"}, &pw); err != nil {
		t.Fatal(err)
	}
	if err := s.TestAccount(testCtx(t), staticAccount("remote", "http://s3.unreachable.test:9000"), &Secrets{SecretAccessKey: "k"}); err != nil {
		t.Fatalf("the connection test through the proxy: %v", err)
	}
}

func splitHostPort(t *testing.T, rawURL string) (string, int) {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	return u.Hostname(), port
}

// ---- bucket aliases --------------------------------------------------------------

func aliasAccount(t *testing.T, s *Service) Account {
	t.Helper()
	a, err := s.SaveAccount(staticAccount("main", "http://localhost:9000"), &Secrets{SecretAccessKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestBucketAliasSetClearAndPersist(t *testing.T) {
	s, dir := newTestServiceDir(t, "", nil)
	a := aliasAccount(t, s)
	if got := s.BucketAlias(a.ID, "alpha"); got != "" {
		t.Fatalf("alias before setting: %q", got)
	}
	if err := s.SetBucketAlias(a.ID, "alpha", "  Photos 2024  "); err != nil {
		t.Fatal(err)
	}
	if err := s.SetBucketAlias(a.ID, "beta", "备份"); err != nil {
		t.Fatal(err)
	}
	if got := s.BucketAlias(a.ID, "alpha"); got != "Photos 2024" {
		t.Fatalf("alias = %q, want the trimmed text", got)
	}
	want := map[string]string{"alpha": "Photos 2024", "beta": "备份"}
	if got := s.Accounts()[0].BucketAliases; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("Accounts() aliases = %v", got)
	}

	s2, _ := newTestServiceDir(t, dir, nil)
	if got := s2.BucketAlias(a.ID, "beta"); got != "备份" {
		t.Fatalf("reloaded alias = %q", got)
	}

	// Renaming, then clearing with "" and with the bucket's own name.
	if err := s.SetBucketAlias(a.ID, "alpha", "Photos"); err != nil {
		t.Fatal(err)
	}
	if got := s.BucketAlias(a.ID, "alpha"); got != "Photos" {
		t.Fatalf("renamed alias = %q", got)
	}
	if err := s.SetBucketAlias(a.ID, "alpha", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SetBucketAlias(a.ID, "beta", "beta"); err != nil {
		t.Fatal(err)
	}
	if got := s.Accounts()[0].BucketAliases; got != nil {
		t.Fatalf("aliases after clearing both: %v", got)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "accounts.json")); strings.Contains(string(b), "bucketAliases") {
		t.Fatalf("accounts.json keeps an empty alias map: %s", b)
	}
	// Clearing what is not set is not an error.
	if err := s.SetBucketAlias(a.ID, "gamma", ""); err != nil {
		t.Fatal(err)
	}
}

func TestBucketAliasValidation(t *testing.T) {
	s := newTestService(t, nil)
	a := aliasAccount(t, s)
	if err := s.SetBucketAlias(a.ID, "alpha", "Photos"); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct{ acct, bucket, alias string }{
		"unknown account": {"nope", "alpha", "x"},
		"no bucket":       {a.ID, " ", "x"},
		"too long":        {a.ID, "beta", strings.Repeat("长", MaxAliasRunes+1)},
		"line break":      {a.ID, "beta", "two\nlines"},
		"duplicate":       {a.ID, "beta", "Photos"},
	} {
		if err := s.SetBucketAlias(c.acct, c.bucket, c.alias); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := s.SetBucketAlias("nope", "alpha", "x"); !IsKind(err, ErrNotFound) {
		t.Errorf("unknown account: %v", err)
	}
	// The longest alias is fine, counted in characters rather than bytes.
	if err := s.SetBucketAlias(a.ID, "beta", strings.Repeat("长", MaxAliasRunes)); err != nil {
		t.Errorf("an alias of %d characters: %v", MaxAliasRunes, err)
	}
	// The same alias may be used by the same bucket again, and by another account.
	if err := s.SetBucketAlias(a.ID, "alpha", "Photos"); err != nil {
		t.Errorf("setting the same alias again: %v", err)
	}
	other, err := s.SaveAccount(staticAccount("other", "http://localhost:9001"), &Secrets{SecretAccessKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetBucketAlias(other.ID, "alpha", "Photos"); err != nil {
		t.Errorf("the same alias in another account: %v", err)
	}
}

func TestSaveAccountKeepsBucketAliases(t *testing.T) {
	s := newTestService(t, nil)
	a := aliasAccount(t, s)
	if err := s.SetBucketAlias(a.ID, "alpha", "Photos"); err != nil {
		t.Fatal(err)
	}
	// An edit made from a copy that knows nothing of the aliases, or that tries to set them.
	edit := a
	edit.Name = "renamed"
	edit.BucketAliases = nil
	saved, err := s.SaveAccount(edit, nil)
	if err != nil {
		t.Fatal(err)
	}
	if saved.BucketAliases["alpha"] != "Photos" || s.BucketAlias(a.ID, "alpha") != "Photos" {
		t.Fatalf("SaveAccount lost the aliases: %v", saved.BucketAliases)
	}
	edit.BucketAliases = map[string]string{"alpha": "Hijack", "beta": "New"}
	if _, err := s.SaveAccount(edit, nil); err != nil {
		t.Fatal(err)
	}
	if got := s.Accounts()[0].BucketAliases; len(got) != 1 || got["alpha"] != "Photos" {
		t.Fatalf("SaveAccount changed the aliases: %v", got)
	}
	// A new account never starts with aliases.
	fresh := staticAccount("fresh", "http://localhost:9002")
	fresh.BucketAliases = map[string]string{"x": "y"}
	f, err := s.SaveAccount(fresh, &Secrets{SecretAccessKey: "k"})
	if err != nil || f.BucketAliases != nil {
		t.Fatalf("new account: %v %v", f.BucketAliases, err)
	}
}

func TestAccountsReturnsACopyOfTheAliases(t *testing.T) {
	s := newTestService(t, nil)
	a := aliasAccount(t, s)
	if err := s.SetBucketAlias(a.ID, "alpha", "Photos"); err != nil {
		t.Fatal(err)
	}
	s.Accounts()[0].BucketAliases["alpha"] = "changed by the caller"
	if got := s.BucketAlias(a.ID, "alpha"); got != "Photos" {
		t.Fatalf("a caller edited the saved alias: %q", got)
	}
}

func TestAliasDoesNotChangeBucketRequests(t *testing.T) {
	var paths []string
	srv := stubS3(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?><ListBucketResult><Name>alpha</Name><IsTruncated>false</IsTruncated></ListBucketResult>`)
	})
	s := newTestService(t, nil)
	a, err := s.SaveAccount(staticAccount("main", srv.URL), &Secrets{SecretAccessKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetBucketAlias(a.ID, "alpha", "Photos"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.List(testCtx(t), Location{AccountID: a.ID, Bucket: "alpha"}, ListOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 || !strings.HasPrefix(paths[len(paths)-1], "GET /alpha") {
		t.Fatalf("requests with an alias set: %v", paths)
	}
}

func TestDeleteBucketForgetsItsAlias(t *testing.T) {
	srv := stubS3(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, "unexpected", 500)
	})
	s := newTestService(t, nil)
	a, err := s.SaveAccount(staticAccount("main", srv.URL), &Secrets{SecretAccessKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range []string{"alpha", "beta"} {
		if err := s.SetBucketAlias(a.ID, b, "label "+b); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeleteBucket(testCtx(t), a.ID, "alpha"); err != nil {
		t.Fatal(err)
	}
	if got := s.BucketAlias(a.ID, "alpha"); got != "" {
		t.Fatalf("the deleted bucket keeps its alias %q", got)
	}
	if got := s.BucketAlias(a.ID, "beta"); got != "label beta" {
		t.Fatalf("another bucket's alias changed: %q", got)
	}
}
