package main

import (
	"strings"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/egoist/mygo/ui"
)

// clipData is the in-app clipboard: what was copied or cut, and where from.
type clipData struct {
	loc  Scope
	keys []string
	cut  bool
}

func (c clipData) has() bool { return len(c.keys) > 0 }

// liveSelection is a selection of listed objects (not the deleted view).
func liveSelection(a *app) bool { return a.browsing() && a.sel.Len() > 0 && !a.mg.del.on }

func canPaste(a *app) bool { return a.browsing() && a.mg.clip.has() && !a.mg.del.on }

// copySelection puts the selection on the in-app clipboard.
func (a *app) copySelection(cut bool) {
	keys := a.selectedKeys()
	if len(keys) == 0 {
		return
	}
	a.mg.clip = clipData{loc: a.scope, keys: keys, cut: cut}
}

// isCutRow reports whether key waits to be moved from the open folder.
func (a *app) isCutRow(key string) bool {
	c := a.mg.clip
	return c.cut && c.loc == a.scope && a.keyInClip(key)
}

func (a *app) keyInClip(key string) bool {
	for _, k := range a.mg.clip.keys {
		if k == key {
			return true
		}
	}
	return false
}

// paste copies or moves the clipboard into the open folder.
func (a *app) paste() {
	cl := a.mg.clip
	if !cl.has() || !a.browsing() {
		return
	}
	if cl.loc.AccountID != a.scope.AccountID {
		a.toast(locales.ClipboardCrossAccount())
		return
	}
	a.transferKeys(cl.loc, cl.keys, a.scope, cl.cut, cl.cut)
}

// transferKeys copies or moves keys of src into the folder dest. clearClip
// empties the clipboard once a move was queued.
func (a *app) transferKeys(src Scope, keys []string, dest Scope, move, clearClip bool) {
	opt := core.CopyOptions{Conflict: a.svc.Settings().Conflict, Move: move}
	verb, past := "Copy", "Copied" // the words copyDone takes (English keys, see there)
	if move {
		verb, past = "Move", "Moved"
	}
	a.bg(func() func() {
		id, err := a.svc.Copy(core.Selection{Location: src, Keys: keys}, dest, opt)
		return func() {
			if err != nil {
				a.toast(copyFailedText(verb, humanError(err)))
				return
			}
			if clearClip {
				a.mg.clip = clipData{}
			}
			a.follow(id, a.copyDone(verb, past, src, dest))
		}
	})
}

// copyDone reports the end of a copy or move task and refreshes the listing
// of both folders.
func (a *app) copyDone(verb, past string, src, dest Scope) func(Task) {
	return func(t Task) {
		sum := a.bulkSummary(t)
		move := verb == "Move"
		ok := locales.CommonItems(sum.ok)
		switch {
		case sum.failed > 0 && sum.ok == 0:
			a.toast(copyFailedText(verb, sum.firstErr))
		case sum.failed > 0 && move:
			a.toast(locales.ClipboardMovedFailed(ok, sum.failed))
		case sum.failed > 0:
			a.toast(locales.ClipboardCopiedFailed(ok, sum.failed))
		case sum.skipped > 0 && sum.ok == 0:
			a.toast(locales.ClipboardSkippedExisting(locales.CommonItems(sum.skipped)))
		case sum.skipped > 0 && move:
			a.toast(locales.ClipboardMovedSkipped(ok, sum.skipped))
		case sum.skipped > 0:
			a.toast(locales.ClipboardCopiedSkipped(ok, sum.skipped))
		case move:
			a.toast(locales.ClipboardMoved(ok))
		default:
			a.toast(locales.ClipboardCopied(ok))
		}
		a.reloadSame(src)
		a.reloadSame(dest)
	}
}

// copyFailedText is the "failed" toast of a copy or a move; verb is the English
// word the callers pass ("Move"), anything else is a copy.
func copyFailedText(verb, detail string) string {
	if verb == "Move" {
		return locales.ClipboardMoveFailed(detail)
	}
	return locales.ClipboardCopyFailed(detail)
}

// askMove confirms moving the selected rows into dest after a drag.
func (a *app) askMove(dest Scope) {
	src := a.scope
	var keys []string
	for _, k := range a.selectedKeys() {
		if k != dest.Prefix {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 || dest == src {
		return
	}
	where := func(l Scope) string {
		if l.Prefix == "" {
			return locales.ClipboardBucketRoot()
		}
		return l.Prefix
	}
	rows := append(a.scopeRows(src), KV{locales.ClipboardFrom(), where(src)}, KV{locales.ClipboardTo(), where(dest)}, KV{locales.ClipboardItems(), describeKeys(keys)})
	a.confirm = confirmState{
		open: true, title: locales.ClipboardMoveTitle(locales.CommonItems(len(keys))), rows: rows, action: locales.CommonMove(),
		run: func() { a.transferKeys(src, keys, dest, true, false) },
	}
}

// dropTarget makes e take rows dragged from the object table: dest is the
// folder they move into ("" key means not a target and the drop is ignored).
// It reports whether rows are over e right now.
func (a *app) dropTarget(e ui.Element, dest *Scope) bool {
	_, over := ui.DragOver[any](e)
	if v, ok := ui.Drop[any](e); ok && dest != nil && a.sel.Len() > 0 {
		_ = v
		a.askMove(*dest)
	}
	return over && dest != nil
}

// folderScope is the scope of a folder key below the open folder.
func (a *app) folderScope(key string) Scope {
	s := a.scope
	s.Prefix = key
	return s
}

func isFolderKey(key string) bool { return strings.HasSuffix(key, "/") }
