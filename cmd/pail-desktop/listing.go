package main

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"time"

	core "github.com/DejavuMoe/Pail/internal/app"
)

// reload lists the current scope again: what it shows may have changed, so the
// folder sizes it could have made wrong go (dropSizes).
func (a *app) reload() {
	if a.scope.Bucket != "" {
		a.dropSizes(a.scope)
	}
	a.listScope()
}

// listScope lists the current scope page by page. The old rows stay on screen
// until the first page arrives, and the table fills as pages come in; every
// further page is requested right away until the listing ends.
func (a *app) listScope() {
	if a.scope.Bucket == "" || a.reloadHook() {
		return
	}
	if a.cancelList != nil {
		a.cancelList()
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.cancelList = cancel
	a.listGen++
	gen, scope, flat := a.listGen, a.scope, a.objOps.flat
	quiet, began, acct := a.listQuiet, time.Now(), a.account()
	// What the list of recent operations says of this listing, however it ends;
	// each ending takes its own copy.
	base := opEntry{what: tookList, ran: true, account: scope.AccountID, accountName: acct.Name, where: scopeText(scope)}
	a.loading, a.listErr = true, ""
	go func() {
		token := ""
		for first := true; ; first = false {
			page, err := a.svc.List(ctx, scope, core.ListOptions{Token: token, Flat: flat})
			if ctx.Err() != nil {
				// Left before it ended: another folder was opened, or the account changed.
				if !quiet {
					left := base
					left.state, left.d = opCanceled, time.Since(began)
					a.post(func() { a.noteOp(left) })
				}
				return
			}
			if flat && err == nil {
				page.Entries = flatEntries(page.Entries, scope.Prefix)
			}
			isFirst, took := first, time.Since(began)
			a.post(func() {
				if gen != a.listGen {
					return
				}
				if err != nil {
					a.listErr, a.loading = a.noteErr(err, "list", func() {
						if gen == a.listGen {
							a.reload()
						}
					}), false
					if !quiet {
						failed := base
						failed.state, failed.d, failed.err = opFailed, took, a.listErr
						a.noteOp(failed)
					}
					return
				}
				if isFirst {
					a.raw = page.Entries
				} else {
					a.raw = append(a.raw, page.Entries...)
				}
				a.rowsDirty = true
				if page.NextToken == "" {
					a.loading = false
					if !quiet {
						done := base
						done.d, done.n = took, len(a.raw)
						a.noteOp(done)
					}
					a.pruneSelection()
					a.applySelectAfter()
				}
			})
			if err != nil || page.NextToken == "" {
				return
			}
			token = page.NextToken
		}
	}()
}

// applySelectAfter selects the key a finished action asked for (a new folder).
func (a *app) applySelectAfter() {
	if a.selectAfter == "" {
		return
	}
	if _, ok := a.byKey[a.selectAfter]; ok {
		a.setSingle(a.selectAfter)
	}
	a.selectAfter = ""
}

// pruneSelection drops selected keys that are no longer listed.
func (a *app) pruneSelection() {
	a.rebuildRows()
	var gone []string
	for k := range a.sel.All() {
		if _, ok := a.byKey[k]; !ok {
			gone = append(gone, k)
		}
	}
	for _, k := range gone {
		a.sel.Remove(k)
	}
}

// rebuildEvery is how often the rows are rebuilt while a listing is still
// arriving. Each rebuild sorts everything loaded so far, so doing it for every
// page of a huge folder would cost more than loading it.
const rebuildEvery = 250 * time.Millisecond

// rowsDue reports whether this frame should rebuild the rows. A changed sort,
// search or a finished listing rebuilds at once; pages that arrive during a
// listing wait for the next rebuildEvery, and a timer brings that frame about
// when no other event does.
func (a *app) rowsDue() bool {
	if !a.rowsDirty && a.sort == a.sortSeen {
		return false
	}
	if !a.loading || a.sort != a.sortSeen || a.rebuiltAt.IsZero() {
		return true
	}
	wait := rebuildEvery - time.Since(a.rebuiltAt)
	if wait <= 0 {
		return true
	}
	if !a.rebuildWake {
		a.rebuildWake = true
		time.AfterFunc(wait, func() { a.post(func() { a.rebuildWake = false }) })
	}
	return false
}

// rebuildRows filters and sorts raw into rows: folders always first.
func (a *app) rebuildRows() {
	a.selSum.ok = false // which chosen rows show depends on the rows
	q := strings.ToLower(strings.TrimSpace(a.search))
	rows := make([]Entry, 0, len(a.raw))
	for _, e := range a.raw {
		if q == "" || strings.Contains(strings.ToLower(e.Name), q) {
			rows = append(rows, e)
		}
	}
	desc := a.sort.Descending
	col := a.sort.Column
	slices.SortStableFunc(rows, func(x, y Entry) int {
		if x.Folder != y.Folder {
			if x.Folder {
				return -1
			}
			return 1
		}
		d := 0
		if x.Folder && col == "size" {
			// Folders whose size is known sort by it; the others, as if empty.
			xs, _ := a.knownFolderSize(x.Key)
			ys, _ := a.knownFolderSize(y.Key)
			if d = cmp.Compare(xs, ys); desc {
				d = -d
			}
		}
		if !x.Folder {
			switch col {
			case "size":
				d = cmp.Compare(x.Size, y.Size)
			case "type":
				d = strings.Compare(extOf(x), extOf(y))
			case "modified":
				d = x.Modified.Compare(y.Modified)
			case "class":
				d = strings.Compare(x.StorageClass, y.StorageClass)
			}
			if desc {
				d = -d
			}
		}
		if d == 0 {
			// Ties (and folders) fall back to the name; only the name
			// column itself reverses with the sort direction.
			d = compareNames(x.Name, y.Name)
			if col == "name" && desc {
				d = -d
			}
		}
		return d
	})
	a.rows = rows
	a.byKey = make(map[string]int, len(rows))
	for i, e := range rows {
		a.byKey[e.Key] = i
	}
	a.sel.touch() // which chosen keys are listed, and their rows, may differ
	a.rowsDirty = false
	a.sortSeen = a.sort
	a.rebuiltAt = time.Now()
}
