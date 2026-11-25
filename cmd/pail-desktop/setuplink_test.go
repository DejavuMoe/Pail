package main

import (
	"strings"
	"testing"

	core "github.com/DejavuMoe/Pail/internal/app"
)

// A link without keys fills in the form, which then refuses to save until the
// keys are entered; with them it saves what the link carried.
func TestSetupLinkFillsTheFormAndKeepsValidation(t *testing.T) {
	t.Parallel()
	h := newHarness(t, "light")
	link := core.SetupLink{
		Name: "Quant", Provider: core.ProviderS3, Endpoint: h.env.Srv.URL, Region: "ap-northeast-1",
		PathStyle: true, Credentials: core.CredStatic, Buckets: []string{"acme-analytics/daily/"},
		ChecksumMode: "when_required",
	}
	h.a.openSetupLink(link.URL())
	h.settle()
	f := &h.a.af
	if !f.open || f.editing != "" || !f.fromLink || f.linkKeys {
		t.Fatalf("form not opened from the link: %+v", f)
	}
	if f.name != "Quant" || f.provider != 1 || f.endpoint != h.env.Srv.URL || f.region != "ap-northeast-1" ||
		f.buckets != "acme-analytics/daily/" || f.checksum != "when_required" || !f.advanced {
		t.Fatalf("fields: %+v", f)
	}
	host := strings.TrimPrefix(h.env.Srv.URL, "http://")
	h.mustHave("Filled in from a setup link", host, "Enter the access key ID", "Enter the secret access key")
	h.snap("account-from-link-light")
	h.mustClick("Save")
	if len(h.env.Svc.Accounts()) != 1 || !f.open {
		t.Fatal("a link without keys was saved")
	}

	f.access, f.secret = "demo", "demo-secret"
	h.mustClick("Save")
	h.waitFor("account saved", func() bool { return len(h.env.Svc.Accounts()) == 2 && !h.a.af.open })
	var got core.Account
	for _, ac := range h.env.Svc.Accounts() {
		if ac.Name == "Quant" {
			got = ac
		}
	}
	if got.Endpoint != h.env.Srv.URL || got.Region != "ap-northeast-1" || got.ChecksumMode != "when_required" ||
		len(got.Buckets) != 1 || got.Buckets[0].Prefix != "daily/" {
		t.Fatalf("saved %+v", got)
	}
}

func TestSetupLinkWithKeysSaysSo(t *testing.T) {
	t.Parallel()
	h := newHarness(t, "light")
	h.a.openSetupLink(core.SetupLink{Name: "Q", Provider: core.ProviderS3, Endpoint: h.env.Srv.URL, PathStyle: true,
		AccessKeyID: "demo", SecretAccessKey: "demo-secret"}.URL())
	h.settle()
	if f := &h.a.af; f.access != "demo" || f.secret != "demo-secret" || !f.linkKeys {
		t.Fatalf("keys not filled in: %+v", f)
	}
	h.mustHave("keys included")
	h.mustNotHave("Enter the secret access key")
}

func TestOpeningSomethingElseOnlySaysSo(t *testing.T) {
	t.Parallel()
	h := newHarness(t, "light")
	h.a.openSetupLink("pail://bucket?name=x")
	h.waitToast("This is not a Pail setup link.")
	if h.a.af.open {
		t.Fatal("the form opened for a link that is not a setup link")
	}
}

func TestPasteSetupLink(t *testing.T) {
	t.Parallel()
	h := newHarness(t, "light")
	h.mustClick(demoAccountName)
	if err := h.chooseMenu("Add account from link…"); err != nil {
		t.Fatal(err)
	}
	h.settle()
	h.mustHave("Add account from link", "Setup link")
	h.tt.Type("https://pail.download/")
	h.mustClick("Continue")
	h.mustHave("This is not a Pail setup link.")
	h.snap("paste-link-error-light")
	h.a.links.paste.text = core.SetupLink{Name: "Pasted", Credentials: core.CredAnonymous}.URL()
	h.mustClick("Continue")
	if h.a.links.paste.open || !h.a.af.open || h.a.af.name != "Pasted" || h.a.af.creds != core.CredAnonymous {
		t.Fatalf("paste did not open the form: %+v", h.a.af)
	}
}

func TestI18NSetupLink(t *testing.T) {
	h := newHarness(t, "light")
	h.setLocale("en-XA")
	plain := []plainOK{plainIn(h.plainSet()), plainPrefix("pail://account?"), plainSub("127.0.0.1:"), plainIn(map[string]bool{demoAccountName: true, "Quant": true, "AWS": true})}
	h.a.openShareLink()
	h.settle()
	h.expectTranslated("share setup link", plain...)
	h.a.links.share.access, h.a.links.share.secret = "AKIAFORQUANT", "s"
	h.settle()
	h.expectTranslated("share setup link with keys", append(plain, plainIn(map[string]bool{"AKIAFORQUANT": true}))...)
	h.a.links.share.open = false
	h.a.openPasteLink()
	h.settle()
	h.expectTranslated("paste setup link", plain...)
	h.a.links.paste.open = false
	h.a.openSetupLink(core.SetupLink{Name: "Quant", Provider: core.ProviderS3, Endpoint: h.env.Srv.URL}.URL())
	h.settle()
	h.expectTranslated("account form from a link", append(plain, plainIn(map[string]bool{h.env.Srv.URL: true}))...)

	h.setLocale("zh-Hans")
	h.settle()
	h.mustHave("已根据配置链接填写")
}

// The share dialog never puts the account's own keys in the link; keys typed
// for the recipient go in, with a warning.
func TestShareSetupLink(t *testing.T) {
	t.Parallel()
	h := newHarness(t, "light")
	h.mustClick(demoAccountName)
	if err := h.chooseMenu("Share setup link…"); err != nil {
		t.Fatal(err)
	}
	h.settle()
	h.mustHave("Share setup link", "Recipient's access key ID (optional)", "Recipient's secret access key (optional)", "Leave both empty", "Copy link")
	h.mustNotHave("Anyone who has this link can use these keys.")
	l, err := core.ParseSetupLink(h.a.shareLink().URL())
	if err != nil {
		t.Fatal(err)
	}
	if l.Name != demoAccountName || l.HasKeys() || l.Endpoint != h.env.Account.Endpoint {
		t.Fatalf("link %+v", l)
	}
	h.snap("share-link-light")

	h.a.links.share.access, h.a.links.share.secret = "AKIAFORQUANT", "quant-secret"
	h.settle()
	h.mustHave("Anyone who has this link can use these keys.")
	h.mustNotHave("quant-secret", "Leave both empty") // the secret is copied, not shown; the warning replaces the hint
	h.snap("share-link-keys-light")
	l, _ = core.ParseSetupLink(h.a.shareLink().URL())
	if l.AccessKeyID != "AKIAFORQUANT" || l.SecretAccessKey != "quant-secret" {
		t.Fatalf("keys for the recipient: %+v", l)
	}
	h.mustClick("Copy link")
	h.waitToast("Setup link copied")
}
