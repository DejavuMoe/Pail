package main

import (
	"time"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
)

// The status bar shows how long the last finished operation took, so a slow
// listing or a transfer can be told from a slow connection. Only what the user
// asked for counts: a list that reloads by itself after an upload or a delete
// keeps the time of the upload or the delete.

// What an opTime measured.
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

// noteTook records an operation that took d.
func (a *app) noteTook(what string, d time.Duration) {
	a.took = opTime{what: what, d: max(d, 0)}
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
