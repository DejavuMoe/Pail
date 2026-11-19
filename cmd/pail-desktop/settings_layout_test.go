package main

import "testing"

// The Settings page (settings_layout.go): the rail of sections beside the
// column of cards.

// Choosing a section in the rail brings it into view and keeps it marked, also
// when the page is too short for it to reach the top; scrolling by hand gives
// the mark back to the place the page stands in.
func TestSettingsRailBringsASectionIntoView(t *testing.T) {
	h := newHarness(t, "light")
	h.openToolView("settings")
	h.mustHave("Sections")
	n := &h.a.srail
	if got := n.spied(); got != "appearance" {
		t.Fatalf("a page at its top stands in %q, want appearance", got)
	}
	if r, ok := h.tt.Find("Write diagnostic log"); !ok || r.Y < testH {
		t.Fatalf("Diagnostics starts on the first screen (%v): this test needs it below", r)
	}

	h.mustClick("Diagnostics") // the rail's row comes before the group's title
	h.settle()
	h.rest()
	h.tt.Frame()
	r, ok := h.tt.Find("Write diagnostic log")
	if !ok || r.Y < topBarH || r.Y+r.H > testH {
		t.Fatalf("the Diagnostics rows are not in view after choosing the section: %v", r)
	}
	if n.pin != "diagnostics" {
		t.Fatalf("the rail marks %q, want the section that was chosen", n.pin)
	}
	if n.scroll.Y <= 0 {
		t.Fatal("the page did not scroll")
	}

	h.tt.Scroll(700, 300, 0, -200)
	h.settle()
	h.tt.Frame()
	if n.pin != "" {
		t.Fatalf("after scrolling by hand the rail still holds %q", n.pin)
	}
}

// spied follows the scroll position, and the end of the page belongs to the
// last section even when it is too short to reach the top.
func TestSettingsRailFollowsThePage(t *testing.T) {
	n := settingsRail{tops: map[string]float32{"appearance": 0, "transfers": 300, "downloads": 700, "about": 1100}}
	n.scroll.MaxY = 900
	for _, c := range []struct {
		y    float32
		want string
	}{
		{0, "appearance"},
		{300 - settingsSpyLead - 1, "appearance"},
		{300 - settingsSpyLead, "transfers"},
		{699, "downloads"},
		{899.5, "about"}, // the end of the page
	} {
		n.scroll.Y = c.y
		if got := n.spied(); got != c.want {
			t.Errorf("at %v the page stands in %q, want %q", c.y, got, c.want)
		}
	}
}

// The rail needs settingsRailNeed of width. Below it the page is the column
// alone, and every section is still there to scroll to.
func TestSettingsRailLeavesWhenThePageIsNarrow(t *testing.T) {
	h := newHarness(t, "light")
	h.resize(960, 600)
	h.openToolView("settings")
	h.mustNotHave("Sections")
	h.mustHave("Appearance", "Theme", "Language")
	h.showSetting("License")
	h.mustHave("About", "MIT License")
	h.resize(testW, testH)
	h.mustHave("Sections")
}
