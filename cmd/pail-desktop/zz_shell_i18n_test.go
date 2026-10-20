package main

import (
	"strings"
	"testing"
	"time"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/egoist/mygo/ui"
)

// shellForeignTexts are visible English texts that belong to files other than
// the shell's (the command table, the inspector, the tool views, ...), user data
// and S3 values; they are not this test's business.
var shellForeignTexts = map[string]bool{
	// user data, S3 values, product and demo names
	"acme-analytics": true, "acme-assets": true, "acme-backups": true, "acme-logs": true,
	"acme-media-archive": true, "acme-versioned": true, "acme-website": true,
	"assets": true, "backups": true, "logs": true, "reports": true, "tmp": true, "website": true,
	"manifest.json": true, "README.txt": true, "hello.bin": true, "us-east-1": true, "us-west-2": true,
	"eu-west-1": true, "ap-southeast-2": true, "Demo · in-memory S3": true, "CloudFront": true,
	"Divider": true, "STANDARD": true,
	// the command table (toolbar, menus, tool names): the lead's
	"Upload": true, "Download": true, "New folder": true, "Copy": true, "Cut": true, "Paste": true,
	"Rename": true, "Delete": true, "Refresh": true, "Toggle transfers": true, "Toggle inspector": true,
	"More": true, "Sync jobs": true, "Upload rules": true, "MIME types": true,
	"Multipart uploads": true, "Settings": true,
	// the inspector: another agent's
	"Overview": true, "Headers": true, "Tags": true, "Permissions": true, "Versions": true, "Preview": true,
	"Multiple selection": true, "Folders": true, "Objects": true, "Total size": true,
	"Modified": true, "Folder": true, "JSON file": true, "TXT file": true, "BIN file": true,
	"2 items · 748 B": true, "Current folder": true, "Items": true, "Region": true, "Account": true,
	"Clear": true, "acme-assets/": true,
	// a toast said before the language changed stays as it was said
	"Uploaded 1 file": true,
}

// shellStays reports whether a still-English text may stay under en-XA.
func shellStays(s string) bool {
	if shellForeignTexts[s] {
		return true
	}
	// sizes, counts and times written in digits; a task title is the core's
	for _, pre := range []string{"Upload ", "Download "} {
		if strings.HasPrefix(s, pre) && strings.Contains(s, ".") {
			return true
		}
	}
	return strings.HasPrefix(s, "s3://") || strings.HasPrefix(s, "→ s3://") || strings.HasPrefix(s, "Counting") || strings.HasPrefix(s, "Scan ") || strings.HasPrefix(s, "found ")
}

func (h *harness) shellUntranslated(screen string) {
	h.t.Helper()
	for _, s := range h.untranslated() {
		if !shellStays(s) {
			h.t.Errorf("%s: %q stays English under en-XA", screen, s)
		}
	}
}

