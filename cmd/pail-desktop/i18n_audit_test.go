package main

import (
	"fmt"
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
	if loc == "en" {
		return []string{en}
	}
	catalogOnce.Do(func() { catalogEn = readCatalog(t, "en") })
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
				h.expectOnScreen(loc, sz, c.footer...)
			})
		}
	}
}

// ---- snapshots ---------------------------------------------------------------

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
		{"transfers-group", func(h *harness) {
			// A folder upload with files running (one compressing), one paused and a
			// backlog, a folder still being scanned, a download with a phase, and
			// a standalone delete.
			ts, gs := groupFixture(time.Now())
			base := time.Now()
			ts = append(ts,
				Task{ID: "scan2", GroupID: "g2", Kind: core.KindOther, Title: "Scan 3 items for upload", Status: core.StatusRunning, Pausable: true, Created: base},
				Task{ID: "d1", GroupID: "g3", Kind: core.KindDownload, Title: "Download backup.tar.gz", Detail: "C:/Users/me/Downloads", Status: core.StatusRunning, BytesTotal: 800 << 20, BytesDone: 800 << 20, Phase: core.PhaseFinishing, Pausable: true, Created: base},
				Task{ID: "x1", Kind: core.KindOther, Title: "Delete 12 items", Status: core.StatusQueued, Created: base},
			)
			gs = append(gs, core.GroupProgress{ID: "g2", Kind: core.KindDownload, Title: "Download 3 items", Detail: "s3://acme-assets/ → C:/Users/me/Downloads", Roots: 3, Created: base, Files: 1234, FilesSucceeded: 1000, BytesTotal: 3 << 30, BytesDone: 2 << 30, Scanning: true, Pausable: true},
				core.GroupProgress{ID: "g3", Kind: core.KindDownload, Created: base, Files: 1, FilesRunning: 1, BytesTotal: 800 << 20, BytesDone: 800 << 20})
			h.setView(ts, gs)
			h.a.tl.tr = trAuto{init: true, seen: len(h.a.tasks), prevOpen: true}
			h.a.transfersOpen = true
			h.a.transTab = 0
		}},
		{"transfers-scan", func(h *harness) {
			// A download whose scan goes on (the total grows), with one file running and
			// a backlog, then a paused batch.
			base := time.Now()
			h.setView([]Task{
				{ID: "d1", GroupID: "g2", Kind: core.KindDownload, Title: "Download a.bin", Status: core.StatusRunning, BytesTotal: 3 << 30, BytesDone: 1 << 30, Speed: 30 << 20, Pausable: true, Created: base},
				{ID: "p1", GroupID: "g4", Kind: core.KindUpload, Title: "Upload b.bin", Status: core.StatusPaused, Pausable: true, Created: base.Add(time.Second)},
			}, []core.GroupProgress{
				{ID: "g2", Kind: core.KindDownload, Title: "Download 3 items", Detail: "s3://acme-assets/ → C:/Users/me/Downloads", Roots: 3, Created: base, Files: 1234, FilesSucceeded: 1000, FilesRunning: 1, FilesQueued: 233, BytesTotal: 3 << 30, BytesDone: 2 << 30, Speed: 30 << 20, Scanning: true, Pausable: true},
				{ID: "g4", Kind: core.KindUpload, Title: "Upload 2 items", Detail: "→ s3://acme-assets/", Roots: 2, Created: base.Add(time.Second), Files: 40, FilesPaused: 40, BytesTotal: 1 << 30, Paused: true, Pausable: false},
			})
			h.a.tl.tr = trAuto{init: true, seen: len(h.a.tasks), prevOpen: true}
			h.a.transfersOpen = true
			h.a.transTab = 0
		}},
		{"transfers-failed", func(h *harness) {
			now := time.Now()
			h.setView([]Task{
				{ID: "c1", Kind: core.KindUpload, Title: "Upload photos/img001.jpg", Status: core.StatusCanceled, Created: now, Finished: now},
				{ID: "c2", Kind: core.KindUpload, Title: "Upload photos/img002.jpg", Status: core.StatusFailed, Err: "Access denied (AccessDenied)", Created: now, Finished: now},
			}, nil)
			h.a.tl.tr = trAuto{init: true, seen: len(h.a.tasks), prevOpen: true}
			h.a.transfersOpen = true
			h.a.transTab = 2
		}},
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
					h.writePNGThemes(filepath.Join(base, loc, sz.name), s.name)
				})
			}
		}
	}
}
