package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
)

// Localization of the five tool views (upload rules, sync jobs, CloudFront,
// MIME types, multipart uploads), the sync job editor and the invalidation
// dialog. Each screen is opened once, shown under the pseudo locale to find
// text that does not come from the catalog, and then under Chinese.

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
	regexp.MustCompile(`^(CloudFront|S3|AWS|MinIO|Pail)$`),
	regexp.MustCompile(`^s3://[\w./-]+$`),
	regexp.MustCompile(`^(Site|Site 2|Images|abc|bad|Pail)$`),
	regexp.MustCompile(`^[\w./*-]+ / [\w./*-]+$`),                                      // a rule mask: acme-* / *.png
	regexp.MustCompile(`^.+ ↔ s3://`),                                                  // a job row: folder ↔ s3://bucket/prefix
	regexp.MustCompile(`^[A-Za-z]:\\`),                                                 // a local path on Windows
	regexp.MustCompile(`^[\w-]{10}…$`),                                                 // a shortened upload ID
	regexp.MustCompile(`^rule \d+ \(.*\): `),                                           // a rule problem from the application layer, kept verbatim
	regexp.MustCompile(`^[a-z]{2}-[a-z]+-\d$`),                                         // region ids
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
	var bad []string
next:
	for _, s := range h.untranslated() {
		for _, re := range toolsPlainOK {
			if re.MatchString(s) {
				continue next
			}
		}
		bad = append(bad, s)
	}
	if len(bad) > 0 {
		h.t.Errorf("%s: texts that stay plain English under en-XA: %q", screen, bad)
	}
	h.setLocale("en")
}

// hoverHelp hovers the "?" whose tip is help and expects the tip to show: the
// text is on the screen twice, as the button's label and as the tip.
func (h *harness) hoverHelp(help string) {
	h.t.Helper()
	h.tt.Frame()
	r, ok := h.tt.Find(help)
	if !ok {
		h.t.Errorf("no help icon with the tip %q", help)
		return
	}
	h.tt.Move(r.X+r.W/2, r.Y+r.H/2)
	h.tt.Advance(700 * time.Millisecond)
	h.tt.Frame()
	n := 0
	for _, s := range h.tt.Texts() {
		if strings.TrimSpace(s) == help {
			n++
		}
	}
	if n < 2 {
		h.t.Errorf("the tip %q is not shown on hover (seen %d times)", help, n)
	}
	h.tt.Move(0, 0)
	h.tt.Frame()
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
		h.mustHave("没有同步任务", "创建同步任务", "新建任务")
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
