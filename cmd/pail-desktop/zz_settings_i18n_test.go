package main

import (
	"strings"
	"testing"
	"time"
)

// Plain texts that legitimately stay as they are under the en-XA pseudo
// locale: product names, code values, example placeholders and user data.
var settingsI18NAllow = map[string]bool{
	"AWS":                              true,
	"us-east-1":                        true,
	"AKIA…":                            true,
	"default":                          true,
	"Pail":                             true,
	"https://s3.example.com":           true,
	"https://cdn.example.com":          true,
	"arn:aws:iam::123456789012:role/…": true,
	"(Argon2id, XChaCha20-Poly1305)":   true,
	"RESET":                            true,
	"Windows Credential Manager":       true,
	"macOS Keychain":                   true,
	"Secret Service":                   true,
	"Demo account":                     true,
	"acme-assets":                      true,
	"One":                              true, // account name (user data)
	"ref1":                             true, // secret reference name (user data)
	"Pail demo":                        true,
	"dev":                              true, // the build version
	"Dejavu Moe":                       true, // the author, a name
	"github.com/DejavuMoe/Pail":        true, // the repository address
}

// clickSettingSwitch clicks the switch of a Settings row. The label column is
// 200px wide and 16px of gap follow it; a "?" sits after the label text, so
// the switch is found from the left edge of the label, not from its text end.
func (h *harness) clickSettingSwitch(label string) {
	h.t.Helper()
	h.tt.Frame()
	r, ok := h.tt.Find(label)
	if !ok {
		h.t.Fatalf("no element shows %q; have %q", label, h.tt.Texts())
	}
	h.tt.ClickAt(r.X+200+16+18, r.Y+r.H/2)
	h.settle()
}

// settingsUntranslated is the list of visible plain English texts of the
// current screen, minus the allowed ones.
func (h *harness) settingsUntranslated(background map[string]bool) []string {
	h.t.Helper()
	var out []string
	for _, s := range h.untranslated() {
		if settingsI18NAllow[s] || background[s] || strings.Contains(s, demoAccountName) {
			continue
		}
		out = append(out, s)
	}
	return out
}

