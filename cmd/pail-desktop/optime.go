package main

import (
	"slices"
	"time"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
)

// The status bar shows how long the last finished operation took, so a slow
// listing or a transfer can be told from a slow connection. Only what the user
// asked for counts: a list that reloads by itself after an upload or a delete
// keeps the time of the upload or the delete.
//
// Behind it is the list of recent operations (recentops.go): the same
// operations, each with how it ended and how long it took, failed and canceled
// ones included. The status bar keeps showing the last success only.

// What an opTime measured. The last three are also the kinds of a watch
// (actions.go), which names them the same.
const (
	tookBuckets  = "buckets"
	tookList     = "list"
	tookUpload   = "upload"
	tookDownload = "download"
	tookDelete   = "delete"
)

// opTime is the duration of the last finished operation; the zero value (no
// operation yet, or the account changed) shows nothing. A clock that ticks in
// steps of about half a millisecond can read 0 for a fast call, so "recorded"
// is what != "", not d > 0.
type opTime struct {
	what string
	d    time.Duration
}

// tip says what took the time, in the language shown now.
func (t opTime) tip() string {
	d := humanDuration(t.d)
	switch t.what {
	case tookBuckets:
		return locales.StatusbarTookBuckets(d)
	case tookUpload:
		return locales.StatusbarTookUpload(d)
	case tookDownload:
		return locales.StatusbarTookDownload(d)
	case tookDelete:
		return locales.StatusbarTookDelete(d)
	}
	return locales.StatusbarTookList(d)
}

// opState is how an operation ended.
type opState uint8

const (
	opDone opState = iota
	opFailed
	opCanceled
)

// opEntry is one line of the list of recent operations (recentops.go): what the
// user did, how it ended and how long it took. The list lives in memory only.
type opEntry struct {
	seq   uint64 // the order they were recorded in: a line's identity while the list changes
	what  string // tookBuckets, tookList, tookUpload, tookDownload or tookDelete
	state opState
	// ran says d was measured: a transfer canceled while it still waited in the
	// queue never ran and has no time.
	ran bool
	d   time.Duration
	at  time.Time // when it ended
	// account is the ID of the account it was done with; accountName is the name
	// that account had then (it may be renamed or removed since).
	account, accountName string
	where                string // "bucket/prefix"; empty for the list of buckets
	n, bad               int    // items done and items that failed, for the operations that count them
	err                  string // why it failed
}

// noteOp records a finished operation in the list of recent operations. A
// success the user asked for is also the time the status bar shows.
//
// An operation of another account than the one in use is kept only when
// KeepRecentOps is on: it may be one that finished after the account was
// changed, which the change has just cleared the list of.
func (a *app) noteOp(e opEntry) {
	if e.at.IsZero() {
		e.at = time.Now()
	}
	e.d = max(e.d, 0)
	mine := e.account == a.accountID
	if mine && e.state == opDone {
		a.took = opTime{what: e.what, d: e.d}
	}
	if !mine && !a.svc.Settings().KeepRecentOps {
		return
	}
	a.opSeq++
	e.seq = a.opSeq
	a.ops = append(a.ops, e)
	a.trimOps()
}

// scopeText is where something happened, as a path: "bucket/prefix". It is the
// real bucket name, not its alias: the list is a record of what was done.
func scopeText(s Scope) string {
	if s.Bucket == "" {
		return ""
	}
	return s.Bucket + "/" + s.Prefix
}

// accountNameOf is the name of the saved account id, "" when there is none.
func (a *app) accountNameOf(id string) string {
	for _, ac := range a.accounts {
		if ac.ID == id {
			return ac.Name
		}
	}
	return ""
}

// opsLimit is how many operations the list keeps.
func (a *app) opsLimit() int {
	if n := a.svc.Settings().RecentOps; n >= core.MinRecentOps {
		return min(n, core.MaxRecentOps)
	}
	return core.DefaultRecentOps
}

// trimOps drops the oldest operations beyond the limit of the setting.
func (a *app) trimOps() {
	if over := len(a.ops) - a.opsLimit(); over > 0 {
		a.ops = slices.Delete(a.ops, 0, over)
	}
}

// clearOps empties the list of recent operations. The time in the status bar
// stays: it is the last operation, not the list.
func (a *app) clearOps() {
	a.ops = nil
}

// accountSwitched is what choosing another account does to the operations done
// so far: the status bar's time says nothing about the new account and goes; the
// list goes too unless the user asked to keep it.
func (a *app) accountSwitched() {
	a.took = opTime{} // the time of another account's operation says nothing here
	if !a.svc.Settings().KeepRecentOps {
		a.clearOps()
	}
}

// reloadQuietly lists the open folder again without replacing the recorded time
// with the time of that list (see the top of this file).
func (a *app) reloadQuietly() {
	a.listQuiet = true
	a.reload()
	a.listQuiet = false
}

// groupDuration is the time from the first start to the last finish of the
// tasks of one action; ok is false when none of them ran.
func groupDuration(ts []Task) (d time.Duration, ok bool) {
	var first, last time.Time
	for _, t := range ts {
		if t.Started.IsZero() || t.Finished.IsZero() {
			continue
		}
		if first.IsZero() || t.Started.Before(first) {
			first = t.Started
		}
		if last.IsZero() || t.Finished.After(last) {
			last = t.Finished
		}
	}
	if first.IsZero() {
		return 0, false
	}
	return max(last.Sub(first), 0), true
}
