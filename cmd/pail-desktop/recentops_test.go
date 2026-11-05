package main

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/DejavuMoe/Pail/internal/s3test"
	"github.com/egoist/mygo/ui"
)

// ---- the lines of the list -----------------------------------------------------------------

func TestOperationLinesSayWhatHappened(t *testing.T) {
	at := time.Date(2026, 11, 5, 14, 3, 9, 0, time.Local)
	for name, c := range map[string]struct {
		e       opEntry
		account bool
		want    string
	}{
		"buckets":           {opEntry{what: tookBuckets}, false, ""},
		"buckets, account":  {opEntry{what: tookBuckets, accountName: "Work"}, true, "Work"},
		"folder":            {opEntry{what: tookList, n: 1, where: "b/photos/"}, false, "1 item · b/photos/"},
		"empty folder":      {opEntry{what: tookList, where: "b/"}, false, "0 items · b/"},
		"upload":            {opEntry{what: tookUpload, n: 3, where: "b/"}, false, "3 files · b/"},
		"download":          {opEntry{what: tookDownload, n: 1, where: "b/"}, false, "1 file · b/"},
		"delete":            {opEntry{what: tookDelete, n: 5, where: "b/"}, false, "5 objects · b/"},
		"part failed":       {opEntry{what: tookUpload, state: opFailed, n: 2, bad: 1, where: "b/"}, false, "2 files · 1 failed · b/"},
		"all failed":        {opEntry{what: tookUpload, state: opFailed, bad: 2, err: "AccessDenied: no\n  retry", where: "b/"}, false, "2 failed · AccessDenied: no retry · b/"},
		"list failed":       {opEntry{what: tookList, state: opFailed, err: "Access denied", where: "b/"}, false, "Failed · Access denied · b/"},
		"buckets failed":    {opEntry{what: tookBuckets, state: opFailed}, false, "Failed"},
		"canceled":          {opEntry{what: tookUpload, state: opCanceled, n: 1, where: "b/"}, false, "Canceled · 1 file · b/"},
		"canceled in queue": {opEntry{what: tookDelete, state: opCanceled, where: "b/"}, false, "Canceled · b/"},
		"canceled folder":   {opEntry{what: tookList, state: opCanceled, where: "b/x/"}, false, "Canceled · b/x/"},
		"with account":      {opEntry{what: tookDownload, n: 2, where: "b/", accountName: "Work"}, true, "2 files · b/ · Work"},
		"account not shown": {opEntry{what: tookDownload, n: 2, where: "b/", accountName: "Work"}, false, "2 files · b/"},
	} {
		if got := c.e.detail(c.account); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}

	e := opEntry{what: tookUpload, ran: true, d: 3500 * time.Millisecond, at: at}
	if e.name() != "Upload" || e.stateText() != "Succeeded" || e.took() != "3.5 s" || e.label() != "Upload, Succeeded, 3.5 s" {
		t.Errorf("%q %q %q %q", e.name(), e.stateText(), e.took(), e.label())
	}
	e = opEntry{what: tookDelete, state: opCanceled, at: at} // canceled while it waited: no time
	if e.took() != "—" || e.label() != "Delete, Canceled" {
		t.Errorf("never ran: %q %q", e.took(), e.label())
	}
	e = opEntry{what: tookList, state: opFailed, ran: true, d: 40 * time.Millisecond}
	if e.name() != "Open folder" || e.label() != "Open folder, Failed, 40 ms" {
		t.Errorf("%q %q", e.name(), e.label())
	}

	// The time of day today; the date and time on another day.
	e = opEntry{at: at}
	if got := e.clock(at.Add(time.Hour)); got != "14:03:09" {
		t.Errorf("same day: %q", got)
	}
	if got := e.clock(at.Add(30 * time.Hour)); got != "11-05 14:03" {
		t.Errorf("another day: %q", got)
	}

	if severalAccounts(nil) || severalAccounts([]opEntry{{account: "a"}, {account: "a"}}) || !severalAccounts([]opEntry{{account: "a"}, {account: "b"}}) {
		t.Error("severalAccounts")
	}
}

// ---- the status bar opens the list ---------------------------------------------------------

// openRecentOps clicks the time in the status bar, as a user does.
func (h *harness) openRecentOps() {
	h.t.Helper()
	label := "Recent operations"
	if h.a.took.what != "" {
		label = h.a.took.tip()
	}
	h.mustClick(label)
	if !h.a.opsOpen {
		h.t.Fatalf("clicking %q did not open the list", label)
	}
}

