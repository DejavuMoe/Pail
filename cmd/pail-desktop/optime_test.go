package main

import (
	"strings"
	"testing"
	"time"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
)

func TestHumanDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0:                      "0 ms",
		-time.Second:           "0 ms",
		850 * time.Millisecond: "850 ms",
		999*time.Millisecond + 600*time.Microsecond: "999 ms",
		time.Second:                             "1.0 s",
		3500 * time.Millisecond:                 "3.5 s",
		3540 * time.Millisecond:                 "3.5 s",
		3560 * time.Millisecond:                 "3.6 s",
		9949 * time.Millisecond:                 "9.9 s",
		9951 * time.Millisecond:                 "10 s", // never "10.0 s"
		10 * time.Second:                        "10 s",
		42400 * time.Millisecond:                "42 s",
		59600 * time.Millisecond:                "1 m 00 s", // never "60 s"
		time.Minute + 5*time.Second:             "1 m 05 s",
		59*time.Minute + 59*time.Second:         "59 m 59 s",
		59*time.Minute + 59500*time.Millisecond: "1 h 00 m",
		time.Hour + 7*time.Minute:               "1 h 07 m",
		26*time.Hour + 30*time.Minute:           "26 h 30 m",
	} {
		if got := humanDuration(d); got != want {
			t.Errorf("humanDuration(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestGroupDuration(t *testing.T) {
	t0 := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	at := func(s float64) time.Time { return t0.Add(time.Duration(s * float64(time.Second))) }
	for name, c := range map[string]struct {
		ts   []Task
		want time.Duration
		ok   bool
	}{
		"none":        {nil, 0, false},
		"not started": {[]Task{{Created: t0}}, 0, false},
		"one task":    {[]Task{{Started: at(1), Finished: at(4.5)}}, 3500 * time.Millisecond, true},
		"first start to last finish": {[]Task{
			{Started: at(2), Finished: at(3)}, {Started: at(1), Finished: at(2.5)}, {Started: at(2.5), Finished: at(9)},
		}, 8 * time.Second, true},
		"a task that never ran is ignored": {[]Task{{Started: at(1), Finished: at(2)}, {}}, time.Second, true},
		// A clock that ticks in half-milliseconds reads 0 for a fast task: still an operation.
		"no time passed": {[]Task{{Started: at(1), Finished: at(1)}}, 0, true},
	} {
		if got, ok := groupDuration(c.ts); got != c.want || ok != c.ok {
			t.Errorf("%s: %v, %v; want %v, %v", name, got, ok, c.want, c.ok)
		}
	}
}

func TestStatusBarShowsHowLongTheLastOperationTook(t *testing.T) {
	h := newRawHarness(t, "light", true)
	// The buckets were listed when the app started, then the first bucket was opened.
	if h.a.took.what != tookList {
		t.Fatalf("after the first load: %+v", h.a.took)
	}
	h.a.loadBuckets(false)
	h.settle()
	if h.a.took.what != tookBuckets {
		t.Fatalf("after listing the buckets: %+v", h.a.took)
	}
	h.mustHave(locales.StatusbarTookBuckets(humanDuration(h.a.took.d)))

	// Opening a folder is the next operation, and replaces it.
	h.a.openBucket("acme-assets")
	h.settle()
	if h.a.took.what != tookList {
		t.Fatalf("after listing a bucket: %+v", h.a.took)
	}
	h.mustHave(humanDuration(h.a.took.d), locales.StatusbarTookList(humanDuration(h.a.took.d)))
	h.snapBoth("statusbar-took")

	// A longer time reads as seconds, in either language.
	h.a.took = opTime{what: tookUpload, d: 3500 * time.Millisecond}
	h.tt.Frame()
	h.mustHave("3.5 s", "The upload took 3.5 s")
	h.setLocale("zh-Hans")
	h.mustHave("3.5 s", "上传用时 3.5 s")
	h.a.took = opTime{what: tookDelete, d: 12 * time.Second}
	h.tt.Frame()
	h.mustHave("12 s", "删除用时 12 s")
	h.setLocale("en")

	// Another account's time says nothing here: switching clears it.
	h.a.took = opTime{what: tookList, d: time.Second}
	h.a.selectAccount("")
	if h.a.took.what != "" {
		t.Fatalf("the time survived an account switch: %+v", h.a.took)
	}
}

func TestUploadTimeIsNotReplacedByTheListThatFollowsIt(t *testing.T) {
	h := newHarness(t, "light")
	p := writeTemp(t, "timed.bin", []byte(strings.Repeat("Pail", 1024)))
	h.a.uploadPaths([]string{p})
	h.waitToast("Uploaded 1 file")
	h.settle()
	h.waitFor("timed.bin listed", func() bool { return h.has("timed.bin") })
	if h.a.took.what != tookUpload {
		t.Fatalf("after the upload: %+v; the refresh that follows it must keep the upload's time", h.a.took)
	}
	h.mustHave(locales.StatusbarTookUpload(humanDuration(h.a.took.d)))

	// The user's own navigation is a new operation.
	h.a.openPrefix("assets/")
	h.settle()
	if h.a.took.what != tookList {
		t.Fatalf("after opening a folder: %+v", h.a.took)
	}

	// So is a delete, again followed by a refresh that keeps its time.
	h.a.openScope(core.Location{AccountID: h.a.accountID, Bucket: bkt})
	h.settle()
	h.a.sel.Clear()
	h.a.sel.Add("timed.bin")
	h.a.askDelete()
	h.settle()
	h.mustClick("Delete 1 item")
	h.waitToast("Deleted 1 object")
	h.settle()
	if h.a.took.what != tookDelete {
		t.Fatalf("after the delete: %+v", h.a.took)
	}
}
