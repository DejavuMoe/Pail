package main

import (
	"strings"
	"testing"

	core "github.com/DejavuMoe/Pail/internal/app"
)

// Regression tests of reported bugs, one per bug.

// Moving a bucket's contents in a language other than English said "Copied".
func TestBucketMoveReportsAMoveInChinese(t *testing.T) {
	h := newRawHarness(t, "light", true)
	h.setLocale("zh-Hans")
	n := h.countObjects("acme-website")
	h.a.openBucketCopy("acme-website", true)
	h.a.objOps.bc.dest = "acme-logs"
	h.a.objOps.bc.prefix = "site/"
	h.a.submitBucketCopy()
	h.waitFor("move done", func() bool {
		tk, ok := h.task("Move everything in acme-website")
		return ok && tk.Status == core.StatusSucceeded
	})
	want := "已移动 " + strings.TrimSpace(groupInt(n)) + " 项"
	h.waitToast(want)
}
