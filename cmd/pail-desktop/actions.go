package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
)

// bg runs work on a goroutine and applies the function it returns on the UI
// thread. It is how every Service call that can touch the network, the disk or
// the keyring is made; busyOps lets tests wait for the result.
func (a *app) bg(work func() func()) {
	a.busyOps.Add(1)
	go func() {
		apply := work()
		a.post(func() {
			a.busyOps.Add(-1)
			if apply != nil {
				apply()
			}
		})
	}()
}

// timeout returns a context for one short request.
func (a *app) timeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(a.ctx, d)
}

// ---- watches: toasts and refreshes when a queued action finishes -----------------------

// watch follows one group or task until it has finished, then toasts the
// outcome and refreshes the listing when it changed the bucket.
type watch struct {
	loc  Scope
	kind string // "upload" | "download" | "delete"
	dest string // download folder
	file string // the single downloaded file, when exactly one was asked for
}

// addWatch starts following key (a group ID or task ID). Events that arrived
// before the watch existed are folded in first, so a very fast action is not
// missed.
func (a *app) addWatch(key string, w *watch) {
	a.watches[key] = w
	a.applyInbox()
	a.checkWatch(key)
}

func (a *app) checkWatch(key string) {
	w := a.watches[key]
	if w == nil {
		return
	}
	var group []Task
	result, grouped := a.svc.TaskGroup(key)
	if grouped && result.Pending > 0 {
		return
	}
	for _, t := range a.svc.Tasks() {
		if t.GroupID == key || t.ID == key {
			group = append(group, t)
		}
	}
	if len(group) == 0 {
		return
	}
	for _, t := range group {
		if !t.Status.Finished() {
			return
		}
	}
	delete(a.watches, key)
	a.finishWatch(w, group, result, grouped)
}

func (a *app) finishWatch(w *watch, ts []Task, result core.TaskGroupResult, grouped bool) {
	var done, failed, canceled, items, itemsFailed int
	firstErr := ""
	for _, t := range ts {
		switch t.Status {
		case core.StatusSucceeded:
			if t.Kind != core.KindOther {
				done += t.ItemsDone - t.ItemsFailed - t.ItemsSkipped
			}
			items += t.ItemsDone - t.ItemsFailed - t.ItemsSkipped
		case core.StatusCanceled:
			canceled++
		default:
			failed++
			itemsFailed += t.ItemsFailed
			if firstErr == "" {
				firstErr = t.Err
			}
			if t.Status == core.StatusPartiallyFailed {
				items += t.ItemsDone - t.ItemsFailed - t.ItemsSkipped
			}
		}
	}
	if grouped {
		done, failed, canceled = result.Succeeded, result.Failed, result.Canceled
	}
	if w.loc.AccountID == a.scope.AccountID && w.loc.Bucket == a.scope.Bucket && a.scope.Bucket != "" {
		a.reload()
	}
	switch w.kind {
	case "upload":
		switch {
		case failed > 0:
			a.toast(locales.ActionsUploadErrors(fmt.Sprint(done), fmt.Sprint(failed)))
		case done > 0:
			msg := locales.ActionsUploaded(locales.CommonFiles(done))
			if u := a.uploadedCDNLink(w.loc, ts); u != "" {
				a.toastAction(msg, locales.ActionsCopyCdnLink(), func() {
					a.post(func() { a.copyText(u); a.toast(locales.CdnUrlCopied()) })
				})
			} else {
				a.toast(msg)
			}
		case canceled > 0:
			a.toast(locales.ActionsUploadCanceled())
		default:
			a.toast(locales.ActionsNothingUploaded())
		}
	case "download":
		switch {
		case failed > 0:
			a.toast(locales.ActionsDownloadErrors(fmt.Sprint(done), fmt.Sprint(failed)))
		case done > 0:
			a.toastAction(locales.ActionsDownloaded(locales.CommonFiles(done)), locales.ActionsShowInFolder(), func() { a.revealPath(w) })
		case canceled > 0:
			a.toast(locales.ActionsDownloadCanceled())
		default:
			a.toast(locales.ActionsNothingDownloaded())
		}
	case "delete":
		switch {
		case canceled > 0 && failed == 0 && items == 0:
			a.toast(locales.ActionsDeleteCanceled())
		case failed > 0 && items == 0 && itemsFailed == 0:
			a.toast(locales.ActionsDeleteFailed(firstErr))
		case failed > 0:
			a.toast(locales.ActionsDeletedSomeFailed(locales.CommonObjects(items), fmt.Sprint(itemsFailed)))
		default:
			a.toast(locales.ActionsDeleted(locales.CommonObjects(items)))
		}
	}
}

