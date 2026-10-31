package main

import (
	"fmt"
	"runtime"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/icons"
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
)

// The window chrome (DESIGN.md "Window appearance"): the system title bar is
// hidden on every platform, the sidebar and a full-width toolbar start at the
// top edge of the window, and the system window buttons sit over the right end
// of the toolbar (the traffic lights over the left end of the sidebar header
// on macOS).

const (
	// topBarH is the height of the toolbar and of the account switcher; it is
	// also the TitleBarHeight the system window buttons are drawn at.
	topBarH = 44

	repoURL = "https://github.com/DejavuMoe/Pail"
	docsURL = repoURL + "#readme"

	// The author is a name, not a translation: it stays as it is in every language.
	authorName = "Dejavu Moe"
	authorURL  = "https://dejavu.moe"
)

// windowOptions are the options of the main window. Its background follows the
// system appearance until the first frame, which avoids a flash of the wrong
// color; the title bar is hidden so that the app draws it.
func windowOptions(title string, w, h int, content mygo.Content) mygo.WindowOptions {
	style := mygo.TitleBarHidden
	if runtime.GOOS == "darwin" {
		style = mygo.TitleBarHiddenInset
	}
	return mygo.WindowOptions{
		Title: title, Width: w, Height: h, MinWidth: 960, MinHeight: 600,
		TitleBarStyle: style, TitleBarHeight: topBarH,
		BackgroundColor: "light-dark(" + cssHex(lightTokens.Background) + ", " + cssHex(darkTokens.Background) + ")",
		Content:         content,
	}
}

func cssHex(c ui.Color) string { return fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B) }

// syncTheme tells the system which appearance the app uses, so that the window
// buttons and the native menus and dialogs follow it. It runs on the main
// thread (the view builds there) and only when the mode changed.
func (a *app) syncTheme() {
	mode := a.opts.theme
	if mode == "" {
		mode = "system"
	}
	if mode == a.themeSynced {
		return
	}
	a.themeSynced = mode
	a.themeSet(mygo.ThemeSource(mode))
}

// installMenu puts the native menu bar on the platforms that have one by
// convention: only macOS. Windows and Linux get no menu bar; their commands
// are reached from the toolbar, the context menus and the command palette.
// It reports whether a menu was installed.
func installMenu(a *app, goos string, set func(*mygo.Menu), redraw func()) bool {
	if goos != "darwin" {
		return false
	}
	set(a.nativeMenu(redraw))
	return true
}

// barRight is the right end of the top bar: the panel toggles and the "more"
// menu. The room of the system window buttons comes after it, from the bar's
// right padding.
func (a *app) barRight(c *ui.Context) {
	a.toggleButton(c, "transfers", a.transfersOpen)
	if a.tool == "" {
		a.toggleButton(c, "inspector", a.inspectorOpen)
	}
	a.moreMenu(c)
}

// moreMenu is the "⋯" button and its menu: the app-level commands that the
// native menu bar holds on macOS.
func (a *app) moreMenu(c *ui.Context) {
	b := ghostButton(c, icons.Get("ellipsis"), "", locales.ChromeMore(), false).Label(locales.ChromeMore())
	a.buttonMenu(c, b, "more", locales.ChromeMore(), func(m *menuBuilder) {
		m.Command(a, "palette", "menu")
		m.Command(a, "tool.settings", "menu")
		m.Separator()
		m.Submenu(locales.ChromeTheme(), func(sm *menuBuilder) {
			for _, mode := range themeModes {
				cmd := a.cmd("theme." + mode)
				sm.Item(themeName(locales.In(i18n.Locale()), mode), func() { a.run(cmd.ID, "menu") }).
					Icon(cmd.Icon).Radio(a.themeMode() == mode)
			}
		}).Icon("sun")
		m.Submenu(locales.ChromeLanguage(), func(sm *menuBuilder) {
			for _, ch := range languageChoices() {
				sm.Item(ch.Label, func() { a.setLanguage(ch.Value) }).Radio(a.lang == ch.Value)
			}
		}).Icon("globe")
		m.Separator()
		m.Command(a, "shortcuts", "menu")
		m.Command(a, "docs", "menu")
		m.Command(a, "about", "menu")
		m.Separator()
		m.Command(a, "quit", "menu")
	})
}

// themeMode is the current mode, "system" when none is set.
func (a *app) themeMode() string {
	if a.opts.theme == "" {
		return "system"
	}
	return a.opts.theme
}
