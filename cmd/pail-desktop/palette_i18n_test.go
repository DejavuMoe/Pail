package main

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// palette entries as the filter sees them.
func pe(label, english, group string, enabled bool) palEntry {
	return palEntry{label: label, english: english, group: group, enabled: enabled,
		hay: searchText(label, english, group)}
}

func labelsOf(es []palEntry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.label)
	}
	return out
}

func TestFilterPaletteRanksPrefixThenWordThenSubstring(t *testing.T) {
	all := []palEntry{
		pe("Reupload", "Reupload", groupObject, true),           // substring
		pe("Quick upload", "Quick upload", groupObject, true),   // word prefix
		pe("Upload files", "Upload files", groupTransfer, true), // prefix
		pe("Upload folder", "Upload folder", groupTransfer, false),
	}
	got := labelsOf(filterPalette(all, "upload"))
	want := []string{"Upload files", "Upload folder", "Quick upload", "Reupload"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("ranking = %q, want %q", got, want)
	}
}

func TestFilterPaletteEmptyQueryListsEnabledFirst(t *testing.T) {
	all := []palEntry{
		pe("A off", "A off", groupTransfer, false),
		pe("B on", "B on", groupObject, true),
		pe("C off", "C off", groupObject, false),
		pe("D on", "D on", groupApp, true),
	}
	got := labelsOf(filterPalette(all, ""))
	want := []string{"B on", "D on", "A off", "C off"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("order = %q, want %q", got, want)
	}
}

func TestPaletteGroupsAreTaskBased(t *testing.T) {
	h := newHarness(t, "light")
	seen := map[string]bool{}
	for _, e := range h.a.paletteEntries() {
		seen[e.heading] = true
	}
	for _, g := range []string{"Transfer", "Object", "Navigate", "View", "Bucket", "Tools", "App"} {
		if !seen[g] {
			t.Errorf("no palette heading %q; have %v", g, seen)
		}
	}
	for _, old := range []string{"File", "Edit", "General", "Appearance", "Go to"} {
		if seen[old] {
			t.Errorf("old heading %q is still used", old)
		}
	}
	h.setLocale("zh-Hans")
	zh := map[string]bool{}
	for _, e := range h.a.paletteEntries() {
		zh[e.heading] = true
	}
	for _, g := range []string{"传输", "对象", "导航", "视图", "存储桶", "工具", "应用"} {
		if !zh[g] {
			t.Errorf("no Chinese palette heading %q; have %v", g, zh)
		}
	}
}

func TestPaletteDisabledCommandsComeAfterEnabled(t *testing.T) {
	h := newHarness(t, "light")
	es := filterPalette(h.a.paletteEntries(), "")
	seenOff := false
	for _, e := range es {
		if !e.enabled {
			seenOff = true
		} else if seenOff {
			t.Fatalf("enabled %q after a disabled command", e.label)
		}
	}
	if !seenOff {
		t.Fatal("expected some disabled commands without a selection")
	}
}

func TestPaletteFindsChineseAndEnglishInChinese(t *testing.T) {
	h := newHarness(t, "light")
	h.setLocale("zh-Hans")
	h.openPaletteWith(ui.Cmd, ui.KeyK)
	h.tt.Type("上传")
	h.settle()
	h.mustHave("上传文件", "上传文件夹")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type("upload")
	h.settle()
	h.mustHave("上传文件")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type("预签名")
	h.settle()
	h.mustHave("复制预签名 URL")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type("语言")
	h.settle()
	h.mustHave("语言：English", "语言：简体中文", "语言：跟随系统")
}

func TestPaletteFindsEnglishAndChineseAliasesInEnglish(t *testing.T) {
	h := newHarness(t, "light")
	h.openPaletteWith(ui.Cmd, ui.KeyK)
	h.tt.Type("upload")
	h.settle()
	h.mustHave("Upload files", "Upload folder")
	// A Chinese keyword reaches the English label too.
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type("上传")
	h.settle()
	h.mustHave("Upload files")
}

func TestPaletteLanguageCommandSwitchesTheApp(t *testing.T) {
	h := newHarness(t, "light")
	h.openPaletteWith(ui.Cmd, ui.KeyK)
	h.tt.Type("language: 简体")
	h.settle()
	h.tt.Key(0, ui.KeyEnter)
	h.settle()
	if h.a.lang != "zh-Hans" {
		t.Fatalf("lang = %q", h.a.lang)
	}
	h.mustHave("上传")
	h.a.setLanguage("")
	h.settle()
	h.mustHave("Upload")
}