// revealPath shows the downloaded file (or the folder) in the file manager.
func (a *app) revealPath(w *watch) {
	go func() {
		p := w.dest
		if w.file != "" {
			if _, err := os.Stat(w.file); err == nil {
				p = w.file
			}
		}
		a.reveal(p)
	}()
}

// ---- upload and download --------------------------------------------------------------

// startUpload asks for files or a folder and uploads them to the current folder.
func (a *app) startUpload(dir bool) {
	if !a.browsing() {
		return
	}
	go func() {
		paths := a.pick(dir)
		if len(paths) == 0 {
			return
		}
		a.post(func() { a.uploadPaths(paths) })
	}()
}

// uploadPaths uploads local files and folders to the current folder; it is
// the target of the dialog and of a drag-and-drop.
func (a *app) uploadPaths(paths []string) {
	if !a.browsing() || len(paths) == 0 {
		return
	}
	loc := a.scope
	a.bg(func() func() {
		gid, err := a.svc.Upload(core.UploadRequest{Dest: loc, Paths: paths, Conflict: a.svc.Settings().Conflict})
		return func() {
			if err != nil {
				a.toast(locales.ActionsUploadStartFailed(a.noteErr(err, "upload", func() {
					if loc == a.scope {
						a.uploadPaths(paths)
					}
				})))
				return
			}
			a.transfersOpen, a.transTab = true, 0
			a.addWatch(gid, &watch{loc: loc, kind: "upload"})
		}
	})
}

// startDownload downloads the selection into the default download folder, or
// into a folder the user picks.
func (a *app) startDownload() {
	keys := a.selectedKeys()
	if !a.browsing() || len(keys) == 0 {
		return
	}
	loc := a.scope
	dir := a.svc.Settings().DefaultDownloadDir
	go func() {
		d := dir
		if d == "" {
			ds := a.pick(true)
			if len(ds) == 0 {
				return
			}
			d = ds[0]
		}
		a.post(func() { a.downloadKeys(loc, keys, d) })
	}()
}

// downloadKeys downloads keys (folders end in "/") of loc into dest.
func (a *app) downloadKeys(loc Scope, keys []string, dest string) {
	file := ""
	if len(keys) == 1 && !strings.HasSuffix(keys[0], "/") {
		file = filepath.Join(dest, filepath.FromSlash(strings.TrimPrefix(keys[0], loc.Prefix)))
	}
	a.bg(func() func() {
		gid, err := a.svc.Download(core.DownloadRequest{Source: loc, Keys: keys, DestDir: dest, Conflict: a.svc.Settings().Conflict})
		return func() {
			if err != nil {
				a.toast(locales.ActionsDownloadStartFailed(a.noteErr(err, "download", func() {
					a.downloadKeys(loc, keys, dest)
				})))
				return
			}
			a.transfersOpen, a.transTab = true, 0
			a.addWatch(gid, &watch{loc: loc, kind: "download", dest: dest, file: file})
		}
	})
}

// ---- delete -------------------------------------------------------------------------------

// deleteState is the confirmation dialog of a delete.
type deleteState struct {
	open  bool
	loc   Scope
	keys  []string
	est   core.Estimate
	state int // delCounting, delReady, delFailed
	gen   int
	err   string
	// cancel stops a running Estimate when the dialog closes.
	cancel context.CancelFunc
}

const (
	delCounting = iota
	delReady
	delFailed
)

