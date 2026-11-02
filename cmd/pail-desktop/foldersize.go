package main

import (
	"context"
	"strings"
	"time"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/icons"
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/egoist/mygo/ui"
)

// Folder sizes. S3 keeps none: a folder's size is the total of every object
// below its prefix, which only a listing of the whole prefix finds, a request
// per 1,000 objects. So the table does not size folders by itself (its Size
// cell shows a dash) and sizes one when asked: the "Calculate size" item of a
// folder's context menu, or "Calculate folder sizes" in the toolbar for every
// folder shown. The walks run off the UI thread, at most folderSizeWorkers at
// a time, and the cell shows the running total under a spinner until its walk
// ends.
//
// A result stays while the user browses elsewhere in the bucket and is
// dropped when it can be out of date: a refresh of a folder (F5, or the reload
// after an upload, a delete, a copy...) drops the sizes of that folder, of
// the folders above it and of those below it.

// folderSizeWorkers is how many folders are walked at once.
const folderSizeWorkers = 3

const (
	sizeQueued = iota
	sizeRunning
	sizeReady
	sizeFailed
)

// folderSize is one folder's size: queued, being walked (est is the total so
// far), known, or failed (err).
type folderSize struct {
	scope  Scope // the folder itself: its Prefix is the folder key
	state  int
	est    core.Estimate
	err    string
	cancel context.CancelFunc
}

// folderSizes is every size asked for, by sizeKey, and the order the queued
// ones wait in.
type folderSizes struct {
	by      map[string]*folderSize
	queue   []string
	running int
}

// sizeKey names a folder of an account's bucket.
func sizeKey(account, bucket, folder string) string {
	return account + "\x00" + bucket + "\x00" + folder
}

// folderSizeOf is the size entry of a folder row of the open folder, nil when
// it was never asked for.
func (a *app) folderSizeOf(folderKey string) *folderSize {
	return a.sizes.by[sizeKey(a.scope.AccountID, a.scope.Bucket, folderKey)]
}

// knownFolderSize is a folder's size once its walk has ended.
func (a *app) knownFolderSize(folderKey string) (int64, bool) {
	if fs := a.folderSizeOf(folderKey); fs != nil && fs.state == sizeReady {
		return fs.est.Bytes, true
	}
	return 0, false
}

// sizeWanted reports whether a folder has no size and none is on its way: a
// new request for it would do something.
func (a *app) sizeWanted(folderKey string) bool {
	fs := a.folderSizeOf(folderKey)
	return fs == nil || fs.state == sizeFailed
}

// selectedFolders are the folder keys of the selection, in table order.
func (a *app) selectedFolders() []string {
	var out []string
	for _, k := range a.selectedKeys() {
		if isFolderKey(k) {
			out = append(out, k)
		}
	}
	return out
}

// shownFolders are the folder keys of the rows shown, in table order.
func (a *app) shownFolders() []string {
	var out []string
	for _, e := range a.rows {
		if e.Folder {
			out = append(out, e.Key)
		}
	}
	return out
}

// anyWanted reports whether one of keys has no size on its way.
func (a *app) anyWanted(keys []string) bool {
	for _, k := range keys {
		if a.sizeWanted(k) {
			return true
		}
	}
	return false
}

// canSizeSelection and canSizeShown enable the two commands: something to
// size that is not sized or being sized already.
func canSizeSelection(a *app) bool { return liveBrowsing(a) && a.anyWanted(a.selectedFolders()) }
func canSizeShown(a *app) bool     { return liveBrowsing(a) && a.anyWanted(a.shownFolders()) }

// sizeFolders queues the folders of the open folder that have no size on its
// way, and starts as many walks as there is room for.
func (a *app) sizeFolders(keys []string) {
	if a.sizes.by == nil {
		a.sizes.by = map[string]*folderSize{}
	}
	for _, k := range keys {
		if !a.sizeWanted(k) {
			continue
		}
		s := a.scope
		s.Prefix = k
		id := sizeKey(s.AccountID, s.Bucket, k)
		a.sizes.by[id] = &folderSize{scope: s, state: sizeQueued}
		a.sizes.queue = append(a.sizes.queue, id)
	}
	a.pumpSizes()
}

