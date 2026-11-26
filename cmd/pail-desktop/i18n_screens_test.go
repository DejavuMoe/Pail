package main

import (
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/egoist/mygo/ui"
)

// Localization of each screen: it is shown under the en-XA pseudo locale, where
// any text that is not bracketed did not come from the catalog
// (expectTranslated), and then under Chinese.

// ---- shell ------------------------------------------------------------------

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
	// the command table (toolbar, menus, tool names)
	"Upload": true, "Download": true, "New folder": true, "Copy": true, "Cut": true, "Paste": true,
	"Rename": true, "Delete": true, "Refresh": true, "Toggle transfers": true, "Toggle inspector": true,
	"More": true, "Sync jobs": true, "Upload rules": true, "MIME types": true,
	"Multipart uploads": true, "Settings": true,
	// the inspector
	"Overview": true, "Headers": true, "Tags": true, "Permissions": true, "Versions": true, "Preview": true,
	"Multiple selection": true, "Folders": true, "Objects": true, "Total size": true,
	"Modified": true, "Folder": true, "JSON file": true, "TXT file": true, "BIN file": true,
	"2 items · 748 B": true, "Current folder": true, "Items": true, "Region": true, "Account": true,
	"Clear": true, "acme-assets/": true,
	// a toast said before the language changed stays as it was said
	"Uploaded 1 file": true,
}

// shellPlain is what may stay plain on the shell's screens: the texts of other
// files, sizes and counts written in digits, and the core's task titles.
var shellPlain = []plainOK{
	plainIn(shellForeignTexts),
	plainPrefix("→ s3://", "Counting", "Scan ", "found "),
	func(s string) bool { // a task title with a size in it
		return (strings.HasPrefix(s, "Upload ") || strings.HasPrefix(s, "Download ")) && strings.Contains(s, ".")
	},
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
	h.mustHave("BUCKETS", "TOOLS", "Name", "Storage class", "2 items selected", "748 B", "Completed 1", "Active 0", "Clear finished")
	h.mustNotHave("已选")

	// ---- pseudo locale: nothing of the shell stays English ----
	h.setLocale("en-XA")
	h.expectTranslated("main view", shellPlain...)
	h.a.transTab = 0
	h.settle()
	h.expectTranslated("transfers, active (empty)", shellPlain...)
	h.a.transTab = 2
	h.settle()
	h.expectTranslated("transfers, failed (empty)", shellPlain...)
	h.a.transTab = 1
	h.settle()
	h.expectTranslated("transfers, completed", shellPlain...)

	h.a.askDelete()
	h.settle()
	h.expectTranslated("delete dialog", shellPlain...)
	h.a.closeDelete()
	h.settle()

	h.a.openNewFolder()
	h.settle()
	h.expectTranslated("new folder dialog", shellPlain...)
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
		"已选 2 项", "748 B", "传输", "已完成 1", "进行中 0", "失败 0", "清除已结束项", "9 项", "平铺", "显示已删除")
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
		h.hover(label)
		if !h.tt.HasText(tip) {
			t.Errorf("hovering %q shows no tip %q", label, tip)
		}
		h.tt.Move(1, 1)
		h.tt.Advance(700 * time.Millisecond)
		h.tt.Frame()
	}
	h.setLocale("zh-Hans")
	h.hover("平铺")
	h.mustHave("平铺列出此文件夹下的所有对象（包括子文件夹中的），不显示文件夹。")
}

// ---- bucket dialogs ---------------------------------------------------------

// bucketPlain is what may stay plain in the bucket dialogs: bucket names and
// prefixes, encryption names, storage classes, and two texts of other files
// (the estimate summary of objops.go, the conflict choice of settings_view.go).
// The storage class labels are taken in the current locale.
func bucketPlain() []plainOK {
	set := map[string]bool{
		bkt: true, "my-bucket": true, "bad_name": true, "COLD": true,
		"AES-256 (SSE-S3)": true, "AWS KMS": true, "SSE-S3": true, "SSE-KMS": true, "SSE-C": true,
		"acme-new": true, "Keep both (rename)": true,
	}
	for _, id := range s3Classes {
		set[storageLabel(id)] = true
	}
	return []plainOK{
		plainIn(set),
		plainSub(bkt, " objects · ", "127.0.0.1:"), // the bucket, and the service the remote notice names
		func(s string) bool { return strings.HasSuffix(s, "/") },
	}
}

func bucketSectionIndex(cfg core.BucketConfig) int {
	for i, s := range bsSections {
		if s.cfg == cfg {
			return i
		}
	}
	return 0
}

