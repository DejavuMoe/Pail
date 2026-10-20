package main

import (
	"strings"
	"testing"
	"time"

	core "github.com/DejavuMoe/Pail/internal/app"
)

// bucketBaseline is the set of plain-English texts the app shows under en-XA
// before one of the bucket dialogs is open: other screens' strings (sidebar,
// listing) that these dialogs only draw over.
func bucketBaseline(h *harness) map[string]bool {
	base := map[string]bool{}
	for _, s := range h.untranslated() {
		base[s] = true
	}
	return base
}

// bucketCheckPlain fails on every visible text of the open dialog that stays
// plain English under en-XA, except the baseline, user data and S3 values.
func bucketCheckPlain(t *testing.T, h *harness, base map[string]bool, where string) {
	t.Helper()
	allow := map[string]bool{
		bkt: true, "my-bucket": true, "us-east-1": true, "bad_name": true, "COLD": true,
		"AES-256 (SSE-S3)": true, "AWS KMS": true, "SSE-S3": true, "SSE-KMS": true, "SSE-C": true,
		"acme-new": true, demoAccountName: true,
	}
	for _, id := range s3Classes { // agent 1 localizes storageLabel
		allow[storageLabel(id)] = true
	}
	var left []string
	for _, s := range h.untranslated() {
		if base[s] || allow[s] || strings.Contains(s, bkt) || strings.HasPrefix(s, "s3://") || strings.HasSuffix(s, "/") ||
			strings.Contains(s, " objects · ") || // estState.summary, objops.go (not this file group)
			s == "Keep both (rename)" { // conflictChoices, settings_view.go (not this file group)
			continue
		}
		left = append(left, s)
	}
	if len(left) > 0 {
		t.Errorf("%s: texts that stay English under en-XA: %q", where, left)
	}
}

func bucketCount(h *harness, text string) int {
	n := 0
	for _, s := range h.tt.Texts() {
		if strings.TrimSpace(s) == text {
			n++
		}
	}
	return n
}

// bucketHoverHelp hovers the "?" whose accessible label is help and checks
// that the tip appears next to the label.
func bucketHoverHelp(t *testing.T, h *harness, help string) {
	t.Helper()
	h.tt.Frame()
	r, ok := h.tt.Find(help)
	if !ok {
		t.Fatalf("no help icon with the label %q; have %q", help, h.tt.Texts())
	}
	h.tt.Move(r.X+r.W/2, r.Y+r.H/2)
	h.tt.Advance(700 * time.Millisecond)
	h.tt.Frame()
	h.mustHave(help)
	if n := bucketCount(h, help); n < 2 {
		t.Errorf("the tip of %q is not shown: %d copies of the text", help, n)
	}
	h.tt.Move(1, 1)
	h.tt.Frame()
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
	h.waitFor("section read", func() bool { _, s := h.a.mg.bs.cur(); return s.state == 2 || s.state == 3 })
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
	h.setLocale("en-XA")
	base := bucketBaseline(h)

	h.a.openBucketSettings(bkt)
	h.settle()
	h.bucketOpenSection(core.ConfigVersioning)
	bucketCheckPlain(t, h, base, "bucket settings, versioning")
	h.bucketOpenSection(core.ConfigPublicAccessBlock)
	bucketCheckPlain(t, h, base, "bucket settings, public access")
	h.bucketOpenSection(core.ConfigEncryption)
	h.a.mg.bs.secs[core.ConfigEncryption].sse = "aws:kms"
	h.settle()
	bucketCheckPlain(t, h, base, "bucket settings, encryption with KMS")
	h.bucketOpenSection(core.ConfigPolicy)
	bucketCheckPlain(t, h, base, "bucket settings, policy")
	if _, s := h.a.mg.bs.cur(); s.server == "" {
		s.draft, s.configuring = h.a.mg.bs.template(core.ConfigPolicy), true
		h.settle()
		bucketCheckPlain(t, h, base, "bucket settings, policy editor")
	}
	h.a.mg.bs.open = false
	h.settle()

	h.a.openNewBucket()
	h.settle()
	h.tt.Type("bad_name")
	h.settle()
	bucketCheckPlain(t, h, base, "new bucket dialog")
	h.a.mg.nb.open = false
	h.settle()

	h.a.openDeleteBucket(bkt)
	h.settle()
	bucketCheckPlain(t, h, base, "delete bucket dialog")
	h.a.mg.db.open = false
	h.settle()

	h.a.openEmptyBucket(bkt)
	h.waitFor("counted", func() bool { return h.a.mg.eb.state == delReady })
	h.settle()
	bucketCheckPlain(t, h, base, "empty bucket dialog")
	h.a.mg.eb.open = false
	h.settle()

	h.bucketOpenRewrite(false)
	bucketCheckPlain(t, h, base, "storage class dialog")
	h.a.objOps.rw.class = customClass
	h.a.objOps.rw.custom = "cold!"
	h.settle()
	bucketCheckPlain(t, h, base, "storage class dialog, custom class")
	h.a.closeRewrite()
	h.settle()

	h.bucketOpenRewrite(true)
	bucketCheckPlain(t, h, base, "encryption dialog")
	h.a.objOps.rw.mode = 2
	h.settle()
	bucketCheckPlain(t, h, base, "encryption dialog, KMS")
	h.a.objOps.rw.mode = encSSEC
	h.settle()
	bucketCheckPlain(t, h, base, "encryption dialog, SSE-C")
	h.a.closeRewrite()
	h.settle()

	h.a.openBucketCopy(bkt, true)
	h.settle()
	bucketCheckPlain(t, h, base, "move contents dialog")
	h.a.objOps.bc.confirming = true
	h.settle()
	bucketCheckPlain(t, h, base, "move contents confirmation")
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
	h.mustHave("删除存储桶？", "输入存储桶名称以确认", "将永久删除存储桶 "+bkt+"，无法撤销。")
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
	bucketHoverHelp(t, h, bsSections[bucketSectionIndex(core.ConfigVersioning)].help())
	h.bucketOpenSection(core.ConfigPublicAccessBlock)
	bucketHoverHelp(t, h, pabHelps()[0])
	h.a.mg.bs.open = false
	h.settle()

	h.a.openNewBucket()
	h.settle()
	bucketHoverHelp(t, h, "Creates the bucket with Object Lock on, which also turns versioning on. It can only be chosen here, when the bucket is created.")
	h.a.mg.nb.open = false
	h.settle()

	h.bucketOpenRewrite(false)
	bucketHoverHelp(t, h, "Colder classes cost less to store but more to read, and archived objects must be restored before they can be read. Each object is copied onto itself to change its class, so archived objects that are not restored and SSE-C objects fail individually.")
	h.a.closeRewrite()
	h.settle()

	h.bucketOpenRewrite(true)
	bucketHoverHelp(t, h, "SSE-S3 (AES256): S3 manages the keys. SSE-KMS: an AWS KMS key encrypts the object. SSE-C: the key is supplied by the user, needs an https endpoint, and the object can only be read with that key. None drops the explicit setting; a bucket default may still apply.")
	h.mustHave("SSE-C needs an https endpoint")
	h.a.closeRewrite()
	h.settle()
}