func TestI18NSettings(t *testing.T) {
	h := newHarness(t, "light")

	// The window around the screens of this file (sidebar, tool header, status
	// bar, the object browser) belongs to other files: what stays plain there
	// is not judged here. It is measured on the browser and on another tool.
	background := map[string]bool{}
	h.setLocale("en-XA")
	for _, s := range h.untranslated() {
		background[s] = true
	}
	h.setLocale("en")
	h.openToolView("mime")
	h.setLocale("en-XA")
	for _, s := range h.untranslated() {
		background[s] = true
	}
	h.setLocale("en")
	h.a.closeTool()
	h.settle()

	// pseudo checks that no plain English is left on the screen opened by open.
	pseudo := func(name string, open func(), closeIt func()) {
		t.Helper()
		h.setLocale("en")
		open()
		h.settle()
		h.setLocale("en-XA")
		if left := h.settingsUntranslated(background); len(left) > 0 {
			t.Errorf("%s: texts that are not translated: %q", name, left)
		}
		closeIt()
		h.setLocale("en")
	}

	// ---- account dialog ----------------------------------------------------------------
	pseudo("account dialog (AWS, advanced)", func() {
		h.a.openAccountForm(false)
		h.a.af.advanced = true
		h.a.af.tried = true // show every error line
	}, func() { h.a.af.open = false })
	pseudo("account dialog (S3-compatible, advanced)", func() {
		h.a.openAccountForm(false)
		h.a.af.provider = 1
		h.a.af.advanced = true
		h.a.af.tried = true
		h.a.af.cdnURL = "ftp://cdn"
	}, func() { h.a.af.open = false })
	pseudo("account dialog (profile)", func() {
		h.a.openAccountForm(false)
		h.a.af.creds = "profile"
		h.a.af.tried = true
	}, func() { h.a.af.open = false })

	// ---- Settings view -----------------------------------------------------------------
	pseudo("settings (keychain)", func() { h.openToolView("settings") }, func() {})
	h.toVault()
	pseudo("settings (vault)", func() { h.openToolView("settings") }, func() {})
	h.a.sf.errs["upload"] = "x"
	h.lockNow()
	pseudo("settings (vault locked)", func() { h.openToolView("settings") }, func() {})
	h.a.askUnlock("t", nil)
	h.settle()
	pseudo("unlock dialog", func() { h.a.vu.unlock.err = "x"; h.a.vu.unlock.open = true }, func() { h.a.closeUnlock() })

	// ---- vault dialogs -----------------------------------------------------------------
	pseudo("create vault dialog", func() {
		h.a.vu.create = pwDialog{open: true, tried: true, pw: "short", pw2: "other"}
	}, func() { h.a.vu.create = pwDialog{} })
	pseudo("existing vault dialog", func() {
		h.a.vu.create = pwDialog{open: true, existing: true, tried: true}
	}, func() { h.a.vu.create = pwDialog{} })
	pseudo("keychain dialog", func() {
		h.a.vu.toKeychain = pwDialog{open: true, needPW: true}
	}, func() { h.a.vu.toKeychain = pwDialog{} })
	pseudo("change password dialog", func() {
		h.a.vu.change = pwDialog{open: true, tried: true}
	}, func() { h.a.vu.change = pwDialog{} })
	pseudo("reset dialog", func() {
		h.a.vu.reset = resetState{open: true, accounts: []string{"One"}, refs: []string{"ref1"}}
	}, func() { h.a.vu.reset = resetState{} })
	pseudo("secrets dialog", func() { h.a.openSecrets() }, func() { h.a.tl.secrets.open = false })

	// ---- Chinese -----------------------------------------------------------------------
	h.setLocale("zh-Hans")
	h.a.openAccountForm(false)
	h.settle()
	h.mustHave("添加账户", "服务商", "测试连接", "保存", "取消", "凭证", "高级")
	h.a.af.provider = 1
	h.a.af.advanced = true
	h.settle()
	h.mustHave("端点", "路径样式访问", "角色 ARN", "外部 ID", "会话名称", "校验和模式", "跳过 TLS 验证")
	h.a.af.open = false

	h.openToolView("settings")
	h.mustHave("外观", "主题", "语言", "传输", "并发上传数", "文件已存在时", "默认下载文件夹", "写入诊断日志", "安全", "凭证存储", "自动锁定", "15 分钟")
	h.a.vu.create = pwDialog{open: true, tried: true}
	h.settle()
	h.mustHave("创建主密码", "确认密码", "创建加密保险库", "至少使用 8 个字符", "再次输入密码")
	h.a.vu.create = pwDialog{}
	h.a.askUnlock("t", nil)
	h.settle()
	h.mustHave("解锁加密保险库", "主密码", "解锁")
	h.a.closeUnlock()
	h.a.vu.reset = resetState{open: true}
	h.settle()
	h.mustHave("重置加密保险库？", "输入 RESET 以确认", "此操作无法撤销。云端的存储桶和对象不受影响。")
	h.a.vu.reset = resetState{}
	h.setLocale("en")

	// ---- help icons --------------------------------------------------------------------
	hover := func(help string) {
		t.Helper()
		h.tt.Frame()
		r, ok := h.tt.Find(help)
		if !ok {
			t.Fatalf("no help icon with the text %q", help)
		}
		h.tt.Move(r.X+r.W/2, r.Y+r.H/2)
		h.tt.Advance(700 * time.Millisecond)
		h.tt.Frame()
		h.mustHave(help)
	}
	h.a.openAccountForm(false)
	h.a.af.provider = 1
	h.a.af.advanced = true
	h.settle()
	hover("Addresses objects as endpoint/bucket/object. MinIO and most self-hosted gateways need this on; keep it off for AWS.")
	hover("Optional. The address your CDN serves objects from. A base URL such as https://cdn.example.com gives links like base/bucket/key. For a custom link, write a template with {bucket} and {key}; a template that uses {bucket} needs {key} as well.")
	hover("Turns off certificate checks for this connection, so a fake server could read your data and credentials. Use it only for test servers you trust, such as a local MinIO with a self-signed certificate.")
	h.a.af.open = false
	h.openToolView("settings")
	hover("Follow system uses your operating system's language and falls back to English. The choice is remembered.")
	hover("How many upload tasks run at the same time; the rest wait in the queue. Lowering it never interrupts a running task. Allowed: 1 to 64.")
	h.mustHave("Language", "Follow system")

	// ---- the Language setting switches the visible texts at once -----------------------
	h.a.setLanguage("zh-Hans")
	h.settle()
	h.mustHave("外观", "语言", "简体中文")
	h.mustNotHave("APPEARANCE")
	if h.a.lang != "zh-Hans" {
		t.Fatalf("lang = %q", h.a.lang)
	}
	h.a.setLanguage("")
	h.settle()
	h.mustHave("APPEARANCE", "Language", "Follow system")
	h.mustNotHave("外观")
}