func (h *harness) bucketOpenSection(cfg core.BucketConfig) {
	h.t.Helper()
	h.a.mg.bs.section = bucketSectionIndex(cfg)
	h.settle()
	h.waitFor("section read", func() bool { _, s := h.a.mg.bs.cur(); return s.state == loadReady || s.state == loadFailed })
	h.settle()
}

func (h *harness) bucketOpenRewrite(enc bool) {
	h.t.Helper()
	h.a.openRewrite(enc, rewriteScope{loc: h.a.bucketRoot(bkt), all: true})
	h.settle()
	h.waitFor("counted", func() bool { return h.a.objOps.rw.est.state == delReady })
	h.settle()
}

func TestI18NBucket(t *testing.T) {
	h := newHarness(t, "light")

	// 1. Nothing stays English under the pseudo locale.
	// The texts of the screens these dialogs draw over (sidebar, listing) are
	// not judged here.
	h.setLocale("en-XA")
	plain := append(bucketPlain(), plainIn(h.plainSet()))

	h.a.openBucketSettings(bkt)
	h.settle()
	h.bucketOpenSection(core.ConfigVersioning)
	h.expectTranslated("bucket settings, versioning", plain...)
	h.bucketOpenSection(core.ConfigPublicAccessBlock)
	h.expectTranslated("bucket settings, public access", plain...)
	h.bucketOpenSection(core.ConfigEncryption)
	h.a.mg.bs.secs[core.ConfigEncryption].sse = "aws:kms"
	h.settle()
	h.expectTranslated("bucket settings, encryption with KMS", plain...)
	h.bucketOpenSection(core.ConfigPolicy)
	h.expectTranslated("bucket settings, policy", plain...)
	if _, s := h.a.mg.bs.cur(); s.server == "" {
		s.draft, s.configuring = h.a.mg.bs.template(core.ConfigPolicy), true
		h.settle()
		h.expectTranslated("bucket settings, policy editor", plain...)
	}
	h.a.mg.bs.section = secDangerIndex()
	h.settle()
	h.expectTranslated("bucket settings, empty or delete", plain...)
	h.a.mg.bs.open = false
	h.settle()

	h.a.openNewBucket()
	h.settle()
	h.tt.Type("bad_name")
	h.settle()
	h.expectTranslated("new bucket dialog", plain...)
	h.a.mg.nb.open = false
	h.settle()

	h.a.openDeleteBucket(bkt)
	h.settle()
	h.expectTranslated("delete bucket dialog", plain...)
	h.a.mg.db.open = false
	h.settle()

	h.a.openEmptyBucket(bkt)
	h.waitFor("counted", func() bool { return h.a.mg.eb.state == delReady })
	h.settle()
	h.expectTranslated("empty bucket dialog", plain...)
	h.a.mg.eb.open = false
	h.settle()

	h.bucketOpenRewrite(false)
	h.expectTranslated("storage class dialog", plain...)
	h.a.objOps.rw.class = customClass
	h.a.objOps.rw.custom = "cold!"
	h.settle()
	h.expectTranslated("storage class dialog, custom class", plain...)
	h.a.closeRewrite()
	h.settle()

	h.bucketOpenRewrite(true)
	h.expectTranslated("encryption dialog", plain...)
	h.a.objOps.rw.mode = 2
	h.settle()
	h.expectTranslated("encryption dialog, KMS", plain...)
	h.a.objOps.rw.mode = encSSEC
	h.settle()
	h.expectTranslated("encryption dialog, SSE-C", plain...)
	h.a.closeRewrite()
	h.settle()

	h.a.openBucketCopy(bkt, true)
	h.settle()
	h.expectTranslated("move contents dialog", plain...)
	h.a.objOps.bc.confirming = true
	h.settle()
	h.expectTranslated("move contents confirmation", plain...)
	h.a.closeBucketCopy()
	h.settle()

	// 2. Chinese texts.
	h.setLocale("zh-Hans")
	h.a.openBucketSettings(bkt)
	h.settle()
	h.bucketOpenSection(core.ConfigVersioning)
	h.mustHave("存储桶设置", "版本控制", "公共访问", "对象所有权", "传输加速", "关闭")
	h.bucketOpenSection(core.ConfigPublicAccessBlock)
	h.mustHave("阻止公共 ACL", "忽略公共 ACL", "限制公共存储桶")
	h.a.mg.bs.open = false
	h.settle()

	h.a.openNewBucket()
	h.settle()
	h.tt.Type("bad_name")
	h.settle()
	h.mustHave("新建存储桶", "名称", "区域", "对象锁定", "创建", "取消", "只能使用小写字母、数字、句点和连字符")
	h.a.mg.nb.open = false
	h.settle()

	h.a.openDeleteBucket(bkt)
	h.settle()
	h.mustHave("删除存储桶？", "输入存储桶名称以确认", "远端操作：修改的是存储服务，不是 Pail 的设置", "存储桶 "+bkt+" 会在存储服务上被删除")
	h.a.mg.db.open = false
	h.settle()

	h.a.openEmptyBucket(bkt)
	h.waitFor("counted", func() bool { return h.a.mg.eb.state == delReady })
	h.settle()
	h.mustHave("清空存储桶？", "个对象")
	h.a.mg.eb.open = false
	h.settle()

	h.bucketOpenRewrite(false)
	h.mustHave("更改存储类别", "范围", "整个存储桶", "存储类别", "应用", "取消")
	h.a.closeRewrite()
	h.settle()

	h.bucketOpenRewrite(true)
	h.mustHave("更改加密", "加密", "无", "SSE-C 需要 https 端点")
	h.a.closeRewrite()
	h.settle()

	// Plural messages: one object, five thousand objects.
	if got := (&emptyBucketForm{state: delReady, est: core.Estimate{Objects: 1, Bytes: 10}}).summary(); got != "1 个对象 · 10 B" {
		t.Errorf("zh summary = %q", got)
	}

	// 3. Help icons, in English.
	h.setLocale("en")
	if got := (&emptyBucketForm{state: delReady, est: core.Estimate{Objects: 5000, Bytes: 10, Truncated: true}}).summary(); got != "at least 5,000 objects · 10 B" {
		t.Errorf("en summary = %q", got)
	}
	h.a.openBucketSettings(bkt)
	h.settle()
	h.bucketOpenSection(core.ConfigVersioning)
	h.hoverHelp(bsSections[bucketSectionIndex(core.ConfigVersioning)].help())
	h.bucketOpenSection(core.ConfigPublicAccessBlock)
	h.hoverHelp(pabHelps()[0])
	h.a.mg.bs.open = false
	h.settle()

	h.a.openNewBucket()
	h.settle()
	h.hoverHelp("Creates the bucket with Object Lock on, which also turns versioning on. It can only be chosen here, when the bucket is created.")
	h.a.mg.nb.open = false
	h.settle()

	h.bucketOpenRewrite(false)
	h.hoverHelp("Colder classes cost less to store but more to read, and archived objects must be restored before they can be read. Each object is copied onto itself to change its class, so archived objects that are not restored and SSE-C objects fail individually.")
	h.a.closeRewrite()
	h.settle()

	h.bucketOpenRewrite(true)
	h.hoverHelp("SSE-S3 (AES256): S3 manages the keys. SSE-KMS: an AWS KMS key encrypts the object. SSE-C: the key is supplied by the user, needs an https endpoint, and the object can only be read with that key. None drops the explicit setting; a bucket default may still apply.")
	h.mustHave("SSE-C needs an https endpoint")
	h.a.closeRewrite()
	h.settle()
}

