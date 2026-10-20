package main

import (
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo/i18n"
)

// The generated message functions say their message in the language the app
// shows. Under test no mygo.App runs, so the language is a variable here: it
// starts as "en" (the suites assert English texts), a test switches it with
// harness.setLocale, and TestMain puts it back after each run.

var testLocale atomic.Value // string

func TestMain(m *testing.M) {
	testLocale.Store("en")
	i18n.SetLocaleFunc(func() string { return testLocale.Load().(string) })
	os.Exit(m.Run())
}

var asciiWord = regexp.MustCompile(`[A-Za-z]{3,}`)

// untranslated lists the visible texts that stay plain under the en-XA pseudo
// locale: text that is not bracketed and accented does not come from the
// catalog. Switch to en-XA first (h.setLocale("en-XA")). What it returns is
// either a string still hard-coded in the view, or user data (bucket and file
// names, server messages, product names, code values), which is fine.
func (h *harness) untranslated() []string {
	h.t.Helper()
	h.tt.Frame()
	var out []string
	seen := map[string]bool{}
	for _, s := range h.tt.Texts() {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] || strings.HasPrefix(s, "[") || !asciiWord.MatchString(s) {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// setLocale makes the app show tag ("" for English) in the headless window and
// in the message functions, as App.SetLocale does in the real window.
func (h *harness) setLocale(tag string) {
	h.t.Helper()
	if tag == "" {
		tag = "en"
	}
	testLocale.Store(tag)
	h.t.Cleanup(func() { testLocale.Store("en") })
	h.tt.SetLocale(tag)
	h.settle()
}

// plural is the English count phrase the older tests build their expectations with.
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return groupInt(n) + " " + many
}
