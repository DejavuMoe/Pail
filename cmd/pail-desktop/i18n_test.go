package main

import (
	"os"
	"regexp"
	"slices"
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

// TestMain sets what the whole process shares. Most tests call t.Parallel():
// each has its own harness, demo server and mygo Tester (Testers run in
// parallel since mygo v0.10). A test that changes what the process shares (the
// locale, through setLocale or the app's localeSet; nativeMenus; goos) stays
// sequential, and so does every test in a file with such tests: Go runs all
// sequential tests before any parallel one resumes, so they never overlap, and
// the parallel ones see English.
func TestMain(m *testing.M) {
	// The suites drive the in-window menus on every platform; the native
	// path has its own test (TestNativeMenusOnMacOS).
	nativeMenus = false
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

// plainSet is the set of texts untranslated lists now. Taken before a screen
// opens, it holds what the screens around it leave plain.
func (h *harness) plainSet() map[string]bool {
	h.t.Helper()
	set := map[string]bool{}
	for _, s := range h.untranslated() {
		set[s] = true
	}
	return set
}

// newPlainTexts lists the texts that a screen opened by open shows in the
// pseudo locale without being bracketed (that is, not taken from the catalog)
// and that the screen before it did not show.
func (h *harness) newPlainTexts(open func()) []string {
	h.t.Helper()
	h.setLocale("en-XA")
	before := h.plainSet()
	open()
	h.settle()
	var out []string
	for _, s := range h.untranslated() {
		if !before[s] {
			out = append(out, s)
		}
	}
	h.setLocale("en")
	return out
}

// plainOK reports whether a text may stay plain English under en-XA because it
// is not catalog text: user data, S3 and HTTP values, product names, or the
// text of another screen.
type plainOK func(s string) bool

// plainBase allows what stays plain on every screen: product names, the demo
// account, region ids and S3 addresses.
var plainBase = []plainOK{
	plainIn(map[string]bool{"Pail": true, "AWS": true, "S3": true, "MinIO": true, "CloudFront": true}),
	plainSub(demoAccountName),
	plainPrefix("s3://"),
	plainMatch(regexp.MustCompile(`^[a-z]{2}-[a-z]+-\d$`)),
}

// plainIn allows the texts of set.
func plainIn(set map[string]bool) plainOK { return func(s string) bool { return set[s] } }

// plainSub allows a text that contains one of subs.
func plainSub(subs ...string) plainOK {
	return func(s string) bool {
		return slices.ContainsFunc(subs, func(sub string) bool { return strings.Contains(s, sub) })
	}
}

// plainPrefix allows a text that starts with one of pres.
func plainPrefix(pres ...string) plainOK {
	return func(s string) bool {
		return slices.ContainsFunc(pres, func(pre string) bool { return strings.HasPrefix(s, pre) })
	}
}

// plainMatch allows a text that one of res matches.
func plainMatch(res ...*regexp.Regexp) plainOK {
	return func(s string) bool {
		return slices.ContainsFunc(res, func(re *regexp.Regexp) bool { return re.MatchString(s) })
	}
}

// expectTranslated fails on every text of the screen that stays plain English
// under en-XA (switch to it first) and that neither plainBase nor ok allows.
func (h *harness) expectTranslated(where string, ok ...plainOK) {
	h.t.Helper()
	allow := slices.Concat(plainBase, ok)
	var left []string
	for _, s := range h.untranslated() {
		if !slices.ContainsFunc(allow, func(f plainOK) bool { return f(s) }) {
			left = append(left, s)
		}
	}
	if len(left) > 0 {
		h.t.Errorf("%s: texts that stay plain English under en-XA: %q", where, left)
	}
}
