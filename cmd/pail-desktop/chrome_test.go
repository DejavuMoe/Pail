package main

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// recordTheme makes the app report its theme changes to the test.
func (h *harness) recordTheme() *[]mygo.ThemeSource {
	var got []mygo.ThemeSource
	h.a.themeSet = func(s mygo.ThemeSource) { got = append(got, s) }
	return &got
}

func (h *harness) openPaletteWith(mods ui.Modifiers, key ui.Key) {
	h.t.Helper()
	h.tt.Key(mods, key)
	h.settle()
	if !h.a.pal.open {
		h.t.Fatalf("the palette did not open; texts %q", h.tt.Texts())
	}
}

// ---- window options and menu -----------------------------------------------------------------

func TestWindowOptionsHideTheTitleBarOnEveryPlatform(t *testing.T) {
	o := windowOptions("Pail", 1440, 900, nil)
	if o.TitleBarStyle != mygo.TitleBarHidden && o.TitleBarStyle != mygo.TitleBarHiddenInset {
		t.Errorf("title bar style = %q", o.TitleBarStyle)
	}
	if o.TitleBarHeight != 44 {
		t.Errorf("title bar height = %d, want the 44px of the toolbar", o.TitleBarHeight)
	}
	if o.BackgroundColor != "light-dark(#FBFAF8, #1A1A18)" {
		t.Errorf("background = %q", o.BackgroundColor)
	}
	if o.MinWidth != 960 || o.MinHeight != 600 {
		t.Errorf("minimum size = %dx%d", o.MinWidth, o.MinHeight)
	}
}

func TestNativeMenuBarOnlyOnMacOS(t *testing.T) {
	h := newHarness(t, "light")
	for _, goos := range []string{"windows", "linux", "freebsd"} {
		set := 0
		if installMenu(h.a, goos, func(*mygo.Menu) { set++ }, func() {}) || set != 0 || h.a.menu != nil {
			t.Errorf("%s: a native menu bar was installed", goos)
		}
	}
	var got *mygo.Menu
	if !installMenu(h.a, "darwin", func(m *mygo.Menu) { got = m }, func() {}) || got == nil || h.a.menu != got {
		t.Fatal("darwin: no menu bar")
	}
	if got.ItemByID("palette") == nil || got.ItemByID("upload.files") == nil {
		t.Error("the macOS menu lost its commands")
	}
}

// ---- theme sync --------------------------------------------------------------------------------

func TestThemeIsSyncedWithTheSystem(t *testing.T) {
	h := newHarness(t, "dark") // --theme dark
	got := h.recordTheme()
	h.a.themeSynced = "" // main syncs before the window exists; the first frame may too
	h.tt.Frame()
	h.tt.Frame()
	if len(*got) != 1 || (*got)[0] != mygo.ThemeDark {
		t.Fatalf("startup sync = %v, want one call with dark", *got)
	}

	h.a.setTheme("light")
	h.settle()
	h.a.setTheme("system")
	h.settle()
	h.mustClick("Settings")
	h.mustClick("Dark")
	want := []mygo.ThemeSource{mygo.ThemeDark, mygo.ThemeLight, mygo.ThemeSystem, mygo.ThemeDark}
	if len(*got) != len(want) {
		t.Fatalf("theme calls = %v, want %v", *got, want)
	}
	for i := range want {
		if (*got)[i] != want[i] {
			t.Fatalf("theme calls = %v, want %v", *got, want)
		}
	}
	h.waitFor("saved", func() bool { return h.env.Svc.Settings().Theme == "dark" })
	h.tt.Frame()
	if len(*got) != len(want) {
		t.Errorf("an unchanged theme was sent again: %v", *got)
	}
}

// ---- the ⋯ menu ----------------------------------------------------------------------------------