func opKinds(ops []opEntry) []string {
	var out []string
	for _, e := range ops {
		out = append(out, e.what)
	}
	return out
}

func TestRecentOperationsOpenFromTheStatusBarNewestFirst(t *testing.T) {
	h := newRawHarness(t, "light", true)
	// The buckets were listed when the app started, then the first one was opened.
	if got := opKinds(h.a.ops); !slices.Equal(got, []string{tookBuckets, tookList}) {
		t.Fatalf("after start: %v", got)
	}
	h.a.openBucket("acme-assets")
	h.settle()
	if got := opKinds(h.a.ops); !slices.Equal(got, []string{tookBuckets, tookList, tookList}) {
		t.Fatalf("after opening a bucket: %v", got)
	}
	for _, e := range h.a.ops {
		if e.state != opDone || !e.ran || e.at.IsZero() || e.accountName != h.env.Account.Name || e.seq == 0 {
			t.Errorf("a successful operation is recorded in full: %+v", e)
		}
	}
	h.mustNotHave("Load buckets") // closed until clicked

	h.openRecentOps()
	h.mustHave("Recent operations", "Load buckets", "Open folder", "3")
	assets, analytics := h.a.ops[2].detail(false), h.a.ops[1].detail(false)
	if !strings.Contains(assets, "acme-assets/") || !strings.Contains(analytics, "acme-analytics/") {
		t.Fatalf("details: %q, %q", assets, analytics)
	}
	ra, ok1 := h.tt.Find(assets)
	rb, ok2 := h.tt.Find(analytics)
	_, ok3 := h.tt.Find("Load buckets")
	if !ok1 || !ok2 || !ok3 {
		t.Fatalf("lines missing; have %q", h.tt.Texts())
	}
	if ra.Y >= rb.Y {
		t.Errorf("the newest operation is not on top: %v below %v", ra, rb)
	}
	h.snapBoth("recent-operations")

	// Clicking the time again, or pressing Escape, closes it.
	h.mustClick(h.a.took.tip())
	if h.a.opsOpen {
		t.Fatal("a second click left the list open")
	}
	h.openRecentOps()
	h.tt.Key(0, ui.KeyEscape)
	h.settle()
	if h.a.opsOpen {
		t.Fatal("Escape left the list open")
	}
	h.mustNotHave("Load buckets")

	// The focus went back to the time, so the keyboard opens it again.
	h.tt.Key(0, ui.KeyEnter)
	h.settle()
	if !h.a.opsOpen {
		t.Fatal("Enter on the time did not open the list")
	}
	h.mustHave("Load buckets")
}