// askDelete opens the confirmation for the selection. When it holds folders,
// the objects below them are counted in the background; the dialog says
// "Counting…" until then.
func (a *app) askDelete() {
	keys := a.selectedKeys()
	if len(keys) == 0 {
		return
	}
	a.closeDelete()
	d := &a.del
	d.open, d.loc, d.keys, d.state, d.err = true, a.scope, keys, delReady, ""
	d.est = core.Estimate{}
	folders := false
	for _, k := range keys {
		folders = folders || strings.HasSuffix(k, "/")
	}
	if !folders {
		for _, k := range keys {
			d.est.Objects++
			d.est.Bytes += a.rows[a.byKey[k]].Size
		}
		return
	}
	d.state = delCounting
	ctx, cancel := a.timeout(2 * time.Minute)
	d.cancel = cancel
	gen := d.gen
	sel := core.Selection{Location: d.loc, Keys: keys}
	go func() {
		defer cancel()
		est, err := a.svc.Estimate(ctx, sel)
		a.post(func() {
			if gen != a.del.gen || !a.del.open {
				return
			}
			if err != nil {
				a.del.state, a.del.err = delFailed, humanError(err)
				return
			}
			a.del.est, a.del.state = est, delReady
		})
	}()
}

// closeDelete closes the dialog and drops any answer still on its way.
func (a *app) closeDelete() {
	a.del.open = false
	a.del.gen++
	if a.del.cancel != nil {
		a.del.cancel()
		a.del.cancel = nil
	}
}

// summary is the dialog's one-line count.
func (d *deleteState) summary() string {
	switch d.state {
	case delCounting:
		return locales.ActionsCounting()
	case delFailed:
		return locales.ActionsCountUnavailable()
	}
	n, size := locales.CommonObjects(d.est.Objects), humanBytes(d.est.Bytes)
	if d.est.Truncated {
		return locales.ActionsDeleteSummaryAtLeast(n, size)
	}
	return locales.ActionsDeleteSummary(n, size)
}

func (a *app) confirmDelete() {
	d := a.del
	a.closeDelete()
	sel := core.Selection{Location: d.loc, Keys: d.keys}
	a.bg(func() func() {
		id, err := a.svc.Delete(sel)
		return func() {
			if err != nil {
				a.toast(locales.ActionsDeleteFailed(a.noteErr(err, "delete", nil)))
				return
			}
			a.sel.Clear()
			a.cursor = -1
			a.addWatch(id, &watch{loc: d.loc, kind: "delete"})
		}
	})
}

// ---- new folder ------------------------------------------------------------------------------

type folderState struct {
	name string
	err  string // from the service
	busy bool
}

func (a *app) openNewFolder() {
	a.folderDlg = folderState{}
	a.newFolder = true
}

func (a *app) confirmNewFolder() {
	name := strings.TrimSpace(a.folderDlg.name)
	if a.folderError(name) != "" || a.folderDlg.busy {
		return
	}
	loc := a.scope
	a.folderDlg.busy, a.folderDlg.err = true, ""
	go func() {
		ctx, cancel := a.timeout(30 * time.Second)
		defer cancel()
		err := a.svc.CreateFolder(ctx, loc, name)
		a.post(func() {
			a.folderDlg.busy = false
			if err != nil {
				a.folderDlg.err = humanError(err)
				return
			}
			a.newFolder = false
			if loc == a.scope {
				a.selectAfter = loc.Prefix + name + "/"
				a.reload()
			}
			a.toast(locales.ActionsCreatedFolder(name))
		})
	}()
}

// folderError validates a new folder name; "" means valid.
func (a *app) folderError(name string) string {
	switch {
	case name == "":
		return locales.ActionsEnterName()
	case strings.Contains(name, "/"):
		return locales.ActionsNameNoSlash()
	case name == "." || name == "..":
		return locales.ActionsNameNotAllowed()
	case len(name) > 255:
		return locales.ActionsNameTooLong()
	}
	for _, e := range a.raw {
		if e.Folder && e.Name == name {
			return locales.ActionsFolderExists()
		}
	}
	return ""
}

// ---- copying addresses --------------------------------------------------------------------------

func (a *app) objectURI(key string) string { return "s3://" + a.scope.Bucket + "/" + key }

// objectURL is the address the object has when it is public.
func (a *app) objectURL(key string) string { return a.svc.PublicURL(a.scope, key) }

// objectCDN is the CDN link of an object of the open bucket, "" without a CDN.
func (a *app) objectCDN(key string) string { return a.svc.CDNURL(a.scope, key) }

// ---- confirm dialog (generic) ---------------------------------------------------------------------

type confirmState struct {
	open    bool
	title   string
	rows    []KV
	message string
	action  string
	danger  bool
	run     func()
	// dangerRows are the keys of rows shown in the Danger color.
	dangerRows []string
}

func dangerIf(on bool, keys ...string) []string {
	if on {
		return keys
	}
	return nil
}
