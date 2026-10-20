package main

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/egoist/mygo/ui"
)

// Layout under the other languages: every dialog of the UX audit is opened in
// the smallest window under zh-Hans and under the en-XA pseudo locale (about
// 40% longer than English), and each footer button must still be fully on
// screen. The cases set up in English (they click English texts), then the
// language switches; the footers are looked up by the localized texts of the
// same messages.

var (
	catalogOnce sync.Once
	catalogEn   map[string]string
)

// footerCandidates are the texts a button labelled en in English may have in
// locale loc, found through the catalog: messages without arguments, messages
// counted by n ("1,234 items"), and messages with text arguments ("Delete 2
// items"), whose arguments are localized in turn. The English text itself comes
// last: a case may set a button's text itself.
func footerCandidates(t *testing.T, loc, en string) []string {
	t.Helper()
	catalogOnce.Do(func() { catalogEn = readCatalog(t, "en") })
	if loc == "en" {
		return []string{en}
	}
	return append(localizeText(loc, en, 0), en)
}

func localizeText(loc, en string, depth int) []string {
	var out []string
	digits := regexp.MustCompile(`\d[\d,]*`).FindString(en)
	n, _ := strconv.Atoi(strings.ReplaceAll(digits, ",", ""))
	for key, msg := range catalogEn {
		switch {
		case msg == en:
			out = append(out, locales.Catalog.Format(loc, key, nil))
		case strings.Contains(msg, "plural,") || strings.Contains(msg, "select,"):
			if digits != "" && strings.Count(msg, "{") > 0 && !strings.Contains(msg, "} {") && locales.Catalog.Format("en", key, map[string]any{"n": n}) == en {
				out = append(out, locales.Catalog.Format(loc, key, map[string]any{"n": n}))
			}
		case strings.Contains(msg, "{") && depth < 2:
			parts := placeholderRe.Split(msg, -1)
			names := placeholderRe.FindAllStringSubmatch(msg, -1)
			if len(names) == 0 || len(names) > 3 {
				continue
			}
			pat := "^"
			for i, p := range parts {
				pat += regexp.QuoteMeta(p)
				if i < len(names) {
					pat += "(.+?)"
				}
			}
			m := regexp.MustCompile(pat + "$").FindStringSubmatch(en)
			if m == nil {
				continue
			}
			args := map[string]any{}
			for i, nm := range names {
				arg := m[i+1]
				if c := localizeText(loc, arg, depth+1); len(c) > 0 {
					arg = c[0]
				}
				args[nm[1]] = arg
			}
			out = append(out, locales.Catalog.Format(loc, key, args))
		}
	}
	return out
}

var placeholderRe = regexp.MustCompile(`\{(\w+)\}`)

// expectOnScreenIn is expectOnScreen for the footers of an English case under
// locale loc.
func (h *harness) expectOnScreenIn(loc string, sz auditSize, enTexts ...string) {
	h.t.Helper()
	for _, en := range enTexts {
		cands := footerCandidates(h.t, loc, en)
		found := false
		for _, s := range cands {
			r, ok := h.tt.Find(s)
			if !ok {
				continue
			}
			found = true
			if r.X < 0 || r.Y < 0 || r.X+r.W > float32(sz.w) || r.Y+r.H > float32(sz.h) || r.W <= 0 || r.H <= 0 {
				h.t.Errorf("%s %dx%d: %q (%q) at %+v is outside the window", loc, sz.w, sz.h, s, en, r)
			}
			break
		}
		if !found {
			h.t.Errorf("%s %dx%d: none of %q is on screen for %q; have %q", loc, sz.w, sz.h, cands, en, h.tt.Texts())
		}
	}
}

func TestDialogFootersReachableAtMinSizeInOtherLanguages(t *testing.T) {
	sz := auditSizes[1]
	for _, loc := range []string{"zh-Hans", "en-XA"} {
		for _, c := range dialogCases() {
			t.Run(loc+"/"+c.name, func(t *testing.T) {
				h := newHarness(t, "light")
				h.resize(sz.w, sz.h)
				c.setup(h)
				h.setLocale(loc)
				h.tt.Frame()
				h.expectOnScreenIn(loc, sz, c.footer...)
			})
		}
	}
}

// ---- help icons --------------------------------------------------------------

// helpIcons lists the help texts of the "?" icons on screen: a help icon is a
// button of exactly helpHitSize square whose accessible label is its text.
func (h *harness) helpIcons() []string {
	h.t.Helper()
	h.tt.Frame()
	var out []string
	seen := map[string]bool{}
	for _, s := range h.tt.Texts() {
		if seen[s] || len(s) < 20 {
			continue
		}
		seen[s] = true
		if r, ok := h.tt.Find(s); ok && r.W == helpHitSize && r.H == helpHitSize {
			out = append(out, s)
		}
	}
	return out
}

// hover rests the pointer on the element with text and lets the tooltip delay
// pass, so its tip is drawn.
func (h *harness) hover(text string) {
	h.t.Helper()
	h.tt.Frame()
	r, ok := h.tt.Find(text)
	if !ok {
		h.t.Fatalf("no element with text %q; have %q", text, h.tt.Texts())
	}
	h.tt.Move(r.X+r.W/2, r.Y+r.H/2)
	h.tt.Frame()
	h.tt.Advance(800 * time.Millisecond)
	h.tt.Frame()
}