func TestRecentOperationsKeepFailedAndCanceledOnesAndTheStatusBarKeepsTheLastSuccess(t *testing.T) {
	h := newHarness(t, "light")
	last := h.a.took
	if last.what != tookList {
		t.Fatalf("took = %+v", last)
	}
	before := len(h.a.ops)

	// A listing that fails.
	h.env.Srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/acme-logs") {
			return &s3test.Fault{Status: 403, Code: "AccessDenied", Message: "no list for you"}
		}
		return nil
	})
	h.a.openBucket("acme-logs")
	h.settle()
	failed := h.a.ops[len(h.a.ops)-1]
	if failed.state != opFailed || failed.what != tookList || failed.where != "acme-logs/" || failed.err == "" || failed.err != h.a.listErr || !failed.ran {
		t.Fatalf("a failed listing: %+v (listErr %q)", failed, h.a.listErr)
	}
	if h.a.took != last {
		t.Errorf("a failed listing set the status bar to %+v from %+v", h.a.took, last)
	}

	// A listing that is left before it ends: another folder is opened while it waits.
	h.env.Srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/acme-logs") {
			return &s3test.Fault{Delay: time.Minute}
		}
		return nil
	})
	h.a.openBucket("acme-logs")
	h.tt.Frame()
	h.a.openBucket("acme-assets")
	h.waitFor("the abandoned listing is recorded", func() bool {
		return slices.ContainsFunc(h.a.ops, func(e opEntry) bool { return e.state == opCanceled })
	})
	h.settle()
	var canceled []opEntry
	for _, e := range h.a.ops {
		if e.state == opCanceled {
			canceled = append(canceled, e)
		}
	}
	if len(canceled) != 1 || canceled[0].what != tookList || canceled[0].where != "acme-logs/" || !canceled[0].ran {
		t.Fatalf("canceled listings: %+v", canceled)
	}
	h.env.Srv.Inject(nil)
	last = h.a.took // the listing of acme-assets that followed succeeded
	if last.what != tookList {
		t.Fatalf("took = %+v", last)
	}

	// Transfers that fail and are canceled end in finishWatch.
	loc := h.a.scope
	t0 := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	h.a.finishWatch(&watch{loc: loc, kind: "upload"}, []Task{
		{Status: core.StatusFailed, Err: "boom", ItemsFailed: 1, Started: t0, Finished: t0.Add(2 * time.Second)},
	}, core.TaskGroupResult{}, false)
	h.a.finishWatch(&watch{loc: loc, kind: "delete"}, []Task{
		{Status: core.StatusCanceled}, // canceled while it waited in the queue
	}, core.TaskGroupResult{}, false)
	h.a.finishWatch(&watch{loc: loc, kind: "download"}, []Task{
		{Status: core.StatusCanceled, Started: t0, Finished: t0.Add(700 * time.Millisecond)},
	}, core.TaskGroupResult{}, false)
	h.settle()
	ops := h.a.ops[before:]
	if len(ops) < 5 {
		t.Fatalf("the operations after the start: %+v", ops)
	}
	up, del, down := ops[len(ops)-3], ops[len(ops)-2], ops[len(ops)-1]
	if up.what != tookUpload || up.state != opFailed || up.bad != 1 || up.err != "boom" || !up.ran || up.d != 2*time.Second || up.where != "acme-assets/" {
		t.Errorf("failed upload: %+v", up)
	}
	if del.what != tookDelete || del.state != opCanceled || del.ran {
		t.Errorf("delete canceled in the queue: %+v", del)
	}
	if down.what != tookDownload || down.state != opCanceled || !down.ran || down.d != 700*time.Millisecond {
		t.Errorf("canceled download: %+v", down)
	}

	// None of them touched the time in the status bar.
	if h.a.took != last {
		t.Errorf("the status bar changed to %+v from %+v; only a success sets it", h.a.took, last)
	}

	h.openRecentOps()
	h.mustHave("Failed · "+oneLine(failed.err), "Canceled", "1 failed · boom", "—", "700 ms", "2.0 s")
	h.snapBoth("recent-operations-outcomes")
}

func TestRecentOperationsKeepOnlyTheNewestUpToTheSetting(t *testing.T) {
	h := newRawHarness(t, "light", true)
	set := func(n int) {
		t.Helper()
		st := h.env.Svc.Settings()
		st.RecentOps = n
		if err := h.env.Svc.SaveSettings(st); err != nil {
			t.Fatal(err)
		}
	}
	add := func(secs int) {
		h.a.noteOp(opEntry{what: tookUpload, ran: true, d: time.Duration(secs) * time.Second, account: h.a.accountID, accountName: "Demo", n: secs})
	}
	secs := func() []int {
		var out []int
		for _, e := range h.a.ops {
			if e.what == tookUpload {
				out = append(out, int(e.d/time.Second))
			}
		}
		return out
	}

	set(3)
	for i := 1; i <= 5; i++ {
		add(i)
	}
	if len(h.a.ops) != 3 || !slices.Equal(secs(), []int{3, 4, 5}) {
		t.Fatalf("limit 3: %v of %d", secs(), len(h.a.ops))
	}
	if h.a.took.what != tookUpload || h.a.took.d != 5*time.Second {
		t.Errorf("took = %+v", h.a.took)
	}

	// A lowered limit shows at once, without waiting for the next operation.
	set(2)
	h.openRecentOps()
	if len(h.a.ops) != 2 || !slices.Equal(secs(), []int{4, 5}) {
		t.Fatalf("limit 2: %v of %d", secs(), len(h.a.ops))
	}
	h.mustHave("5 files", "4 files")
	h.mustNotHave("3 files")

	// Raising it again does not bring back what was dropped.
	set(10)
	add(6)
	if !slices.Equal(secs(), []int{4, 5, 6}) {
		t.Fatalf("limit 10: %v", secs())
	}

	// Clear empties the list and keeps the time in the status bar.
	h.mustClick("Clear")
	if len(h.a.ops) != 0 {
		t.Fatalf("after Clear: %d", len(h.a.ops))
	}
	h.mustHave("No recent operations")
	if h.a.took.what != tookUpload {
		t.Errorf("Clear took the status bar's time: %+v", h.a.took)
	}
	h.tt.Key(0, ui.KeyEscape)
	h.settle()
	// The timer stays, so that the list can be opened again.
	h.mustHave(h.a.took.tip())
}

