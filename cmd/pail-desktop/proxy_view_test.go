package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	core "github.com/DejavuMoe/Pail/internal/app"
)

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
	h.mustHave("Network", "Proxy server", "From environment")
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
	h.showSetting("Host and port")
	h.mustClick("Host name or IP address")
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
	h.editSetting("Part size", "24")
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

func TestProxySectionIsTranslated(t *testing.T) {
	h := newHarness(t, "light")
	h.openToolView("settings")
	h.a.sf.proxy = proxyForm{scheme: core.ProxySOCKS5, host: "!", port: "x", pass: "p", tried: true, err: "x"}
	h.tt.Frame()
	h.showSetting("Proxy server")
	open := func() { h.a.sf.proxy.tried = true }
	allowed := map[string]bool{"HTTP": true, "HTTPS": true, "SOCKS5": true, "!": true, "x": true}
	h.setLocale("en-XA")
	h.expectTranslated("the Network section", plainIn(allowed), plainIn(settingsI18NAllow), plainPrefix("acme-"))
	open()
	h.setLocale("en")
}

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