// ---- objects and the inspector ----------------------------------------------

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

// objectsStatus matches the status bar text of the shell ("1 selected · 1.0 KB").
var objectsStatus = regexp.MustCompile(`^([\d,]+ (selected|items?)\b|[A-Za-z0-9_.-]{8}|[a-z0-9/-]+/)$|^[\d,]+ (selected|items?)\b`)

// objectsChrome holds the texts around the screens under test that this suite
// does not own (sidebar, toolbar, table, status bar): they are taken once per
// test, with the inspector closed and no dialog open, and ignored afterwards.
var objectsChrome = map[string]bool{}

// objectsPlain is what may stay plain on the object screens.
var objectsPlain = []plainOK{
	plainIn(objectsAllowedExact), plainMatch(objectsStatus), plainSub(objectsAllowedSub...), plainIn(objectsChrome),
}

func (h *harness) chrome() {
	h.t.Helper()
	was := h.a.inspectorOpen
	h.a.inspectorOpen = false
	h.settle()
	h.setLocale("en-XA")
	maps.Copy(objectsChrome, h.plainSet())
	h.setLocale("en")
	h.a.inspectorOpen = was
	h.settle()
}

// pseudo shows the screen in the en-XA pseudo locale and fails on every text
// that stays plain English and is not data or chrome of another area.
func (h *harness) pseudo(name string) {
	h.t.Helper()
	h.setLocale("en-XA")
	h.expectTranslated(name, objectsPlain...)
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
	h.dialogIn("en-XA", open, func() { h.expectTranslated(name, objectsPlain...) })
}

