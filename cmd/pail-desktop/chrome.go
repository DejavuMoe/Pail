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
	b.Menu(func(m *ui.Menu) {
		run := func(id string) {
			a.run(id, "menu")
		}
		item := func(id string) {
			cmd := a.cmd(id)
			it := m.Item(cmd.text()).Disabled(!a.enabled(cmd))
			if len(cmd.Chords) > 0 {
				it.Shortcut(cmd.Chords[0].Mods, cmd.Chords[0].Key)
			}
			if it.Chosen() {
				run(id)
			}
		}
		item("palette")
		item("tool.settings")
		m.Separator()
		m.Submenu(locales.ChromeTheme(), func(sm *ui.Menu) {
			for _, mode := range themeModes {
				cmd := a.cmd("theme." + mode)
				if sm.Item(themeName(locales.In(i18n.Locale()), mode)).Checked(a.themeMode() == mode).Chosen() {
					run(cmd.ID)
				}
			}
		})
		m.Submenu(locales.ChromeLanguage(), func(sm *ui.Menu) {
			for _, ch := range languageChoices() {
				if sm.Item(ch.Label).Checked(a.lang == ch.Value).Chosen() {
					a.setLanguage(ch.Value)
				}
			}
		})
		m.Separator()
		item("shortcuts")
		item("docs")
		item("about")
		m.Separator()
		item("quit")
	})
}

// themeMode is the current mode, "system" when none is set.
func (a *app) themeMode() string {
	if a.opts.theme == "" {
		return "system"
	}
	return a.opts.theme
}