func TestRecentOperationsAndAccountSwitch(t *testing.T) {
	setup := func(t *testing.T, keep bool) (*harness, core.Account) {
		h := newHarness(t, "light")
		st := h.env.Svc.Settings()
		st.KeepRecentOps = keep
		if err := h.env.Svc.SaveSettings(st); err != nil {
			t.Fatal(err)
		}
		second, err := h.env.Svc.SaveAccount(core.Account{Name: "Second", Provider: core.ProviderS3, Endpoint: h.env.Srv.URL, PathStyle: true,
			Credentials: core.CredStatic, AccessKeyID: "x"}, &core.Secrets{SecretAccessKey: "y"})
		if err != nil {
			t.Fatal(err)
		}
		h.a.refreshAccounts()
		return h, second
	}

	t.Run("cleared by default", func(t *testing.T) {
		h, second := setup(t, false)
		if len(h.a.ops) == 0 {
			t.Fatal("nothing recorded before the switch")
		}
		h.a.selectAccount(second.ID)
		if len(h.a.ops) != 0 || h.a.took.what != "" {
			t.Fatalf("the switch kept %d operations, took %+v", len(h.a.ops), h.a.took)
		}
		h.tt.Frame()
		h.mustNotHave("Recent operations") // nothing to open: the timer is gone
		h.settle()
		// What the new account does is listed, and only that.
		for _, e := range h.a.ops {
			if e.account != second.ID {
				t.Errorf("an operation of another account: %+v", e)
			}
		}
		// An operation of the account just left that ends late is not kept.
		h.a.noteOp(opEntry{what: tookUpload, ran: true, account: h.env.Account.ID, accountName: "Demo", n: 1})
		for _, e := range h.a.ops {
			if e.what == tookUpload {
				t.Errorf("a late operation of the old account was kept: %+v", e)
			}
		}
		if h.a.took.what == tookUpload {
			t.Errorf("a late operation of the old account set the time: %+v", h.a.took)
		}
	})

	t.Run("kept when asked", func(t *testing.T) {
		h, second := setup(t, true)
		old := slices.Clone(h.a.ops)
		if len(old) == 0 {
			t.Fatal("nothing recorded before the switch")
		}
		h.a.selectAccount(second.ID)
		if h.a.took.what != "" {
			t.Errorf("the time of another account's operation stayed in the status bar: %+v", h.a.took)
		}
		if len(h.a.ops) != len(old) || !slices.Equal(opKinds(h.a.ops), opKinds(old)) {
			t.Fatalf("the switch changed the list: %v", opKinds(h.a.ops))
		}
		// The list can still be opened, from the timer without a time.
		h.tt.Frame()
		h.mustHave("Recent operations")
		h.settle()
		h.a.noteOp(opEntry{what: tookUpload, ran: true, account: h.env.Account.ID, accountName: "Demo", n: 1, where: "acme-assets/"})
		h.openRecentOps()
		// With more than one account, every line says which one.
		if !severalAccounts(h.a.ops) {
			t.Fatalf("expected operations of two accounts: %+v", h.a.ops)
		}
		h.mustHave("Second", "Demo")
		h.snapBoth("recent-operations-accounts")
	})
}

// ---- settings ------------------------------------------------------------------------------