func (h *harness) zhDialog(open func(), texts ...string) {
	h.t.Helper()
	h.dialogIn("zh-Hans", open, func() { h.mustHave(texts...) })
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
	h.hoverHelp("Tells browsers and CDNs how long they may cache the object, for example max-age=3600.")
	h.hoverHelp("Custom key-value pairs stored with the object and sent back as x-amz-meta-* headers. Keys are saved in lower case, everything must be plain ASCII, and the total is limited to 2 KiB.")
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
	h2.hoverHelp("A change is applied to every selected object, and to everything inside selected folders. Fields where the objects differ keep each object's own value until you edit them.")
	h2.mustClick("Edit Cache-Control")
	h2.typeAndEnter("no-cache")
	h2.pseudo("multi headers, edited")
	h2.zh("更新 2 项")
	h2.mustHave("Update 2 items")

	// Tags.
	h3 := newHarness(t, "light")
	h3.chrome()
	h3.mustClick("manifest.json")
	h3.mustClick("Tags")
	h3.waitFor("tags ready", func() bool { return h3.a.mg.tags.ready })
	h3.pseudo("tags")
	h3.zh("标签", "添加标签")
	h3.hoverHelp("Key-value labels on the object (up to 10) that lifecycle rules, cost reports and access policies can match. Keys starting with aws: are reserved.")
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
	h4.waitFor("acl loaded", func() bool { return h4.a.mg.acl.state == loadReady })
	h4.pseudo("permissions")
	h4.zh("预设 ACL", "私有", "所有者", "完全控制", "授权")
	h4.hoverHelp("A canned ACL is a ready-made set of grants for this object. Private keeps it to the owner; the public options let anyone read it, or read and write it.")
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
	h.waitFor("versions loaded", func() bool { return h.a.mg.vers.state == loadReady })
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
}