// pumpSizes starts queued walks while fewer than folderSizeWorkers run.
func (a *app) pumpSizes() {
	for a.sizes.running < folderSizeWorkers && len(a.sizes.queue) > 0 {
		id := a.sizes.queue[0]
		a.sizes.queue = a.sizes.queue[1:]
		if fs := a.sizes.by[id]; fs != nil && fs.state == sizeQueued {
			a.walkFolder(fs)
		}
	}
}

// walkFolder lists fs's folder on a goroutine. The running total reaches the
// table at most every 100ms; a walk whose entry was dropped (or replaced)
// meanwhile changes nothing.
func (a *app) walkFolder(fs *folderSize) {
	ctx, cancel := context.WithCancel(a.ctx)
	fs.state, fs.cancel = sizeRunning, cancel
	a.sizes.running++
	a.busyOps.Add(1)
	id := sizeKey(fs.scope.AccountID, fs.scope.Bucket, fs.scope.Prefix)
	current := func() bool { return a.sizes.by[id] == fs }
	go func() {
		defer cancel()
		var last time.Time
		est, err := a.svc.FolderSize(ctx, fs.scope, func(p core.Estimate) {
			if now := time.Now(); now.Sub(last) >= 100*time.Millisecond {
				last = now
				a.post(func() {
					if current() && fs.state == sizeRunning {
						fs.est = p
					}
				})
			}
		})
		a.post(func() {
			a.busyOps.Add(-1)
			a.sizes.running--
			defer a.pumpSizes()
			if !current() {
				return
			}
			fs.cancel = nil
			switch {
			case err != nil:
				fs.state, fs.err = sizeFailed, humanError(err)
			default:
				fs.state, fs.est = sizeReady, est
			}
			if a.sort.Column == "size" {
				a.rowsDirty = true // the folder moves to its place among the folders
			}
		})
	}()
}

// dropSizes forgets the sizes that a change at s can have made wrong: those of
// the folder s, of the folders above it and of those below it, in its bucket.
// Their walks stop.
func (a *app) dropSizes(s Scope) {
	for id, fs := range a.sizes.by {
		f := fs.scope
		if f.AccountID != s.AccountID || f.Bucket != s.Bucket {
			continue
		}
		if !strings.HasPrefix(f.Prefix, s.Prefix) && !strings.HasPrefix(s.Prefix, f.Prefix) {
			continue
		}
		if fs.cancel != nil {
			fs.cancel()
		}
		delete(a.sizes.by, id)
	}
}

// folderSizeCell is the Size cell of a folder row: a dash until it is asked
// for, then a spinner with the running total, then the size (its object
// count in the tooltip), or "Failed" with the reason.
func (a *app) folderSizeCell(c *ui.Context, e *Entry) {
	k := tk(c)
	fs := a.folderSizeOf(e.Key)
	box := ui.Row(c.Key("size")).FillWidth().Height(18).Gap(6).AlignItems(ui.Center).Justify(ui.End)
	if a.dropTarget(box, a.dropDest(e)) {
		a.mg.dropKey = e.Key
	}
	text := func(s string, col ui.Color) {
		ui.Text(c, s).FontSize(sizeSecondary).TextColor(col).SingleLine().TextAlign(ui.End)
	}
	switch {
	case fs == nil:
		box.Tooltip(locales.ObjectsSizeNotCalculated())
		box.Children(func() { text(locales.ObjectsSizeUnknown(), k.TextSubtle) })
	case fs.state == sizeQueued || fs.state == sizeRunning:
		box.Tooltip(locales.ObjectsSizeCalculating())
		box.Children(func() {
			ui.Spinner(c).Size(12, 12)
			if fs.est.Objects > 0 {
				text(humanBytes(fs.est.Bytes), k.TextMuted)
			}
		})
	case fs.state == sizeFailed:
		box.Tooltip(locales.ObjectsSizeFailedTip(fs.err))
		box.Children(func() {
			ui.Icon(c, icons.Get("circle-alert")).FontSize(12).TextColor(k.Danger)
			text(locales.ObjectsSizeFailed(), k.Danger)
		})
	default:
		box.Tooltip(locales.ScopeCountSummary(locales.CommonObjects(fs.est.Objects), humanBytes(fs.est.Bytes)))
		box.Children(func() { text(humanBytes(fs.est.Bytes), k.TextMuted) })
	}
}
