package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/i18n"
)

// Languages (DESIGN.md "文案与本地化"): the messages live in locales/*.json and
// the typed functions in locales/catalog_gen.go (regenerate with
// `go run github.com/egoist/mygo/cmd/mygo generate` in this directory). They
// say their message in the language the app shows (mygo.App.Locale), so a
// view that calls them every frame follows a language change at once.
//
// The language choice is "" (follow the system, the default) or a tag of
// appLanguages. mygo saves its own copy of the choice; this app keeps one
// more in a small file so that Settings can tell "follow the system" from a
// choice that happens to match it, on every platform.

// appLanguages are the languages of the catalogs, source language first.
var appLanguages = []string{"en", "zh-Hans"}

const languageFile = "language"

// languageName is a language written in itself; "" is the follow-system choice.
func languageName(tag string) string {
	if tag == "" {
		return locales.LanguageFollowSystem()
	}
	return i18n.Autonym(tag)
}

// languageChoices are the options of the Language setting, "" first.
func languageChoices() []choice {
	out := []choice{{"", languageName("")}}
	for _, t := range appLanguages {
		out = append(out, choice{t, languageName(t)})
	}
	return out
}

func languagePath() string {
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil || dir == "" {
		return ""
	}
	return filepath.Join(dir, languageFile)
}

// loadLanguageChoice reads the saved choice; MYGO_LOCALE (a dev override that
// mygo never saves) wins and shows as the choice for that run.
func loadLanguageChoice() string {
	if tag := strings.TrimSpace(os.Getenv("MYGO_LOCALE")); tag != "" {
		return i18n.Normalize(tag)
	}
	p := languagePath()
	if p == "" {
		return ""
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return validLanguage(strings.TrimSpace(string(data)))
}

// saveLanguageChoice keeps the choice; "" removes the file.
func saveLanguageChoice(tag string) {
	p := languagePath()
	if p == "" || strings.TrimSpace(os.Getenv("MYGO_LOCALE")) != "" {
		return
	}
	if tag == "" {
		_ = os.Remove(p)
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err == nil {
		_ = os.WriteFile(p, []byte(tag+"\n"), 0o644)
	}
}

// validLanguage returns tag when it names one of the app's languages.
func validLanguage(tag string) string {
	for _, t := range appLanguages {
		if t == tag {
			return tag
		}
	}
	return ""
}

// setLanguage applies a choice at once (every label is said again on the next
// frame), and keeps it.
func (a *app) setLanguage(tag string) {
	if tag == a.lang {
		return
	}
	a.lang = tag
	a.localeSet(tag)
	a.langSave(tag)
}

// languageItems is the native menu's Language submenu: radio items, each
// language named in itself, "Follow system" first.
func (a *app) languageItems(redraw func()) []*mygo.MenuItem {
	var out []*mygo.MenuItem
	for _, ch := range languageChoices() {
		tag := ch.Value
		out = append(out, &mygo.MenuItem{
			Label: ch.Label, Type: mygo.MenuItemRadio, Checked: tag == a.lang,
			Click: func(*mygo.MenuItem, *mygo.Window) { a.setLanguage(tag); redraw() },
		})
	}
	return out
}