func TestI18NObjectsSigned(t *testing.T) {
	h := newHarness(t, "light")
	h.chrome()
	h.openSignedFor("manifest.json")
	h.pseudo("signed options")
	h.zh("预签名 URL", "方法", "有效期", "15 分钟", "7 天", "自定义", "版本 ID（可选）", "生成")
	h.hoverHelp("How long the link keeps working, up to 7 days (the S3 limit). Anyone with the link can use it until then, and links signed with temporary credentials stop when those expire.")
	h.hoverHelp("GET lets anyone with the link download the object. PUT lets them upload a file to this key, replacing what is there; a PUT link cannot point to a version.")
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

// ---- account dialog, Settings and vault dialogs -----------------------------

// Plain texts that legitimately stay as they are under the en-XA pseudo
// locale: product names, code values, example placeholders and user data.
var settingsI18NAllow = map[string]bool{
	"AWS":                              true,
	"us-east-1":                        true,
	"AKIA…":                            true,
	"default":                          true,
	"Pail":                             true,
	"https://s3.example.com":           true,
	"https://cdn.example.com":          true,
	"arn:aws:iam::123456789012:role/…": true,
	"(Argon2id, XChaCha20-Poly1305)":   true,
	"RESET":                            true,
	"Windows Credential Manager":       true,
	"macOS Keychain":                   true,
	"Secret Service":                   true,
	"Demo account":                     true,
	"acme-assets":                      true,
	"One":                              true, // account name (user data)
	"ref1":                             true, // secret reference name (user data)
	"Pail demo":                        true,
	"dev":                              true, // the build version
	"Dejavu Moe":                       true, // the author, a name
	"github.com/DejavuMoe/Pail":        true, // the repository address
	"MiB":                              true, // units: the same in every language
	"KiB/s":                            true,
}

// clickSettingSwitch clicks the switch of a Settings row: it is the last
// control of the row, 36px wide.
func (h *harness) clickSettingSwitch(label string) {
	h.t.Helper()
	x, y := h.settingRowEnd(label)
	h.tt.ClickAt(x-18, y)
	h.settle()
}

func TestI18NSettings(t *testing.T) {
	h := newHarness(t, "light")

	// The window around the screens of this file (sidebar, tool header, status
	// bar, the object browser) belongs to other files: what stays plain there
	// is not judged here. It is measured on the browser and on another tool.
	h.setLocale("en-XA")
	background := h.plainSet()
	h.setLocale("en")
	h.openToolView("mime")
	h.setLocale("en-XA")
	maps.Copy(background, h.plainSet())
	h.setLocale("en")
	h.a.closeTool()
	h.settle()

	// pseudo checks that no plain English is left on the screen opened by open.
	pseudo := func(name string, open func(), closeIt func()) {
		t.Helper()
		h.setLocale("en")
		open()
		h.settle()
		h.setLocale("en-XA")
		h.expectTranslated(name, plainIn(settingsI18NAllow), plainIn(background))
		closeIt()
		h.setLocale("en")
	}

	// ---- account dialog ----------------------------------------------------------------
	pseudo("account dialog (AWS, advanced)", func() {
		h.a.openAccountForm(false)
		h.a.af.advanced = true
		h.a.af.tried = true // show every error line
	}, func() { h.a.af.open = false })
	pseudo("account dialog (S3-compatible, advanced)", func() {
		h.a.openAccountForm(false)
		h.a.af.provider = 1
		h.a.af.advanced = true
		h.a.af.tried = true
		h.a.af.cdnURL = "ftp://cdn"
	}, func() { h.a.af.open = false })
	pseudo("account dialog (profile)", func() {
		h.a.openAccountForm(false)
		h.a.af.creds = "profile"
		h.a.af.tried = true
	}, func() { h.a.af.open = false })

	// ---- Settings view -----------------------------------------------------------------
	pseudo("settings (keychain)", func() { h.openToolView("settings") }, func() {})
	h.toVault()
	pseudo("settings (vault)", func() { h.openToolView("settings") }, func() {})
	h.a.sf.errs["upload"] = "x"
	h.lockNow()
	pseudo("settings (vault locked)", func() { h.openToolView("settings") }, func() {})
	h.a.askUnlock("t", nil)
	h.settle()
	pseudo("unlock dialog", func() { h.a.vu.unlock.err = "x"; h.a.vu.unlock.open = true }, func() { h.a.closeUnlock() })

	// ---- vault dialogs -----------------------------------------------------------------
	pseudo("create vault dialog", func() {
		h.a.vu.create = pwDialog{open: true, tried: true, pw: "short", pw2: "other"}
	}, func() { h.a.vu.create = pwDialog{} })
	pseudo("existing vault dialog", func() {
		h.a.vu.create = pwDialog{open: true, existing: true, tried: true}
	}, func() { h.a.vu.create = pwDialog{} })
	pseudo("keychain dialog", func() {
		h.a.vu.toKeychain = pwDialog{open: true, needPW: true}
	}, func() { h.a.vu.toKeychain = pwDialog{} })
	pseudo("change password dialog", func() {
		h.a.vu.change = pwDialog{open: true, tried: true}
	}, func() { h.a.vu.change = pwDialog{} })
	pseudo("reset dialog", func() {
		h.a.vu.reset = resetState{open: true, accounts: []string{"One"}, refs: []string{"ref1"}}
	}, func() { h.a.vu.reset = resetState{} })
	pseudo("secrets dialog", func() { h.a.openSecrets() }, func() { h.a.tl.secrets.open = false })

	// ---- Chinese -----------------------------------------------------------------------
	h.setLocale("zh-Hans")
	h.a.openAccountForm(false)
	h.settle()
	h.mustHave("添加账户", "服务商", "测试连接", "保存", "取消", "凭证", "高级")
	h.a.af.provider = 1
	h.a.af.advanced = true
	h.settle()
	h.mustHave("端点", "路径样式访问", "角色 ARN", "外部 ID", "会话名称", "校验和模式", "跳过 TLS 验证")
	h.a.af.open = false

	h.openToolView("settings")
	h.mustHave("外观", "主题", "语言", "传输", "并发上传数", "文件已存在时", "默认下载文件夹", "写入诊断日志", "安全", "凭证存储", "自动锁定", "15 分钟")
	h.a.vu.create = pwDialog{open: true, tried: true}
	h.settle()
	h.mustHave("创建主密码", "确认密码", "创建加密保险库", "至少使用 8 个字符", "再次输入密码")
	h.a.vu.create = pwDialog{}
	h.a.askUnlock("t", nil)
	h.settle()
	h.mustHave("解锁加密保险库", "主密码", "解锁")
	h.a.closeUnlock()
	h.a.vu.reset = resetState{open: true}
	h.settle()
	h.mustHave("重置加密保险库？", "输入 RESET 以确认", "此操作无法撤销。云端的存储桶和对象不受影响。")
	h.a.vu.reset = resetState{}
	h.setLocale("en")

	// ---- help icons --------------------------------------------------------------------
	h.a.openAccountForm(false)
	h.a.af.provider = 1
	h.a.af.advanced = true
	h.settle()
	h.hoverHelp("Addresses objects as endpoint/bucket/object. MinIO and most self-hosted gateways need this on; keep it off for AWS.")
	h.hoverHelp("Optional. The address your CDN serves objects from. A base URL such as https://cdn.example.com gives links like base/bucket/key. For a custom link, write a template with {bucket} and {key}; a template that uses {bucket} needs {key} as well.")
	// The TLS row is below the dialog's fold, so its icon cannot be hovered:
	// only its label is checked.
	h.mustHave("Turns off certificate checks for this connection, so a fake server could read your data and credentials. Use it only for test servers you trust, such as a local MinIO with a self-signed certificate.")
	h.a.af.open = false
	h.openToolView("settings")
	h.hoverHelp("Follow system uses your operating system's language and falls back to English. The choice is remembered.")
	h.hoverHelp("How many upload tasks run at the same time; the rest wait in the queue. Lowering it never interrupts a running task. Allowed: 1 to 64.")
	h.mustHave("Language", "Follow system")

	// ---- the Language setting switches the visible texts at once -----------------------
	h.a.setLanguage("zh-Hans")
	h.settle()
	h.mustHave("外观", "语言", "简体中文")
	h.mustNotHave("Appearance")
	if h.a.lang != "zh-Hans" {
		t.Fatalf("lang = %q", h.a.lang)
	}
	h.a.setLanguage("")
	h.settle()
	h.mustHave("Appearance", "Language", "Follow system")
	h.mustNotHave("外观")
}

// ---- tools ------------------------------------------------------------------

// The five tool views (upload rules, sync jobs, CloudFront, MIME types,
// multipart uploads), the sync job editor and the invalidation dialog.

// toolsPlainOK is what may stay plain under en-XA: user data (names, keys,
// paths), S3 and HTTP values that are shown as code, product names, and the
// text of the shell around the tools, which the files that own it localize.
var toolsPlainOK = []*regexp.Regexp{
	regexp.MustCompile(`^[\w./*-]+\.\w+$`),                            // file names and paths: a.txt, sub/c.txt
	regexp.MustCompile(`^(acme-[\w-]+|[\w-]+/|[\w./-]+\*)$`),          // bucket names, prefixes, masks
	regexp.MustCompile(`^(Cache-Control|Content-[A-Za-z]+|Expires)$`), // header names
	regexp.MustCompile(`^(STANDARD|STANDARD_IA|AES256|aws:kms|SSE-C|GZIP|ZIP|AES-256 ZIP|ZIP AES-256)$`),
	regexp.MustCompile(`^(Cache-Control: .*|SSE .*|GZIP .*|ZIP .*|.*STANDARD_IA.*)$`), // an evaluated rule result
	regexp.MustCompile(`^(Production AWS|Demo.*|E[12][A-Z]{3}|d[12]\.cloudfront\.net|cdn\.acme\.test|Deployed|InProgress|Completed|IOLD1|INEW1)$`),
	regexp.MustCompile(`^(\.ext|type/subtype|application/[\w.+-]+|text/plain)$`), // placeholders and content types
	regexp.MustCompile(`^(Site|Site 2|Images|abc|bad)$`),
	regexp.MustCompile(`^[\w./*-]+ / [\w./*-]+$`),                                      // a rule mask: acme-* / *.png
	regexp.MustCompile(`^.+ ↔ s3://`),                                                  // a job row: folder ↔ s3://bucket/prefix
	regexp.MustCompile(`^[A-Za-z]:\\`),                                                 // a local path on Windows
	regexp.MustCompile(`^[\w-]{10}…$`),                                                 // a shortened upload ID
	regexp.MustCompile(`^rule \d+ \(.*\): `),                                           // a rule problem from the application layer, kept verbatim
	regexp.MustCompile(`^\d+ active · `),                                               // the transfers bar
	regexp.MustCompile(`^` + regexp.QuoteMeta(filepath.Clean(os.TempDir())) + `[/\\]`), // temporary folder paths are user data
	regexp.MustCompile(`^(Account|BUCKETS|TOOLS|New bucket|Filter buckets|Sync jobs|Upload rules|MIME types|Multipart uploads|Settings|Tool|Back|More|Toggle transfers|Transfers|Cancel|Standard|Standard-IA)$`),
}

// mustBeTranslated shows the screen under en-XA (after act, which runs under
// that locale so that texts kept in state are said in it) and fails on any
// text that is neither bracketed nor user data.
func (h *harness) mustBeTranslated(screen string, act ...func()) {
	h.t.Helper()
	h.setLocale("en-XA")
	for _, f := range act {
		f()
	}
	h.settle()
	h.expectTranslated(screen, plainMatch(toolsPlainOK...))
	h.setLocale("en")
}

func TestI18NTools(t *testing.T) {
	t.Run("rules", func(t *testing.T) {
		h := newHarness(t, "light")
		h.openToolView("rules")
		rs := &h.a.tl.rules
		h.mustBeTranslated("rules, empty")

		h.a.rulesAdd()
		rs.form.name, rs.form.bucket, rs.form.key = "Images", "acme-*", "*.png"
		rs.form.headers = []ruleKV{{"Cache-Control", "max-age=60"}}
		rs.form.meta = []ruleKV{{"owner", "web"}}
		rs.form.class = "STANDARD_IA"
		rs.form.enc, rs.form.comp, rs.form.level = "aws:kms", "zipaes", "-2"
		h.tt.Frame()
		rs.testBucket, rs.testKey = "acme-assets", "hero.png"
		h.tt.Frame()
		h.mustBeTranslated("rules, KMS and AES-256 ZIP")
		rs.form.enc = "SSE-C"
		h.tt.Frame()
		h.mustBeTranslated("rules, SSE-C")
		rs.form.enc = ""
		rs.form.headers = []ruleKV{{"Expires", "tomorrow"}} // the problem line
		h.tt.Frame()
		h.mustBeTranslated("rules, a problem")

		h.setLocale("zh-Hans")
		h.mustHave("添加规则", "保存", "标头", "元数据", "存储类别", "加密", "压缩", "管理机密…", "测试", "含未保存修改",
			"规则 0（Images）：Expires 必须是 HTTP 日期，例如 Wed, 21 Oct 2026 07:28:00 GMT")
		h.setLocale("en")
		h.hoverHelp(locales.RulesKeyMaskHelp())
		h.hoverHelp(locales.RulesOrderHelp())
		h.hoverHelp(locales.RulesCompressionHelp())
	})

	t.Run("mime", func(t *testing.T) {
		h := newHarness(t, "light")
		if err := h.a.svc.SaveMIMETypes(map[string]string{".xyz": "application/x-xyz"}); err != nil {
			t.Fatal(err)
		}
		h.openToolView("mime")
		m := &h.a.tl.mime
		m.addExt, m.addType = "abc", "bad"
		m.testName = "a.xyz"
		h.tt.Frame()
		h.mustBeTranslated("mime, add error", h.a.mimeAdd)
		m.rows = append(m.rows, mimeRow{".xyz", "x/y"}) // a duplicate
		h.mustBeTranslated("mime, duplicate", h.a.mimeSave)

		h.setLocale("zh-Hans")
		h.mustHave("扩展名", "内容类型", "添加", "测试", "含未保存修改", "保存")
		h.setLocale("en")
		h.hoverHelp(locales.MimeContentTypeHelp())
		h.hoverHelp(locales.MimeTestHelp())
	})

	t.Run("multipart", func(t *testing.T) {
		h := newHarness(t, "light")
		h.openToolView("multipart")
		m := &h.a.tl.mp
		h.mustBeTranslated("multipart, empty")
		startMultipart(t, h, "big/one.bin")
		startMultipart(t, h, "big/two.bin")
		h.a.mpLoad()
		h.waitFor("uploads listed", func() bool { return len(m.items) == 2 })
		h.settle()
		for _, u := range m.items {
			m.sel.Add(u.UploadID)
		}
		h.tt.Frame()
		h.mustBeTranslated("multipart, list")
		h.mustBeTranslated("multipart, abort dialog", h.a.mpAskAbort)
		h.a.confirm.open = false

		h.setLocale("zh-Hans")
		h.a.mpAskAbort()
		h.settle()
		if want := "中止 2 个上传？"; h.a.confirm.title != want {
			t.Errorf("abort title = %q, want %q", h.a.confirm.title, want)
		}
		if want := "将丢弃 2 个未完成的上传及已上传的分段，无法撤销。"; h.a.confirm.message != want {
			t.Errorf("abort message = %q, want %q", h.a.confirm.message, want)
		}
		h.mustHave("中止 2 个上传？", "存储桶", "前缀")
		h.a.confirm.open = false
		h.settle()
		h.mustHave("已选 2 项", "中止…", "开始时间", "上传 ID", "存储类别", "发起者")
		h.setLocale("en")
		h.hoverHelp(locales.MultipartHelp())
	})

	t.Run("cloudfront", func(t *testing.T) {
		h := newHarness(t, "light")
		fake := &fakeCF{}
		h.a.tl.cf.api = fake
		h.a.accounts = []Account{{ID: "aws1", Name: "Production AWS", Provider: core.ProviderAWS}}
		h.a.accountID = "aws1"
		h.a.scope = Scope{AccountID: "aws1", Bucket: "acme-assets"}
		h.openToolView("cloudfront")
		cf := &h.a.tl.cf
		h.waitFor("distributions", func() bool { return len(cf.invs) == 1 })
		h.settle()
		h.mustBeTranslated("cloudfront")

		h.a.cfOpenDialog("/a.txt\n/dir/*")
		h.waitFor("whole-bucket helper", func() bool { return cf.dlg.forBucket["E1ABC"] })
		h.settle()
		h.mustBeTranslated("invalidation dialog")
		h.setLocale("zh-Hans")
		h.mustHave("新建失效", "分配", "路径", "使整个存储桶失效", "取消", "创建失效", "Production AWS")
		h.setLocale("en")
		h.hoverHelp(locales.CloudfrontPathsHelp())

		h.a.cfSubmit()
		h.waitFor("result", func() bool { return cf.dlg.result != nil })
		h.settle()
		h.setLocale("zh-Hans")
		h.mustHave("失效 INEW1 · InProgress", "关闭")
		cf.dlg.open = false
		h.settle()
		h.mustHave("域名", "别名", "状态", "已启用", "已禁用", "刷新", "新建失效…")
		h.setLocale("en")
		h.hoverHelp(locales.CloudfrontInvalidationsHelp())
	})

	t.Run("cloudfront-non-aws", func(t *testing.T) {
		h := newHarness(t, "light")
		h.openToolView("cloudfront")
		h.mustBeTranslated("cloudfront, not AWS")
		h.setLocale("zh-Hans")
		h.mustHave("CloudFront 仅适用于 AWS 账户")
	})

	t.Run("sync", func(t *testing.T) {
		h := newHarness(t, "light")
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "a.txt"), []byte("alpha"), 0o600)
		os.WriteFile(filepath.Join(dir, "skip.tmp"), []byte("junk"), 0o600)
		h.openToolView("sync")
		h.mustBeTranslated("sync, no jobs")
		h.setLocale("zh-Hans")
		h.mustHave("没有同步任务", "让本地文件夹与存储桶保持一致。", "新建任务")
		h.setLocale("en")

		if _, err := h.a.svc.SaveSyncJob(core.SyncJob{Name: "Site", LocalDir: dir, Direction: "upload", Mode: "mirror",
			Remote: Scope{AccountID: h.env.Account.ID, Bucket: "acme-website", Prefix: "sync-it/"}, Excludes: []string{"*.tmp"}}); err != nil {
			t.Fatal(err)
		}
		h.env.Srv.PutObject("acme-website", "sync-it/extra.txt", []byte("stale"), "text/plain", nil)
		h.a.closeTool()
		h.openToolView("sync")
		s := &h.a.tl.sync
		h.mustBeTranslated("sync, editor")

		h.a.syncPreview()
		h.waitFor("preview", func() bool { return s.pv != nil })
		h.settle()
		if s.pv.Uploads != 1 || s.pv.Deletes != 1 || s.pv.Skips == 0 {
			t.Fatalf("preview = %+v", s.pv)
		}
		h.mustBeTranslated("sync, preview")
		h.mustBeTranslated("sync, run dialog", h.a.syncAskRun)
		h.a.confirm.open = false

		h.setLocale("zh-Hans")
		h.mustHave("1 个待上传", "1 个待删除", "已跳过", "全部", "变更", "删除", "路径", "原因", "大小", "a.txt", "extra.txt", "新增", "源中不存在", "已排除")
		h.mustHave("保存", "预览", "运行同步", "方向", "本地文件夹", "存储桶", "前缀", "模式", "比较方式", "排除", "会删除目标位置多出的文件")
		h.a.syncAskRun()
		h.settle()
		for i, w := range []KV{{"任务", "Site"}, {"本地", dir}, {"远程", "s3://acme-website/sync-it/"}, {"上传", "1 个文件"}, {"下载", "0 个文件"}, {"删除", "将在目标位置删除 1 个文件"}} {
			if h.a.confirm.rows[i] != w {
				t.Errorf("confirm row %d = %+v, want %+v", i, h.a.confirm.rows[i], w)
			}
		}
		if h.a.confirm.title != "运行同步？" || !h.a.confirm.danger {
			t.Errorf("confirm = %+v", h.a.confirm)
		}
		h.mustHave("运行同步？", "将在目标位置删除 1 个文件")
		h.a.confirm.open = false
		h.settle()

		// The discard dialog.
		s.f.name = "Site 2"
		h.tt.Frame()
		h.a.syncGuard(h.a.syncNew)
		h.settle()
		h.mustHave("放弃对 Site 2 的更改？", "放弃", "取消", "保存")
		h.mustBeTranslated("sync, discard dialog")
		s.dsc.open = false
		h.settle()

		h.hoverHelp(locales.SyncExcludesHelp())
		h.hoverHelp(locales.SyncModeHelp(trashName()))
		h.hoverHelp(locales.SyncCompareHelp())
		h.hoverHelp(locales.SyncPreviewHelp())
	})
}