func TestMoreMenuHasTheAppCommands(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("More")
	h.tt.Frame()
	menu := h.tt.Menu()
	for _, want := range []string{"Command palette", "Settings", "Theme", "Keyboard shortcuts", "Documentation", "About Pail", "Quit"} {
		if !contains(menu, want) {
			t.Errorf("more menu %q is missing %q", menu, want)
		}
	}
	h.snap("more-menu-light")

	if err := h.tt.ChooseMenuItem("Theme", "Dark"); err != nil {
		t.Fatal(err)
	}
	h.settle()
	if h.a.opts.theme != "dark" {
		t.Fatalf("theme = %q after choosing Dark", h.a.opts.theme)
	}
	// Settings, palette, shortcuts, about and quit from the menu.
	h.mustClick("More")
	h.tt.Frame()
	if err := h.tt.ChooseMenuItem("Settings"); err != nil || h.a.tool != "settings" {
		t.Fatalf("Settings: %v, tool %q", err, h.a.tool)
	}
	h.mustClick("More")
	h.tt.Frame()
	if err := h.tt.ChooseMenuItem("Command palette"); err != nil {
		t.Fatal(err)
	}
	h.settle()
	if !h.a.pal.open {
		t.Fatal("Command palette did not open the palette")
	}
	h.tt.Key(0, ui.KeyEscape)
	h.settle()
	for item, open := range map[string]func() bool{
		"Keyboard shortcuts": func() bool { return h.a.keys.open },
		"About Pail":         func() bool { return h.a.about.open },
	} {
		h.mustClick("More")
		h.tt.Frame()
		if err := h.tt.ChooseMenuItem(item); err != nil {
			t.Fatal(err)
		}
		h.settle()
		if !open() {
			t.Errorf("%s did not open its dialog", item)
		}
		h.tt.Key(0, ui.KeyEscape)
		h.settle()
	}

	urls := make(chan string, 1)
	h.a.openURL = func(u string) error { urls <- u; return nil }
	h.mustClick("More")
	h.tt.Frame()
	if err := h.tt.ChooseMenuItem("Documentation"); err != nil {
		t.Fatal(err)
	}
	select {
	case u := <-urls:
		if u != docsURL || !strings.HasPrefix(u, "https://") {
			t.Errorf("Documentation opened %q", u)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Documentation opened nothing")
	}

	quit := false
	h.a.quit = func() { quit = true }
	h.mustClick("More")
	h.tt.Frame()
	if err := h.tt.ChooseMenuItem("Quit"); err != nil || !quit {
		t.Fatalf("Quit: %v, quit=%v", err, quit)
	}
}

// ---- the command palette -------------------------------------------------------------------------

func TestPaletteListsEveryCommandAndBuckets(t *testing.T) {
	h := newHarness(t, "light")
	h.openPaletteWith(ui.Cmd, ui.KeyK)
	entries := h.a.paletteEntries()
	have := map[string]bool{}
	for _, e := range entries {
		have[e.label] = true
	}
	for i := range h.a.commands {
		cmd := &h.a.commands[i]
		if cmd.Visible != nil && !cmd.Visible(h.a) {
			continue
		}
		if !have[strings.TrimSuffix(cmd.text(), "…")] {
			t.Errorf("the palette lacks command %s (%q)", cmd.ID, cmd.text())
		}
	}
	for _, b := range demoBuckets {
		if !have["Open bucket "+b.name] {
			t.Errorf("the palette lacks %q", "Open bucket "+b.name)
		}
	}
	h.mustHave("Upload files", "Settings", "Theme: Dark", "Open bucket acme-assets", "Ctrl+U", "Ctrl+K")
	h.mustNotHave("Lock Vault") // the vault is not in use: not applicable
	h.snap("palette-open-light")
	h.tt.Key(0, ui.KeyEscape)
	h.settle()
	if h.a.pal.open {
		t.Fatal("Escape left the palette open")
	}
}

func TestPaletteFiltersRunsAndCloses(t *testing.T) {
	h := newHarness(t, "light")
	got := h.recordTheme()
	h.openPaletteWith(ui.Cmd|ui.Shift, ui.KeyP)

	h.tt.Type("DARK")
	h.settle()
	h.mustHave("Theme: Dark")
	h.mustNotHave("Upload files", "Theme: Light")
	h.snap("palette-query-light")
	h.tt.Key(0, ui.KeyEnter)
	h.settle()
	if h.a.pal.open || h.a.opts.theme != "dark" {
		t.Fatalf("Enter did not run the choice: open=%v theme=%q", h.a.pal.open, h.a.opts.theme)
	}
	if n := len(*got); n == 0 || (*got)[n-1] != mygo.ThemeDark {
		t.Fatalf("theme calls = %v", *got)
	}

	// Several words all have to match, in the label, the heading or an alias.
	h.openPaletteWith(ui.Cmd, ui.KeyK)
	h.tt.Type("object copy")
	h.settle()
	h.mustHave("Copy key", "Copy URL")
	h.mustNotHave("Upload files", "Open bucket")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type("zzzz")
	h.settle()
	h.mustHave("No matching commands")
	h.tt.Key(0, ui.KeyEscape)
	h.settle()
	if h.a.pal.open {
		t.Fatal("Escape left the palette open")
	}

	// An alias finds a command by another word; a bucket is found by its region.
	h.openPaletteWith(ui.Cmd, ui.KeyK)
	h.tt.Type("preferences")
	h.settle()
	h.tt.Key(0, ui.KeyEnter)
	h.settle()
	if h.a.tool != "settings" {
		t.Fatalf("\"preferences\" did not run Settings (tool %q)", h.a.tool)
	}
	h.openPaletteWith(ui.Cmd, ui.KeyK)
	h.tt.Type("open bucket acme-back")
	h.settle()
	h.tt.Key(0, ui.KeyEnter)
	h.settle()
	if h.a.tool != "" || h.a.scope.Bucket != "acme-backups" {
		t.Fatalf("Open bucket ran as tool %q bucket %q", h.a.tool, h.a.scope.Bucket)
	}
}

func TestPaletteKeyboardAndMouse(t *testing.T) {
	h := newHarness(t, "light")
	h.openPaletteWith(ui.Cmd, ui.KeyK)
	entries := filterPalette(h.a.paletteEntries(), "")
	cur := firstEnabled(entries)
	if h.a.pal.cur != cur || !entries[cur].enabled {
		t.Fatalf("cursor starts at %d, want the first enabled row %d", h.a.pal.cur, cur)
	}
	// Down and Up step over the rows that cannot run, and wrap round.
	for _, d := range []int{1, 1, -1, -1, -1} {
		cur = stepPalette(entries, cur, d)
		h.tt.Key(0, map[int]ui.Key{1: ui.KeyDown, -1: ui.KeyUp}[d])
		h.settle()
		if h.a.pal.cur != cur || !entries[cur].enabled {
			t.Fatalf("after a step of %d the cursor is at %d, want %d", d, h.a.pal.cur, cur)
		}
	}
	if cur == 0 {
		t.Fatal("the wrap-around never left the first row")
	} // The typed text is not lost to the arrow keys.
	h.tt.Type("buck")
	h.settle()
	if h.a.pal.query != "buck" || h.a.pal.cur != 0 {
		t.Fatalf("query %q cursor %d after typing", h.a.pal.query, h.a.pal.cur)
	}

	// A click on a row runs it and closes the palette.
	h.mustClick("Open bucket acme-backups")
	h.settle()
	if h.a.pal.open || h.a.scope.Bucket != "acme-backups" {
		t.Fatalf("click: open=%v bucket=%q", h.a.pal.open, h.a.scope.Bucket)
	}

	// The key that opened it closes it.
	h.openPaletteWith(ui.Cmd, ui.KeyK)
	h.tt.Key(ui.Cmd, ui.KeyK)
	h.settle()
	if h.a.pal.open {
		t.Fatal("Ctrl+K did not close the palette")
	}
}

func TestPaletteDisabledCommandsAreDimmedAndDoNotRun(t *testing.T) {
	h := newHarness(t, "light")
	var dl *palEntry
	for _, e := range h.a.paletteEntries() {
		if e.label == "Download" {
			e := e
			dl = &e
		}
	}
	if dl == nil || dl.enabled {
		t.Fatalf("Download should be listed but disabled with nothing selected: %+v", dl)
	}
	h.openPaletteWith(ui.Cmd, ui.KeyK)
	h.tt.Type("download")
	h.settle()
	h.mustHave("Download")
	h.tt.Key(0, ui.KeyEnter)
	h.settle()
	if !h.a.pal.open {
		t.Fatal("a disabled command closed the palette")
	}
	// The first row of the list: the palette is 96px from the top, its search
	// row is 48px and the list pads 4px; rows are 32px.
	h.tt.ClickAt(testW/2, 96+49+4+16)
	h.settle()
	if !h.a.pal.open {
		t.Fatal("clicking a disabled command ran it")
	}
	h.tt.Key(0, ui.KeyEscape)
	h.settle()

	// Once something is selected it runs: the command is the same one the
	// toolbar uses.
	h.mustClick("manifest.json")
	if !h.a.enabled(h.a.cmd("download")) {
		t.Fatal("Download should be enabled")
	}
	ran := false
	h.a.cmd("download").Run = func(*app) { ran = true }
	h.openPaletteWith(ui.Cmd, ui.KeyK)
	h.tt.Type("download")
	h.settle()
	h.tt.Key(0, ui.KeyEnter)
	h.settle()
	if !ran || h.a.pal.open {
		t.Fatalf("Download did not run from the palette: ran=%v open=%v", ran, h.a.pal.open)
	}
}

func TestPaletteDoesNotOpenBehindADialogAndShowsVaultCommandsWhenApplicable(t *testing.T) {
	h := newHarness(t, "light")
	h.a.newFolder = true
	h.settle()
	h.tt.Key(ui.Cmd, ui.KeyK)
	h.settle()
	if h.a.pal.open {
		t.Fatal("the palette opened over a dialog")
	}
	h.a.newFolder = false
	h.settle()

	h.toVault()
	h.settle()
	h.openPaletteWith(ui.Cmd, ui.KeyK)
	h.mustHave("Lock Vault", "Unlock Vault")
	h.tt.Type("lock")
	h.settle()
	h.tt.Key(0, ui.KeyEnter)
	h.settle()
	h.waitFor("locked", func() bool { return !h.vaultState().Unlocked })
}

// ---- about and shortcuts -------------------------------------------------------------------

func TestAboutDialog(t *testing.T) {
	h := newHarness(t, "light")
	h.a.run("about", "test")
	h.settle()
	h.mustHave("About Pail", "Pail", "Version "+version, "MIT License", "Lucide icons", "ISC License", "Inter font (Linux builds)", "SIL Open Font License 1.1", "Close")
	h.snap("about-light")
	urls := make(chan string, 1)
	h.a.openURL = func(u string) error { urls <- u; return nil }
	h.mustClick("Project page")
	select {
	case u := <-urls:
		if u != repoURL {
			t.Errorf("the link opened %q", u)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the project link opened nothing")
	}
	h.mustHave("by Dejavu Moe")
	h.mustClick("Dejavu Moe")
	select {
	case u := <-urls:
		if u != authorURL || authorURL != "https://dejavu.moe" {
			t.Errorf("the author link opened %q", u)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the author link opened nothing")
	}
	h.mustClick("Close")
	if h.a.about.open {
		t.Fatal("Close left the dialog open")
	}
}

func TestSettingsAboutSection(t *testing.T) {
	h := newHarness(t, "light")
	h.a.openTool("settings")
	h.settle()
	h.mustHave("ABOUT", "Version", version, "Author", "Dejavu Moe", "Repository", "github.com/DejavuMoe/Pail", "License", "MIT License")
	urls := make(chan string, 2)
	h.a.openURL = func(u string) error { urls <- u; return nil }
	h.tt.Scroll(700, 300, 0, 3000) // the About section is the last one
	h.settle()
	h.mustClick("Dejavu Moe")
	h.mustClick("Project page")
	for _, want := range []string{"https://dejavu.moe", "https://github.com/DejavuMoe/Pail"} {
		select {
		case u := <-urls:
			if u != want {
				t.Errorf("opened %q, want %q", u, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s opened nothing", want)
		}
	}
}

func TestShortcutsDialogListsTheCommandTable(t *testing.T) {
	h := newHarness(t, "light")
	h.a.run("shortcuts", "test")
	h.settle()
	h.mustHave("Keyboard shortcuts", "TRANSFER", "OBJECT", "NAVIGATE", "VIEW", "APP", "Upload files", "Ctrl+U", "Command palette", "Ctrl+K  ·  Ctrl+Shift+P", "Refresh", "F5  ·  Ctrl+R", "Close")
	h.snap("shortcuts-light")
	h.tt.Key(0, ui.KeyEscape)
	h.settle()
	if h.a.keys.open {
		t.Fatal("Escape left the dialog open")
	}
}

// ---- the title bar ---------------------------------------------------------------------------------

// The toolbar keeps clear of the system window buttons at its right end and
// the account switcher of the traffic lights at the left end of the sidebar.
func TestTopBarKeepsClearOfWindowControls(t *testing.T) {
	h := newHarness(t, "light")
	for _, sz := range auditSizes {
		h.resize(sz.w, sz.h)
		h.tt.SetTitleBar(ui.TitleBar{Height: 44, Left: 78, Right: 138})
		h.settle()
		more, ok := h.tt.Find("More")
		if !ok {
			t.Fatalf("%s: no more button", sz.name)
		}
		if limit := float32(sz.w) - 138; more.X+more.W > limit {
			t.Errorf("%s: the more button ends at %.0f, past the window buttons at %.0f", sz.name, more.X+more.W, limit)
		}
		if more.Y < 0 || more.Y+more.H > 44 {
			t.Errorf("%s: the more button is outside the 44px bar: %+v", sz.name, more)
		}
		acct, ok := h.tt.Find(demoAccountName)
		if !ok {
			t.Fatalf("%s: no account name", sz.name)
		}
		if acct.X < 78 {
			t.Errorf("%s: the account switcher starts at %.0f, under the traffic lights (78)", sz.name, acct.X)
		}
		// With the window buttons' room taken, the labelled commands give way.
		for _, id := range []string{"upload.files", "refresh", "inspector"} {
			if r, ok := h.tt.Find(h.a.cmd(id).text()); ok && r.X+r.W > float32(sz.w)-138 && r.Y < 44 {
				t.Errorf("%s: %s ends at %.0f, under the window buttons", sz.name, id, r.X+r.W)
			}
		}
	}
	// A tool view uses the same bar.
	h.mustClick("Settings")
	h.settle()
	more, _ := h.tt.Find("More")
	if more.X+more.W > float32(auditSizes[1].w)-138 || more.Y+more.H > 44 {
		t.Errorf("tool view: the more button is at %+v", more)
	}
	h.mustHave("Back")
	h.snapAudit(auditSizes[1], "chrome-titlebar-tool")
}

// ---- Credential storage never starts by itself -------------------------------------------------

// The segmented control of Settings used to be a radio group, which "selects"
// whatever the focus moves to and reports Changed from a throwaway local: an
// arrow key, a stale report or a focus restore could open the Create master
// password dialog (a security change) without anyone asking. Only an explicit
// activation of "Encrypted vault" may open it.
func TestCredentialStorageOnlyOpensTheDialogWhenAsked(t *testing.T) {
	h := newRawHarness(t, "light", true)
	h.a.openTool("settings")
	h.settle()
	noDialog := func(what string) {
		t.Helper()
		h.tt.Frame()
		h.tt.Frame()
		if h.a.vu.create.open || h.a.vu.toKeychain.open || h.a.modalOpen() {
			t.Fatalf("a dialog opened by itself after %s", what)
		}
	}
	noDialog("the first frames")
	r, ok := h.tt.Find("Encrypted vault")
	if !ok {
		t.Fatal("no Encrypted vault segment")
	}
	cx, cy := r.X+r.W/2, r.Y+r.H/2

	h.tt.Move(cx, cy)
	noDialog("hovering the segment")
	h.tt.Press(cx, cy)
	h.tt.Move(cx, cy+300)
	h.tt.Release(cx, cy+300)
	noDialog("pressing the segment and releasing elsewhere")
	h.tt.Press(cx, cy+300)
	h.tt.Move(cx, cy)
	h.tt.Release(cx, cy)
	noDialog("pressing elsewhere and releasing on the segment")
	h.mustClick("System keychain")
	noDialog("clicking the segment already in effect")

	// Walk the keyboard over the whole page, arrows included.
	keys := []ui.Key{ui.KeyRight, ui.KeyDown, ui.KeyLeft, ui.KeyUp, ui.KeyEnd, ui.KeyHome}
	for i := 0; i < 40; i++ {
		h.tt.Key(0, ui.KeyTab)
		h.tt.Frame()
		if h.a.vu.create.open || h.a.modalOpen() {
			// Enter/Space activate a focused control; arrows must not.
			t.Fatalf("Tab %d opened a dialog", i)
		}
		if h.tt.Focused("System keychain") || h.tt.Focused("Encrypted vault") {
			for _, k := range keys {
				h.tt.Key(0, k)
				noDialog("an arrow key on the focused segment")
			}
			break
		}
	}

	// An explicit click does open it, and Escape followed by a focus return
	// plus idle frames does not reopen it.
	h.mustClick("Encrypted vault")
	if !h.a.vu.create.open {
		t.Fatal("clicking Encrypted vault did not open the dialog")
	}
	h.tt.Key(0, ui.KeyEscape)
	h.settle()
	noDialog("closing the dialog")
	h.tt.Advance(5 * time.Second)
	noDialog("waiting")

	// Enter on the focused segment is an explicit activation, too.
	for i := 0; i < 40 && !h.tt.Focused("Encrypted vault"); i++ {
		h.tt.Key(0, ui.KeyTab)
		h.tt.Frame()
	}
	if h.tt.Focused("Encrypted vault") {
		h.tt.Key(0, ui.KeyEnter)
		h.settle()
		if !h.a.vu.create.open {
			t.Error("Enter on the focused Encrypted vault segment did not open the dialog")
		}
	}
}
