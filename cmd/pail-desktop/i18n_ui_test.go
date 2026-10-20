package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestLanguageSwitchChangesTextsWithoutRestart(t *testing.T) {
	h := newHarness(t, "light")
	h.mustHave("Upload", "Download", "Transfers", "Settings")
	h.a.setLanguage("zh-Hans")
	h.settle()
	h.mustHave("上传", "下载", "传输", "设置", "存储桶")
	h.mustNotHave("Upload", "Download", "BUCKETS")
	if h.a.lang != "zh-Hans" {
		t.Fatalf("lang = %q", h.a.lang)
	}
	// Back to following the system, which in a test is English.
	h.a.setLanguage("")
	h.settle()
	h.mustHave("Upload", "Download", "Transfers")
	h.mustNotHave("上传")
}

func TestLanguageFromTheMoreMenu(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("More")
	h.tt.Frame()
	if err := h.tt.ChooseMenuItem("Language", "简体中文"); err != nil {
		t.Fatal(err)
	}
	h.settle()
	if h.a.lang != "zh-Hans" {
		t.Fatalf("lang = %q after choosing 简体中文", h.a.lang)
	}
	h.mustHave("上传")
	// The menu says its own words in the new language; the follow-system item
	// and each language in itself.
	h.mustClick("更多")
	h.tt.Frame()
	if err := h.tt.ChooseMenuItem("语言", "跟随系统"); err != nil {
		t.Fatal(err)
	}
	h.settle()
	if h.a.lang != "" {
		t.Fatalf("lang = %q after Follow system", h.a.lang)
	}
	h.mustHave("Upload")
}

func TestLanguageChoiceIsSavedAndRestored(t *testing.T) {
	h := newHarness(t, "light")
	var saved []string
	h.a.langSave = func(tag string) { saved = append(saved, tag) }
	h.a.setLanguage("zh-Hans")
	h.a.setLanguage("zh-Hans") // no change, nothing saved
	h.a.setLanguage("")
	if len(saved) != 2 || saved[0] != "zh-Hans" || saved[1] != "" {
		t.Fatalf("saved %q, want [zh-Hans \"\"]", saved)
	}
	if validLanguage("fr") != "" || validLanguage("zh-Hans") != "zh-Hans" || validLanguage("") != "" {
		t.Fatal("validLanguage accepts what the app does not ship")
	}
}

func TestHelpIconsShowTheirTipOnHoverAndOnKeyboardFocus(t *testing.T) {
	for _, loc := range []string{"en", "zh-Hans"} {
		t.Run(loc, func(t *testing.T) {
			h := newHarness(t, "light")
			h.setLocale(loc)
			h.a.openAccountForm(false)
			h.a.af.provider = 1
			h.a.af.advanced = true
			h.settle()
			icons := h.helpIcons()
			if len(icons) < 6 {
				t.Fatalf("the account dialog has %d help icons: %q", len(icons), icons)
			}
			for _, s := range icons {
				if len(s) > 400 {
					t.Errorf("a help text is too long for a tooltip (%d bytes): %.60q", len(s), s)
				}
			}
			// Hover.
			h.hover(icons[1])
			if !h.tipShown(icons[1]) {
				t.Errorf("hovering the icon did not show %q", icons[1])
			}
			h.tt.Move(2, 2)
			h.tt.Advance(200 * time.Millisecond)
			h.tt.Frame()
			// Keyboard focus: Tab until an icon has the focus, its tip shows.
			shown := false
			for i := 0; i < 80 && !shown; i++ {
				h.tt.Key(0, ui.KeyTab)
				h.tt.Frame()
				for _, s := range icons {
					if h.tt.Focused(s) && h.tipShown(s) {
						shown = true
					}
				}
			}
			if !shown {
				t.Error("Tab never reached a help icon with its tooltip showing")
			}
		})
	}
}