// tipShown reports whether text is drawn twice: once as the icon's accessible
// label and once in the open tooltip.
func (h *harness) tipShown(text string) bool {
	n := 0
	for _, s := range h.tt.Texts() {
		if s == text {
			n++
		}
	}
	return n >= 2
}

// ---- snapshots ---------------------------------------------------------------

// snapLocale writes name under base/<locale>/<size>/<theme>/ for both themes.
func (h *harness) snapLocale(base, loc string, sz auditSize, name string) {
	h.t.Helper()
	was := h.a.opts.theme
	defer func() { h.a.opts.theme = was; h.tt.Frame() }()
	for _, th := range []string{"light", "dark"} {
		h.a.opts.theme = th
		h.tt.Frame()
		h.tt.Frame()
		dir := filepath.Join(base, loc, sz.name, th)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			h.t.Fatal(err)
		}
		f, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			h.t.Fatal(err)
		}
		if err := png.Encode(f, h.tt.Image()); err != nil {
			f.Close()
			h.t.Fatal(err)
		}
		f.Close()
	}
}

// i18nScenarios are the views reviewed in each language. They work in any
// language: they click file names and call the app, not English labels.
func i18nScenarios(loc string) []struct {
	name  string
	setup func(h *harness)
} {
	query := map[string]string{"zh-Hans": "上传", "en": "upload", "en-XA": "upload"}[loc]
	type sc = struct {
		name  string
		setup func(h *harness)
	}
	return []sc{
		{"main", func(h *harness) { h.mustClick("manifest.json") }},
		{"account-help-tooltip", func(h *harness) {
			h.a.openAccountForm(false)
			h.a.af.provider = 1
			h.a.af.advanced = true
			h.settle()
			icons := h.helpIcons()
			if len(icons) == 0 {
				h.t.Fatal("no help icons in the account dialog")
			}
			h.hover(icons[min(2, len(icons)-1)])
		}},
		{"settings", func(h *harness) { h.a.openTool("settings"); h.settle() }},
		{"palette-query", func(h *harness) {
			h.openPaletteWith(ui.Cmd, ui.KeyK)
			h.tt.Type(query)
			h.settle()
		}},
		{"delete-confirm", func(h *harness) {
			h.mustClick("README.txt")
			h.tt.ClickWith(ui.Shift, "manifest.json")
			h.settle()
			h.a.askDelete()
			h.settle()
		}},
		{"storage-class", func(h *harness) {
			h.mustClick("manifest.json")
			h.a.openRewriteSelection(false)
			h.settle()
		}},
		{"sync-tool", func(h *harness) {
			dir := h.t.TempDir()
			_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("alpha"), 0o600)
			_ = os.WriteFile(filepath.Join(dir, "skip.tmp"), []byte("junk"), 0o600)
			if _, err := h.a.svc.SaveSyncJob(core.SyncJob{Name: "Site", LocalDir: dir, Direction: "upload", Mode: "mirror",
				Remote: Scope{AccountID: h.env.Account.ID, Bucket: "acme-website", Prefix: "sync/"}, Excludes: []string{"*.tmp"}}); err != nil {
				h.t.Fatal(err)
			}
			h.env.Srv.PutObject("acme-website", "sync/extra.txt", []byte("stale"), "text/plain", nil)
			h.a.openTool("sync")
			h.settle()
			s := &h.a.tl.sync
			h.a.syncPreview()
			h.waitFor("preview", func() bool { return s.pv != nil })
			h.settle()
		}},
		{"upload-rules", func(h *harness) {
			h.a.openTool("rules")
			h.settle()
			h.a.rulesAdd()
			rs := &h.a.tl.rules
			rs.form.name, rs.form.bucket, rs.form.key = "Images", "acme-*", "*.png"
			rs.form.headers = []ruleKV{{"Cache-Control", "max-age=60"}}
			rs.form.class, rs.form.enc = "STANDARD_IA", "aws:kms"
			h.tt.Frame()
			rs.testBucket, rs.testKey = "acme-assets", "hero.png"
			h.tt.Frame()
		}},
	}
}

func TestI18NSnapshots(t *testing.T) {
	base := os.Getenv("PAIL_UI_SNAPSHOT_DIR")
	if base == "" {
		t.Skip("set PAIL_UI_SNAPSHOT_DIR to write the i18n PNGs")
	}
	locs := []string{"zh-Hans", "en"}
	if v := os.Getenv("PAIL_I18N_LOCALES"); v != "" {
		locs = strings.Split(v, ",")
	}
	for _, loc := range locs {
		for _, sz := range auditSizes {
			for _, s := range i18nScenarios(loc) {
				t.Run(fmt.Sprintf("%s/%s/%s", loc, sz.name, s.name), func(t *testing.T) {
					h := newHarness(t, "light")
					h.resize(sz.w, sz.h)
					h.tt.SetTitleBar(ui.TitleBar{Height: topBarH, Right: 138})
					h.setLocale(loc)
					s.setup(h)
					h.tt.Frame()
					h.snapLocale(base, loc, sz, s.name)
				})
			}
		}
	}
}