func TestRecentOperationsSettings(t *testing.T) {
	h := newRawHarness(t, "light", true)
	h.a.openTool("settings")
	h.settle()
	h.showSetting("Recent operations to keep")
	h.mustHave("DIAGNOSTICS", "Recent operations to keep", "Keep on account switch")
	if st := h.env.Svc.Settings(); st.RecentOps != 100 || st.KeepRecentOps || h.a.sf.recentOps != "100" || h.a.sf.keepOps {
		t.Fatalf("defaults: %+v, form %q %v", st, h.a.sf.recentOps, h.a.sf.keepOps)
	}

	// Beyond 1000 (or below 1) is explained and nothing is written.
	h.editSetting("Recent operations to keep", "1001")
	h.mustHave("Enter a whole number from 1 to 1000")
	if h.a.sf.recentOps != "100" || h.env.Svc.Settings().RecentOps != 100 {
		t.Fatalf("1001 stuck: field %q, saved %d", h.a.sf.recentOps, h.env.Svc.Settings().RecentOps)
	}
	h.editSetting("Recent operations to keep", "0")
	if h.env.Svc.Settings().RecentOps != 100 {
		t.Fatalf("0 stuck: %d", h.env.Svc.Settings().RecentOps)
	}

	h.editSetting("Recent operations to keep", "1000")
	h.waitFor("1000 saved", func() bool { return h.env.Svc.Settings().RecentOps == 1000 })
	h.editSetting("Recent operations to keep", "250")
	h.waitFor("250 saved", func() bool { return h.env.Svc.Settings().RecentOps == 250 })
	if h.a.opsLimit() != 250 {
		t.Errorf("the list's limit = %d", h.a.opsLimit())
	}

	h.clickSettingSwitch("Keep on account switch")
	h.waitFor("keep saved", func() bool { return h.env.Svc.Settings().KeepRecentOps })
	if !h.a.sf.keepOps {
		t.Error("the switch does not show on")
	}
	// Saving another setting keeps both.
	h.editSetting("Part size (MiB)", "24")
	h.waitFor("part size saved", func() bool { return h.env.Svc.Settings().PartSizeMiB == 24 })
	if st := h.env.Svc.Settings(); st.RecentOps != 250 || !st.KeepRecentOps {
		t.Errorf("another setting reset them: %+v", st)
	}
	h.snapBoth("settings-recent-operations")
	h.clickSettingSwitch("Keep on account switch")
	h.waitFor("keep cleared", func() bool { return !h.env.Svc.Settings().KeepRecentOps })
}

// ---- translation ---------------------------------------------------------------------------

func TestRecentOperationsAreTranslated(t *testing.T) {
	h := newHarness(t, "light")
	h.a.ops = nil
	acct := h.a.accountID
	for _, e := range []opEntry{
		{what: tookBuckets, ran: true, d: 120 * time.Millisecond},
		{what: tookList, ran: true, d: 90 * time.Millisecond, n: 12, where: "acme-assets/"},
		{what: tookUpload, ran: true, d: 3500 * time.Millisecond, n: 3, where: "acme-assets/"},
		{what: tookDownload, state: opFailed, ran: true, d: time.Second, bad: 2, err: "denied", where: "acme-assets/"},
		{what: tookDelete, state: opCanceled, where: "acme-assets/"},
		{what: tookList, state: opFailed, ran: true, d: time.Second, where: "acme-assets/"},
	} {
		e.account, e.accountName = acct, "Demo"
		h.a.noteOp(e)
	}
	h.a.opsOpen = false
	if left := h.newPlainTexts(func() { h.a.opsOpen = true }); len(left) > 0 {
		t.Errorf("the list has texts that are not translated: %q", left)
	}

	h.setLocale("zh-Hans")
	h.mustHave("最近操作", "加载存储桶", "打开文件夹", "上传", "下载", "删除", "已取消", "失败", "12 项", "3 个文件", "2 项失败", "清空")
	h.snapBoth("recent-operations-zh")
	h.setLocale("en")
}

func TestRecentOperationsListOfAThousandStillBuilds(t *testing.T) {
	h := newRawHarness(t, "light", true)
	st := h.env.Svc.Settings()
	st.RecentOps = core.MaxRecentOps
	if err := h.env.Svc.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1200; i++ {
		h.a.noteOp(opEntry{what: tookUpload, ran: true, d: time.Duration(i) * time.Millisecond, account: h.a.accountID, accountName: "Demo", n: i + 1, where: "acme-assets/"})
	}
	if len(h.a.ops) != core.MaxRecentOps {
		t.Fatalf("kept %d, want %d", len(h.a.ops), core.MaxRecentOps)
	}
	h.openRecentOps()
	h.mustHave("1,000", "1,200 files") // the newest is on top; the count in the header is grouped
	start := time.Now()
	for i := 0; i < 5; i++ {
		h.tt.Frame()
	}
	t.Logf("a frame of the list of 1000 takes %v", time.Since(start)/5)
}