func TestI18NShell(t *testing.T) {
	h := newHarness(t, "light")
	h.mustClick("manifest.json")
	h.tt.ClickWith(ui.Shift, "README.txt")
	h.settle()
	if h.a.sel.Len() != 2 {
		t.Fatalf("selected %d rows", h.a.sel.Len())
	}

	// A finished upload for the transfers panel.
	h.a.uploadPaths([]string{writeTemp(t, "hello.bin", []byte("hi"))})
	h.waitTask("Upload hello.bin", core.StatusSucceeded)
	h.a.transfersOpen, h.a.transTab = true, 1
	h.settle()

	// ---- English stays as it was ----
	h.mustHave("BUCKETS", "TOOLS", "Name", "Storage class", "2 selected, 748 B", "Completed 2", "Active 0", "Clear finished")
	h.mustNotHave("已选")

	// ---- pseudo locale: nothing of the shell stays English ----
	h.setLocale("en-XA")
	h.shellUntranslated("main view")
	h.a.transTab = 0
	h.settle()
	h.shellUntranslated("transfers, active (empty)")
	h.a.transTab = 2
	h.settle()
	h.shellUntranslated("transfers, failed (empty)")
	h.a.transTab = 1
	h.settle()
	h.shellUntranslated("transfers, completed")

	h.a.askDelete()
	h.settle()
	h.shellUntranslated("delete dialog")
	h.a.closeDelete()
	h.settle()

	h.a.openNewFolder()
	h.settle()
	h.shellUntranslated("new folder dialog")
	h.a.newFolder = false
	h.settle()

	if err := h.tt.Click(locales.CommonAccount()); err != nil {
		t.Fatal(err)
	}
	h.tt.Frame()
	for _, item := range h.menu() {
		if !strings.HasPrefix(item, "[") && asciiWord.MatchString(item) && item != "Demo · in-memory S3" {
			t.Errorf("account menu: %q stays English under en-XA", item)
		}
	}
	h.closeMenu()
	h.settle()

	// ---- Chinese ----
	h.setLocale("zh-Hans")
	h.mustHave("存储桶", "工具", "名称", "大小", "类型", "修改时间", "存储类别",
		"已选 2 项，748 B", "传输", "已完成 2", "进行中 0", "失败 0", "清除已结束项", "9 项", "平铺", "显示已删除")
	h.mustNotHave("BUCKETS", "TOOLS", "Clear finished")
	h.mustHave("标准") // a storage class of the table

	h.a.transTab = 0
	h.settle()
	h.mustHave("没有进行中的传输")
	h.a.transTab = 2
	h.settle()
	h.mustHave("没有失败的传输")
	h.a.transTab = 1
	h.settle()
	h.mustHave("2 B", "Upload hello.bin") // the finished upload: its size, and the core's own title

	h.a.askDelete()
	h.settle()
	h.mustHave("删除 2 项？", "将删除 2 个对象（748 B）", "此操作无法撤销。", "取消", "账户", "存储桶", "存储桶根目录")
	h.a.closeDelete()
	h.settle()

	h.a.openNewFolder()
	h.settle()
	h.mustHave("新建文件夹", "位置", "创建", "取消")
	h.a.newFolder = false
	h.settle()

	if err := h.tt.Click("账户"); err != nil {
		t.Fatal(err)
	}
	h.tt.Frame()
	menu := strings.Join(h.menu(), "|")
	for _, want := range []string{"添加账户…", "编辑账户…", "移除账户…"} {
		if !strings.Contains(menu, want) {
			t.Errorf("account menu %q lacks %q", menu, want)
		}
	}
	h.closeMenu()
	h.settle()

	// typeLabel and storageLabel
	if got := typeLabel(Entry{Key: "a/b.png"}); got != "PNG 文件" {
		t.Errorf("typeLabel = %q", got)
	}
	if got := storageLabel("GLACIER_IR"); got != "Glacier 即时检索" {
		t.Errorf("storageLabel = %q", got)
	}
	// humanError: the headline is translated, the code stays
	err := &core.OpError{Kind: core.ErrAccessDenied, Code: "AccessDenied"}
	if got := humanError(err); !strings.HasPrefix(got, "访问被拒绝") || !strings.HasSuffix(got, " (AccessDenied)") {
		t.Errorf("humanError = %q", got)
	}

	// the empty search shows its own text
	h.setLocale("en")
	h.a.search = "zzzz"
	h.a.rebuildRows()
	h.settle()
	h.mustHave("No matches", "Nothing in this folder matches “zzzz”", "Clear filter", "0 items of 9")
	h.setLocale("zh-Hans")
	h.mustHave("没有匹配项", "此文件夹中没有与“zzzz”匹配的项", "清除筛选", "共 9 项")
	h.a.search = ""
	h.a.rebuildRows()
	h.settle()

	// ---- help on the "Flat" and "Show deleted" toggles ----
	h.setLocale("en")
	h.a.openBucket(demoVersioned) // versioning is on here, so "Show deleted" is live
	h.settle()
	h.waitFor("versioning known", func() bool { _, ok := h.a.versioningOf(h.a.accountID, demoVersioned); return ok })
	for label, tip := range map[string]string{
		"Flat":         locales.FlatListTip(),
		"Show deleted": locales.PathbarShowDeletedTip(),
	} {
		r, ok := h.tt.Find(label)
		if !ok {
			t.Fatalf("no %q toggle", label)
		}
		h.tt.Move(r.X+r.W/2, r.Y+r.H/2)
		h.tt.Advance(700 * time.Millisecond)
		h.tt.Frame()
		if !h.tt.HasText(tip) {
			t.Errorf("hovering %q shows no tip %q", label, tip)
		}
		h.tt.Move(1, 1)
		h.tt.Advance(700 * time.Millisecond)
		h.tt.Frame()
	}
	h.setLocale("zh-Hans")
	r, ok := h.tt.Find("平铺")
	if !ok {
		t.Fatal("no 平铺 toggle")
	}
	h.tt.Move(r.X+r.W/2, r.Y+r.H/2)
	h.tt.Advance(700 * time.Millisecond)
	h.tt.Frame()
	h.mustHave("平铺列出此文件夹下的所有对象（包括子文件夹中的），不显示文件夹。")
}
