package main

import (
	core "github.com/DejavuMoe/Pail/internal/app"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// objectsAllowed lists the texts that stay plain under the en-XA pseudo locale
// because they are data (names, ids, S3 codes, server answers), not catalog
// text. A text matches when it equals an entry or contains one of the
// substrings.
var objectsAllowedExact = map[string]bool{
	// chrome of dialogs.go / mgmt.go (other areas): the scope rows and buttons of a confirmation
	"Bucket": true, "Cancel": true, "Switch account": true, "Deleted": true, // objects.go badge
	// csv cells, demo data
	"exports": true, "name": true, "plan": true, "region": true, "mrr": true, "team": true, "pro": true, "free": true, "id": true,
	"Standard-IA": true,
	"GET":         true, "PUT": true, "ETag": true, "URI": true, "URL": true, "CDN URL": true,
	"Content-Type": true, "Content-Encoding": true, "Content-Language": true, "Content-Disposition": true,
	"Cache-Control": true, "Expires": true,
}

var objectsAllowedSub = []string{
	"manifest.json", "README.txt", "notes.txt", "report.pdf", "old-plan.txt", "app.yaml", "compact.json",
	"acme-", "s3://", "http://", "https://", "build-id", "uploaded-by", "source-sha", "ci-deploy", "application/",
	"text/plain", "STANDARD", "AES256", "aws:kms", "max-age", "Demo", "demo", "X-Amz", "0x", "bytes)", "KiB",
	"pail-test-owner", "PNG file", "image/", "aws:x", "no-cache", "canned.bin", "empty.txt", "huge.png", "bad.png", "blob.bin", "customers-export.csv", "Northwind",
}

// objectsStatus matches the status bar text of the shell ("2 selected, 1.0 KB").
var objectsStatus = regexp.MustCompile(`^([\d,]+ (selected|items?)\b|[A-Za-z0-9_.-]{8}|[a-z0-9/-]+/)$|^[\d,]+ (selected|items?)\b`)

func objectsData(s string) bool {
	if objectsAllowedExact[s] || objectsStatus.MatchString(s) {
		return true
	}
	for _, sub := range objectsAllowedSub {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// objectsChrome holds the texts around the screens under test that this suite
// does not own (sidebar, toolbar, table, status bar): they are taken once per
// test, with the inspector closed and no dialog open, and ignored afterwards.
var objectsChrome = map[string]bool{}

func (h *harness) chrome() {
	h.t.Helper()
	was := h.a.inspectorOpen
	h.a.inspectorOpen = false
	h.settle()
	h.setLocale("en-XA")
	for _, s := range h.untranslated() {
		objectsChrome[s] = true
	}
	h.setLocale("en")
	h.a.inspectorOpen = was
	h.settle()
}

// pseudo shows the screen in the en-XA pseudo locale and fails on every text
// that stays plain English and is not data or chrome of another area.
func (h *harness) pseudo(name string) {
	h.t.Helper()
	h.setLocale("en-XA")
	for _, s := range h.untranslated() {
		if !objectsData(s) && !objectsChrome[s] {
			h.t.Errorf("%s: untranslated text %q", name, s)
		}
	}
	h.setLocale("en")
}

// zh shows the screen in Chinese and checks that the texts are there.
func (h *harness) zh(texts ...string) {
	h.t.Helper()
	h.setLocale("zh-Hans")
	h.mustHave(texts...)
	h.setLocale("en")
}

// dialogIn opens a dialog with open while the app speaks tag (dialogs keep the
// words they were opened with), runs check on it and closes it again.
func (h *harness) dialogIn(tag string, open func(), check func()) {
	h.t.Helper()
	h.setLocale(tag)
	open()
	h.settle()
	check()
	h.a.confirm.open = false
	h.a.objOps.sg.open = false
	h.setLocale("en")
}

// pseudoDialog and zhDialog are pseudo and zh for a dialog opened by open.
func (h *harness) pseudoDialog(name string, open func()) {
	h.t.Helper()
	h.dialogIn("en-XA", open, func() {
		for _, s := range h.untranslated() {
			if !objectsData(s) && !objectsChrome[s] {
				h.t.Errorf("%s: untranslated text %q", name, s)
			}
		}
	})
}

func (h *harness) zhDialog(open func(), texts ...string) {
	h.t.Helper()
	h.dialogIn("zh-Hans", open, func() { h.mustHave(texts...) })
}

// helpTip hovers the "?" whose tip is text and checks that the tip shows.
func (h *harness) helpTip(text string) {
	h.t.Helper()
	h.tt.Frame()
	r, ok := h.tt.Find(text)
	if !ok {
		h.t.Fatalf("no help icon with the tip %q", text)
	}
	h.tt.Move(r.X+r.W/2, r.Y+r.H/2)
	h.tt.Advance(700 * time.Millisecond)
	h.tt.Frame()
	n := 0
	for _, s := range h.tt.Texts() {
		if s == text {
			n++
		}
	}
	if n < 2 {
		h.t.Errorf("tip %q drawn %d times, want the label and the shown tip", text, n)
	}
	h.tt.Move(0, 0)
	h.tt.Frame()
}

func TestI18NObjects(t *testing.T) {
	h := newHarness(t, "light")
	h.chrome()

	// Inspector: overview and the folder summaries.
	h.pseudo("current folder")
	h.zh("当前文件夹", "总大小")
	h.mustClick("manifest.json")
	h.pseudo("overview")
	h.zh("概览", "存储类别", "修改时间", "复制对象键")
	// Headers of one object, with the help icons.
	h.mustClick("manifest.json")
	h.mustClick("Headers")
	h.pseudo("headers")
	h.zh("标头", "用户元数据", "添加元数据", "未设置")
	h.helpTip("Tells browsers and CDNs how long they may cache the object, for example max-age=3600.")
	h.helpTip("Custom key-value pairs stored with the object and sent back as x-amz-meta-* headers. Keys are saved in lower case, everything must be plain ASCII, and the total is limited to 2 KiB.")
	h.mustClick("Edit Cache-Control")
	h.typeAndEnter("max-age=60")
	h.mustClick("Add metadata")
	h.pseudo("headers editing")
	h.zh("还原", "保存", "撤销 Cache-Control")

	// Headers of several objects and the confirmation.
	h2 := newHarness(t, "light")
	h2.chrome()
	h2.mustClick("README.txt")
	h2.tt.ClickWith(ui.Shift, "manifest.json")
	h2.settle()
	h2.pseudo("multi selection")
	h2.zh("已选择多项", "总大小")
	h2.mustClick("Headers")
	h2.waitFor("fields loaded", func() bool { return h2.a.mg.props.ready })
	h2.pseudo("multi headers")
	h2.zh("各不相同", "未设置", "已选择多项")
	h2.helpTip("A change is applied to every selected object, and to everything inside selected folders. Fields where the objects differ keep each object's own value until you edit them.")
	h2.mustClick("Edit Cache-Control")
	h2.typeAndEnter("no-cache")
	h2.pseudoDialog("update confirmation", h2.a.saveProps)
	h2.zhDialog(h2.a.saveProps, "更新 2 项？", "更新 2 项", "设置 Cache-Control", "2 个对象", "将就地重写所选的每个对象")
	h2.mustClick("Save")
	h2.mustHave("Update 2 items?", "set Cache-Control")

	// Tags.
	h3 := newHarness(t, "light")
	h3.chrome()
	h3.mustClick("manifest.json")
	h3.mustClick("Tags")
	h3.waitFor("tags ready", func() bool { return h3.a.mg.tags.ready })
	h3.pseudo("tags")
	h3.zh("标签", "添加标签")
	h3.helpTip("Key-value labels on the object (up to 10) that lifecycle rules, cost reports and access policies can match. Keys starting with aws: are reserved.")
	h3.mustClick("Add tag")
	h3.typeAndEnter("aws:x")
	h3.typeAndEnter("v")
	h3.mustClick("Save")
	h3.pseudo("tags error")
	h3.zh("以 aws: 开头的键为保留键", "先修正标出的标签")

	// Permissions.
	h4 := newHarness(t, "light")
	h4.chrome()
	h4.mustClick("manifest.json")
	h4.mustClick("Permissions")
	h4.waitFor("acl loaded", func() bool { return h4.a.mg.acl.state == 2 })
	h4.pseudo("permissions")
	h4.zh("预设 ACL", "私有", "所有者", "完全控制", "授权")
	h4.helpTip("A canned ACL is a ready-made set of grants for this object. Private keeps it to the owner; the public options let anyone read it, or read and write it.")
	openPublic := func() { h4.a.chooseCanned("public-read") }
	h4.pseudoDialog("make public", openPublic)
	h4.zhDialog(openPublic, "将对象设为公开？", "设为公开", "任何知道对象地址的人都可以读取此对象。")

	// Preview: text, table, empty, too large, broken image.
	h5 := newHarness(t, "light")
	h5.chrome()
	h5.env.Srv.PutObject(bkt, "empty.txt", nil, "text/plain", nil)
	h5.env.Srv.PutObject(bkt, "huge.png", make([]byte, maxImagePreview+1), "image/png", nil)
	h5.env.Srv.PutObject(bkt, "bad.png", []byte("not a picture"), "image/png", nil)
	h5.previewOf("manifest.json")
	h5.pseudo("preview text")
	h5.zh("预览")
	for key, zh := range map[string]string{
		"empty.txt": "此文件为空",
		"huge.png":  "文件过大，无法预览（上限 8.0 MB）",
		"bad.png":   "无法显示此图片",
	} {
		h5.a.selectAfter = key
		h5.a.reload()
		h5.settle()
		h5.mustClick(key)
		h5.waitFor("preview of "+key, func() bool {
			return h5.a.prev.key == key && h5.a.prev.state != previewLoading && h5.a.prev.state != previewIdle
		})
		h5.pseudo("preview " + key)
		h5.zh(zh)
	}
	h5.a.openPrefix("reports/")
	h5.settle()
	h5.mustClick("customers-export.csv")
	h5.mustClick("Preview")
	h5.waitFor("csv", func() bool { return h5.a.prev.state == previewReady })
	h5.zh("预览")

	// Folder selected.
	h6 := newHarness(t, "light")
	h6.chrome()
	h6.mustClick("reports")
	h6.pseudo("folder overview")
	h6.zh("文件夹", "前缀")
}

func TestI18NObjectsVersions(t *testing.T) {
	h := newRawHarness(t, "light", true)
	h.openVersioned()
	h.chrome()
	h.mustClick("notes.txt")
	h.mustClick("Versions")
	h.waitFor("versions loaded", func() bool { return h.a.mg.vers.state == 2 })
	h.pseudo("versions")
	h.zh("版本", "最新", "恢复此版本")
	ask := func() {
		h.a.askDeleteVersions(h.a.scope, []core.ObjectRef{{Key: "notes.txt", VersionID: h.a.mg.vers.rows[1].VersionID}})
	}
	h.pseudoDialog("delete version confirmation", ask)
	h.zhDialog(ask, "永久删除此版本？", "将永久删除此版本，无法撤销。", "删除版本")
	h.a.mg.vers.sel = map[string]bool{h.a.mg.vers.rows[0].VersionID: true, h.a.mg.vers.rows[1].VersionID: true}
	h.settle()
	h.pseudo("versions selected")
	h.zh("已选择 2 个版本", "永久删除所选版本")

	// Deleted items.
	h.mustClick("Show deleted")
	h.waitFor("deleted listing", func() bool { return h.has("old-plan.txt") })
	h.mustClick("old-plan.txt")
	h.pseudo("deleted item")
	h.zh("已删除", "恢复")
	h.pseudoDialog("restore all", h.a.askRestoreAll)
	h.zhDialog(h.a.askRestoreAll, "恢复已删除的项？", "此文件夹下每个已删除的项都将恢复为最新版本。", "存储桶根目录")
}

func TestI18NObjectsSigned(t *testing.T) {
	h := newHarness(t, "light")
	h.chrome()
	h.openSignedFor("manifest.json")
	h.pseudo("signed options")
	h.zh("预签名 URL", "方法", "有效期", "15 分钟", "7 天", "自定义", "版本 ID（可选）", "生成")
	h.helpTip("How long the link keeps working, up to 7 days (the S3 limit). Anyone with the link can use it until then, and links signed with temporary credentials stop when those expire.")
	h.helpTip("GET lets anyone with the link download the object. PUT lets them upload a file to this key, replacing what is there; a PUT link cannot point to a version.")
	h.mustClick("Custom")
	h.a.objOps.sg.amount, h.a.objOps.sg.unit = "8", 2
	h.settle()
	h.pseudo("signed too long")
	h.zh("最长 7 天")
	h.a.objOps.sg.amount, h.a.objOps.sg.unit = "36", 1
	h.settle()
	h.mustClick("Generate")
	h.waitFor("result", func() bool { return len(h.a.objOps.sg.res) == 1 })
	h.pseudo("signed result")
	h.zh("复制公开 URL", "有效期 36 小时", "到期")

	h = newHarness(t, "light")
	h.chrome()
	h.openSignedFor("README.txt", "manifest.json")
	h.mustClick("Generate")
	h.waitFor("result", func() bool { return len(h.a.objOps.sg.res) == 2 })
	h.pseudo("signed many")
	h.zh("全部复制", "2 个对象")
}
