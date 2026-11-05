package main

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/egoist/mygo/ui"
)

// ---- how long the last operation took ------------------------------------------------------

func TestHumanDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0:                      "0 ms",
		-time.Second:           "0 ms",
		850 * time.Millisecond: "850 ms",
		999*time.Millisecond + 600*time.Microsecond: "999 ms",
		time.Second:                             "1.0 s",
		3500 * time.Millisecond:                 "3.5 s",
		3540 * time.Millisecond:                 "3.5 s",
		3560 * time.Millisecond:                 "3.6 s",
		9949 * time.Millisecond:                 "9.9 s",
		9951 * time.Millisecond:                 "10 s", // never "10.0 s"
		10 * time.Second:                        "10 s",
		42400 * time.Millisecond:                "42 s",
		59600 * time.Millisecond:                "1 m 00 s", // never "60 s"
		time.Minute + 5*time.Second:             "1 m 05 s",
		59*time.Minute + 59*time.Second:         "59 m 59 s",
		59*time.Minute + 59500*time.Millisecond: "1 h 00 m",
		time.Hour + 7*time.Minute:               "1 h 07 m",
		26*time.Hour + 30*time.Minute:           "26 h 30 m",
	} {
		if got := humanDuration(d); got != want {
			t.Errorf("humanDuration(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestGroupDuration(t *testing.T) {
	t0 := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	at := func(s float64) time.Time { return t0.Add(time.Duration(s * float64(time.Second))) }
	for name, c := range map[string]struct {
		ts   []Task
		want time.Duration
		ok   bool
	}{
		"none":        {nil, 0, false},
		"not started": {[]Task{{Created: t0}}, 0, false},
		"one task":    {[]Task{{Started: at(1), Finished: at(4.5)}}, 3500 * time.Millisecond, true},
		"first start to last finish": {[]Task{
			{Started: at(2), Finished: at(3)}, {Started: at(1), Finished: at(2.5)}, {Started: at(2.5), Finished: at(9)},
		}, 8 * time.Second, true},
		"a task that never ran is ignored": {[]Task{{Started: at(1), Finished: at(2)}, {}}, time.Second, true},
		// A clock that ticks in half-milliseconds reads 0 for a fast task: still an operation.
		"no time passed": {[]Task{{Started: at(1), Finished: at(1)}}, 0, true},
	} {
		if got, ok := groupDuration(c.ts); got != c.want || ok != c.ok {
			t.Errorf("%s: %v, %v; want %v, %v", name, got, ok, c.want, c.ok)
		}
	}
}
func TestStatusBarShowsHowLongTheLastOperationTook(t *testing.T) {
	h := newRawHarness(t, "light", true)
	// The buckets were listed when the app started, then the first bucket was opened.
	if h.a.took.what != tookList {
		t.Fatalf("after the first load: %+v", h.a.took)
	}
	h.a.loadBuckets(false)
	h.settle()
	if h.a.took.what != tookBuckets {
		t.Fatalf("after listing the buckets: %+v", h.a.took)
	}
	h.mustHave(locales.StatusbarTookBuckets(humanDuration(h.a.took.d)))

	// Opening a folder is the next operation, and replaces it.
	h.a.openBucket("acme-assets")
	h.settle()
	if h.a.took.what != tookList {
		t.Fatalf("after listing a bucket: %+v", h.a.took)
	}
	h.mustHave(humanDuration(h.a.took.d), locales.StatusbarTookList(humanDuration(h.a.took.d)))
	h.snapBoth("statusbar-took")

	// A longer time reads as seconds, in either language.
	h.a.took = opTime{what: tookUpload, d: 3500 * time.Millisecond}
	h.tt.Frame()
	h.mustHave("3.5 s", "The upload took 3.5 s")
	h.setLocale("zh-Hans")
	h.mustHave("3.5 s", "上传用时 3.5 s")
	h.a.took = opTime{what: tookDelete, d: 12 * time.Second}
	h.tt.Frame()
	h.mustHave("12 s", "删除用时 12 s")
	h.setLocale("en")

	// Another account's time says nothing here: switching clears it.
	h.a.took = opTime{what: tookList, d: time.Second}
	h.a.selectAccount("")
	if h.a.took.what != "" {
		t.Fatalf("the time survived an account switch: %+v", h.a.took)
	}
}

func TestUploadTimeIsNotReplacedByTheListThatFollowsIt(t *testing.T) {
	h := newHarness(t, "light")
	p := writeTemp(t, "timed.bin", []byte(strings.Repeat("Pail", 1024)))
	h.a.uploadPaths([]string{p})
	h.waitToast("Uploaded 1 file")
	h.settle()
	h.waitFor("timed.bin listed", func() bool { return h.has("timed.bin") })
	if h.a.took.what != tookUpload {
		t.Fatalf("after the upload: %+v; the refresh that follows it must keep the upload's time", h.a.took)
	}
	h.mustHave(locales.StatusbarTookUpload(humanDuration(h.a.took.d)))

	// The user's own navigation is a new operation.
	h.a.openPrefix("assets/")
	h.settle()
	if h.a.took.what != tookList {
		t.Fatalf("after opening a folder: %+v", h.a.took)
	}

	// So is a delete, again followed by a refresh that keeps its time.
	h.a.openScope(core.Location{AccountID: h.a.accountID, Bucket: bkt})
	h.settle()
	h.a.sel.Clear()
	h.a.sel.Add("timed.bin")
	h.a.askDelete()
	h.settle()
	h.mustClick("Delete 1 item")
	h.waitToast("Deleted 1 object")
	h.settle()
	if h.a.took.what != tookDelete {
		t.Fatalf("after the delete: %+v", h.a.took)
	}
}

// ---- proxy server --------------------------------------------------------------------------

// pickProxyProtocol opens the protocol drop-down of the Network section and
// chooses name.
func (h *harness) pickProxyProtocol(row, current, name string) {
	h.t.Helper()
	h.showSetting(row)
	h.mustClick(current)
	h.mustClick(name)
}

func TestProxyFieldsAreAppliedTogether(t *testing.T) {
	h := newHarness(t, "light")
	h.openToolView("settings")
	h.mustHave("NETWORK", "Proxy server", "From environment")
	h.mustNotHave("Host and port") // nothing to fill in while the environment decides
	if h.hasExactText("Apply") {
		t.Fatal("Apply shows with nothing to apply")
	}
	h.snapBoth("settings-proxy")
	if h.env.Svc.Settings().Proxy != (core.ProxySettings{}) {
		t.Fatalf("the proxy starts as %+v; it must follow the environment", h.env.Svc.Settings().Proxy)
	}

	// Choosing a protocol shows the fields and saves nothing yet.
	h.pickProxyProtocol("Proxy server", "From environment", "SOCKS5")
	h.mustHave("SOCKS5", "Host and port", "User name (optional)", "Password (optional)")
	if !h.hasExactText("Apply") {
		t.Fatal("no Apply button")
	}
	if h.env.Svc.Settings().Proxy != (core.ProxySettings{}) {
		t.Fatal("choosing a protocol saved a proxy that has no host")
	}
	h.snapBoth("settings-proxy-fields")

	// Typing in the host field and pressing Enter applies; the port is missing.
	h.settingField("Host and port")
	h.typeAndEnter("127.0.0.1")
	h.mustHave("Enter a port from 1 to 65535")
	if h.env.Svc.Settings().Proxy != (core.ProxySettings{}) {
		t.Fatal("a proxy without a port was saved")
	}

	// Each problem is explained under its field and nothing is written.
	f := &h.a.sf.proxy
	for _, c := range []struct{ host, port, user, pass, want string }{
		{"", "7890", "", "", "Enter a host name or an IP address"},
		{"http://127.0.0.1", "7890", "", "", "Use only a host name or an IP address, without http:// or a port"},
		{"127.0.0.1:7890", "7890", "", "", "Use only a host name or an IP address, without http:// or a port"},
		{"127.0.0.1", "abc", "", "", "Enter a port from 1 to 65535"},
		{"127.0.0.1", "0", "", "", "Enter a port from 1 to 65535"},
		{"127.0.0.1", "70000", "", "", "Enter a port from 1 to 65535"},
		{"127.0.0.1", "7890", "", "secret", "A password needs a user name"},
	} {
		f.host, f.port, f.user, f.pass = c.host, c.port, c.user, c.pass
		h.mustClick("Apply")
		h.mustHave(c.want)
		if h.env.Svc.Settings().Proxy != (core.ProxySettings{}) {
			t.Fatalf("%+v was saved", c)
		}
	}

	// A good address is saved as it was cleaned.
	f.host, f.port, f.user, f.pass = " [127.0.0.1] ", "7890", "", ""
	h.mustClick("Apply")
	h.waitFor("proxy saved", func() bool { return h.env.Svc.Settings().Proxy.Scheme == core.ProxySOCKS5 })
	h.settle()
	if got := h.env.Svc.Settings().Proxy; got != (core.ProxySettings{Scheme: core.ProxySOCKS5, Host: "127.0.0.1", Port: 7890}) {
		t.Fatalf("saved %+v", got)
	}
	if f.host != "127.0.0.1" || f.pass != "" || f.tried {
		t.Fatalf("the form after saving: %+v", *f)
	}
	h.mustHave("Saved")
	h.mustNotHave("Enter a port from 1 to 65535")
	if h.hasExactText("Apply") {
		t.Fatal("Apply still shows with nothing left to apply") // nothing left to apply
	}

	// A user name and password: the password goes to the secret store.
	f.user, f.pass = "robin", "s3cret-pw"
	h.tt.Frame()
	if !h.hasExactText("Apply") {
		t.Fatal("no Apply for a typed password")
	}
	h.mustClick("Apply")
	h.waitFor("login saved", func() bool { return h.env.Svc.Settings().Proxy.HasPassword })
	h.settle()
	if got := h.env.Svc.Settings().Proxy; got.Username != "robin" || got.Host != "127.0.0.1" || got.Port != 7890 {
		t.Fatalf("saved %+v", got)
	}
	if raw, err := os.ReadFile(filepath.Join(h.env.Dir, "settings.json")); err != nil || strings.Contains(string(raw), "s3cret") {
		t.Fatalf("settings.json holds the password (%v): %s", err, raw)
	}
	if f.pass != "" || f.user != "robin" {
		t.Fatalf("the form after saving: %+v", *f)
	}

	// Another setting keeps the proxy, and a new form shows it.
	h.editSetting("Part size (MiB)", "24")
	h.waitFor("part size saved", func() bool { return h.env.Svc.Settings().PartSizeMiB == 24 })
	if got := h.env.Svc.Settings().Proxy; got.Scheme != core.ProxySOCKS5 || !got.HasPassword {
		t.Fatalf("saving another setting changed the proxy to %+v", got)
	}
	h.a.sf.loaded = false
	h.tt.Frame()
	if f := h.a.sf.proxy; f.scheme != "socks5" || f.host != "127.0.0.1" || f.port != "7890" || f.user != "robin" || f.pass != "" {
		t.Fatalf("the reloaded form shows %+v", f)
	}
	h.settle()
	if h.hasExactText("Apply") {
		t.Fatal("Apply shows for a form that matches the saved proxy")
	}

	// Back to the environment removes the proxy, its login and the password.
	h.pickProxyProtocol("Proxy server", "SOCKS5", "From environment")
	if !h.hasExactText("Apply") {
		t.Fatal("no Apply for going back to the environment")
	}
	h.mustNotHave("Host and port")
	h.mustClick("Apply")
	h.waitFor("proxy removed", func() bool { return h.env.Svc.Settings().Proxy == (core.ProxySettings{}) })
	if raw, _ := os.ReadFile(filepath.Join(h.env.Dir, "settings.json")); strings.Contains(string(raw), "proxy") {
		t.Fatalf("settings.json keeps the proxy: %s", raw)
	}
}

func TestProxyFormShowsAFailedSave(t *testing.T) {
	h := newHarness(t, "light")
	h.openToolView("settings")
	h.pickProxyProtocol("Proxy server", "From environment", "HTTP")
	f := &h.a.sf.proxy
	f.host, f.port = "proxy.lan", "3128"
	cfg := filepath.Join(h.env.Dir, "settings.json")
	if err := os.Remove(cfg); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cfg, 0o700); err != nil { // a folder where the file goes: the write fails
		t.Fatal(err)
	}
	h.mustClick("Apply")
	h.waitFor("failure", func() bool { return strings.Contains(strings.Join(h.tt.Texts(), "\n"), "Could not save") })
	if !f.tried || f.host != "proxy.lan" || h.env.Svc.Settings().Proxy != (core.ProxySettings{}) {
		t.Fatalf("after a failed save: %+v", *f)
	}
	if !h.hasExactText("Apply") {
		t.Fatal("Apply is gone after a failed save") // still there to try again
	}
}

func TestProxySectionInChinese(t *testing.T) {
	h := newHarness(t, "light")
	h.openToolView("settings")
	h.setLocale("zh-Hans")
	h.mustHave("网络", "代理服务器", "跟随环境变量")
	h.pickProxyProtocol("代理服务器", "跟随环境变量", "HTTPS")
	h.mustHave("HTTPS", "主机和端口", "用户名（可选）", "密码（可选）")
	if !h.hasExactText("应用") {
		t.Fatal("no 应用 button")
	}
	h.mustClick("应用")
	h.mustHave("请输入主机名或 IP 地址")
	h.a.sf.proxy.host, h.a.sf.proxy.port = "proxy.lan", "99999"
	h.mustClick("应用")
	h.mustHave("请输入 1 到 65535 的端口")
	h.a.sf.proxy.port, h.a.sf.proxy.pass = "443", "x"
	h.mustClick("应用")
	h.mustHave("填写密码时需要同时填写用户名")
}

var regionName = regexp.MustCompile(`^[a-z]{2}-[a-z]+-\d$`)

func TestProxySectionIsTranslated(t *testing.T) {
	h := newHarness(t, "light")
	h.openToolView("settings")
	h.a.sf.proxy = proxyForm{scheme: core.ProxySOCKS5, host: "!", port: "x", pass: "p", tried: true, err: "x"}
	h.tt.Frame()
	h.showSetting("Proxy server")
	open := func() { h.a.sf.proxy.tried = true }
	allowed := map[string]bool{"HTTP": true, "HTTPS": true, "SOCKS5": true, "!": true, "x": true}
	h.setLocale("en-XA")
	for _, s := range h.untranslated() {
		if !allowed[s] && !settingsI18NAllow[s] && !strings.Contains(s, demoAccountName) && !strings.HasPrefix(s, "acme-") && !regionName.MatchString(s) {
			t.Errorf("untranslated text on the Network section: %q", s)
		}
	}
	open()
	h.setLocale("en")
}

// ---- the vault reset and the proxy password ------------------------------------------------

func TestVaultResetListsTheProxyPassword(t *testing.T) {
	h := newHarness(t, "light")
	h.toVault()
	pw := "proxy-secret"
	if err := h.env.Svc.SaveProxy(core.ProxySettings{Scheme: core.ProxyHTTP, Host: "proxy.lan", Port: 3128, Username: "robin"}, &pw); err != nil {
		t.Fatal(err)
	}
	h.openToolView("settings")
	h.showSetting("Reset vault…")
	h.mustClick("Reset vault…")
	h.mustHave("Reset vault?", "Settings that lose their secret", "Proxy password")
	h.a.vu.reset.typed = "RESET"
	h.mustClick("Reset vault")
	h.waitFor("reset", func() bool { return !h.a.vu.reset.open && !h.a.vaultMode() })
	if h.env.Svc.Settings().Proxy.HasPassword || h.env.Svc.Settings().Proxy.Username != "robin" {
		t.Fatalf("after the reset: %+v", h.env.Svc.Settings().Proxy)
	}
	h.settle()
	h.showSetting("Proxy server")
	h.tt.Frame()
	// The reset lists it, and the form offers it again.
	if h.a.vu.lastReset == nil || !h.a.vu.lastReset.Proxy {
		t.Fatalf("lastReset = %+v", h.a.vu.lastReset)
	}
	h.setLocale("zh-Hans")
	h.mustHave("代理密码已丢失，请在“设置 › 网络”中重新输入。")
}

// ---- bucket aliases ------------------------------------------------------------------------

// hasExactText reports whether an element shows exactly s; HasText also matches
// inside longer texts, such as a toast that names the bucket.
func (h *harness) hasExactText(s string) bool {
	h.t.Helper()
	h.tt.Frame()
	return slices.Contains(h.tt.Texts(), s)
}

// openAliasFromMenu opens the Set alias dialog the way a user does.
func (h *harness) openAliasFromMenu(bucket string) {
	h.t.Helper()
	if err := h.tt.RightClick(bucket); err != nil {
		h.t.Fatal(err)
	}
	h.tt.Frame()
	if menu := h.menu(); !contains(menu, "Set alias…") {
		h.t.Fatalf("bucket menu %q lacks Set alias…", menu)
	}
	if err := h.chooseMenu("Set alias…"); err != nil {
		h.t.Fatal(err)
	}
	h.settle()
	if !h.a.alias.open || h.a.alias.bucket != bucket {
		h.t.Fatalf("the dialog did not open for %s: %+v", bucket, h.a.alias)
	}
}

func TestBucketAliasShowsInPlaceOfTheNameAndNothingElseChanges(t *testing.T) {
	h := newHarness(t, "light")
	h.openAliasFromMenu("acme-assets")
	h.mustHave("Bucket alias", "Bucket", "acme-assets", "Alias", "Save", "Cancel",
		"Shown instead of the bucket name in the sidebar, the path bar and the command palette. The bucket itself is not renamed.")
	h.snapBoth("bucket-alias-dialog")

	h.tt.Type("Storefront assets")
	h.settle()
	h.mustClick("Save")
	h.waitFor("dialog closed", func() bool { return !h.a.alias.open })
	h.waitToast("Saved the alias of acme-assets")
	if got := h.env.Svc.BucketAlias(h.a.accountID, "acme-assets"); got != "Storefront assets" {
		t.Fatalf("saved alias = %q", got)
	}

	// The sidebar and the path bar show the alias; the real name is in the tooltip.
	h.mustHave("Storefront assets")
	if got := h.a.bucketLabel("acme-assets"); got != "Storefront assets" {
		t.Fatalf("bucketLabel = %q", got)
	}
	if got := h.a.bucketTip("acme-assets"); got != "Storefront assets · acme-assets" {
		t.Fatalf("bucketTip = %q", got)
	}
	if got := h.a.bucketLabel("acme-logs"); got != "acme-logs" || h.a.bucketTip("acme-logs") != "acme-logs" {
		t.Fatalf("a bucket without an alias is labelled %q", got)
	}
	if h.hasExactText("acme-assets") {
		t.Error("the real name is still shown as a label")
	}
	h.snapBoth("bucket-alias-shown")

	// Both names find the bucket in the sidebar filter.
	for _, q := range []string{"storefront", "acme-assets"} {
		h.a.bucketFilter = q
		h.tt.Frame()
		h.mustHave("Storefront assets")
		h.mustNotHave("acme-logs")
	}
	h.a.bucketFilter = ""

	// It is still the same bucket: browsing, the object URI and the real name in
	// the dialogs that act on it.
	h.settle()
	if h.a.scope.Bucket != "acme-assets" || !h.has("manifest.json") {
		t.Fatalf("browsing changed: bucket %q", h.a.scope.Bucket)
	}
	if got := h.a.objectURI("manifest.json"); got != "s3://acme-assets/manifest.json" {
		t.Fatalf("object URI = %q", got)
	}
	h.a.openDeleteBucket("acme-assets")
	h.settle()
	h.mustHave("Delete bucket?", "acme-assets")
	h.a.mg.db.open = false
	h.settle()

	// Another bucket keeps its own name in the path bar, then this one shows the alias again.
	h.a.openBucket("acme-logs")
	h.settle()
	h.mustHave("acme-logs")
	h.a.openBucket("acme-assets")
	h.settle()
	h.mustHave("Storefront assets")

	// The command palette finds it by the alias and by the real name.
	h.openPaletteWith(ui.Cmd, ui.KeyK)
	for _, q := range []string{"Storefront", "acme-assets"} {
		h.tt.Key(ui.Cmd, ui.KeyA)
		h.tt.Type(q)
		h.settle()
		h.mustHave(locales.PaletteOpenBucket("Storefront assets"))
	}
	h.a.pal.open = false
	h.settle()

	// It survives a restart, and is Chinese-ready.
	if got := h.env.Svc.Accounts()[0].BucketAliases["acme-assets"]; got != "Storefront assets" {
		t.Fatalf("Accounts() lost the alias: %v", h.env.Svc.Accounts()[0].BucketAliases)
	}
	h.zhDialog(func() { h.a.openAliasDialog("acme-assets") }, "存储桶别名", "存储桶", "别名", "保存", "取消",
		"在侧边栏、路径栏和命令面板里显示，代替存储桶名称。存储桶本身不会被重命名。")
	h.a.alias.open = false
	h.settle()
}

func TestBucketAliasCanBeChangedAndRemoved(t *testing.T) {
	h := newHarness(t, "light")
	acct := h.a.accountID

	h.a.openAliasDialog("acme-assets")
	h.a.alias.text = "  Shop  "
	h.settle()
	h.a.saveAlias()
	h.waitFor("saved", func() bool { return !h.a.alias.open })
	if got := h.env.Svc.BucketAlias(acct, "acme-assets"); got != "Shop" {
		t.Fatalf("alias = %q, want it trimmed", got)
	}

	// The dialog starts from the saved alias; another name replaces it.
	h.a.openAliasDialog("acme-assets")
	if h.a.alias.text != "Shop" {
		t.Fatalf("the dialog starts with %q", h.a.alias.text)
	}
	h.a.alias.text = "Shop (live)"
	h.a.saveAlias()
	h.waitFor("renamed", func() bool { return !h.a.alias.open })
	h.settle()
	h.mustHave("Shop (live)")
	if h.hasExactText("Shop") {
		t.Error("the old alias is still shown")
	}

	// Emptying it removes the alias.
	h.a.openAliasDialog("acme-assets")
	h.a.alias.text = ""
	h.settle()
	h.mustClick("Save")
	h.waitFor("removed", func() bool { return !h.a.alias.open })
	h.waitToast("Removed the alias of acme-assets")
	if got := h.env.Svc.BucketAlias(acct, "acme-assets"); got != "" {
		t.Fatalf("alias after removing = %q", got)
	}
	if !h.hasExactText("acme-assets") || h.hasExactText("Shop (live)") {
		t.Error("the bucket does not show its own name again")
	}

	// Cancel changes nothing.
	h.a.openAliasDialog("acme-assets")
	h.a.alias.text = "Never saved"
	h.settle()
	h.mustClick("Cancel")
	if h.env.Svc.BucketAlias(acct, "acme-assets") != "" || h.a.alias.open {
		t.Fatal("Cancel saved or kept the dialog")
	}
}

func TestBucketAliasErrorsStayInTheDialog(t *testing.T) {
	h := newHarness(t, "light")
	if err := h.env.Svc.SetBucketAlias(h.a.accountID, "acme-assets", "Shop"); err != nil {
		t.Fatal(err)
	}
	h.a.refreshAccounts()
	h.settle()

	// Another bucket cannot take the same alias; the Save button is off.
	h.a.openAliasDialog("acme-logs")
	h.a.alias.text = "Shop"
	h.settle()
	h.mustHave("Already used by acme-assets")
	h.mustClick("Save")
	if !h.a.alias.open || h.env.Svc.BucketAlias(h.a.accountID, "acme-logs") != "" {
		t.Fatal("a duplicate alias was saved")
	}
	// A name this bucket already has is fine.
	h.a.alias.text = "Shop 2"
	h.settle()
	h.mustNotHave("Already used by acme-assets")

	h.a.alias.text = strings.Repeat("长", core.MaxAliasRunes+1)
	h.settle()
	h.mustHave("Use at most 64 characters")
	h.mustClick("Save")
	if !h.a.alias.open {
		t.Fatal("a too long alias closed the dialog")
	}
	h.a.alias.text = strings.Repeat("长", core.MaxAliasRunes)
	h.settle()
	h.mustNotHave("Use at most 64 characters")

	// A failure of the service is reported in the dialog, which stays open.
	h.a.alias.text = "Logs"
	h.a.accountID = "gone"
	h.a.saveAlias()
	h.waitFor("failure shown", func() bool { return h.a.alias.err != "" })
	h.mustHave(h.a.alias.err)
	if !strings.HasPrefix(h.a.alias.err, "Could not save the alias: ") || !h.a.alias.open {
		t.Fatalf("failure: %q, open %v", h.a.alias.err, h.a.alias.open)
	}
}

// ---- translation -----------------------------------------------------------------------------

// newPlainTexts lists the texts that a screen opened by open shows in the
// pseudo locale without being bracketed (that is, not taken from the catalog)
// and that the screen before it did not show.
func (h *harness) newPlainTexts(open func()) []string {
	h.t.Helper()
	h.setLocale("en-XA")
	before := map[string]bool{}
	for _, s := range h.untranslated() {
		before[s] = true
	}
	open()
	h.settle()
	var out []string
	for _, s := range h.untranslated() {
		if !before[s] {
			out = append(out, s)
		}
	}
	h.setLocale("en")
	return out
}

func TestAliasDialogAndStatusBarAreTranslated(t *testing.T) {
	h := newHarness(t, "light")
	if err := h.env.Svc.SetBucketAlias(h.a.accountID, "acme-logs", "Taken"); err != nil {
		t.Fatal(err)
	}
	h.a.refreshAccounts()
	h.settle()

	open := func() {
		h.a.openAliasDialog("acme-assets")
		h.a.alias.text = "Taken" // shows the "already used" line
	}
	if left := h.newPlainTexts(open); len(left) > 0 {
		t.Errorf("the alias dialog has texts that are not translated: %q", left)
	}
	h.a.alias.open = false
	h.settle()
	open = func() {
		h.a.openAliasDialog("acme-assets")
		h.a.alias.text = strings.Repeat("x", core.MaxAliasRunes+1)
	}
	if left := h.newPlainTexts(open); len(left) > 0 {
		t.Errorf("the alias dialog (too long) has texts that are not translated: %q", left)
	}
	h.a.alias.open = false
	h.settle()

	for _, what := range []string{tookBuckets, tookList, tookUpload, tookDownload, tookDelete} {
		w := what
		if left := h.newPlainTexts(func() { h.a.took = opTime{what: w, d: 4200 * time.Millisecond} }); len(left) > 0 {
			t.Errorf("the status bar after %s has texts that are not translated: %q", w, left)
		}
	}
	// Each kind says its own thing in Chinese.
	for what, want := range map[string]string{tookBuckets: "加载存储桶用时 4.2 s", tookList: "加载列表用时 4.2 s",
		tookUpload: "上传用时 4.2 s", tookDownload: "下载用时 4.2 s", tookDelete: "删除用时 4.2 s"} {
		h.a.took = opTime{what: what, d: 4200 * time.Millisecond}
		h.setLocale("zh-Hans")
		h.mustHave(want)
		h.setLocale("en")
	}
}
