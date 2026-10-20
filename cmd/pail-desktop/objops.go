package main

import (
	"context"
	"strings"
	"time"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/egoist/mygo/ui"
)

// objOpsState holds the state of the object-operation features: the storage class and
// encryption dialogs, the bucket copy / move dialog, the signed URL dialog and
// the flat listing. It is the one field the app struct carries for them.
type objOpsState struct {
	rw rewriteForm
	bc bucketCopyForm
	sg signedForm

	flat bool // the table lists every object below the open folder
}

// objOpsModalOpen reports whether one of these dialogs holds the keyboard.
func (a *app) objOpsModalOpen() bool {
	m := &a.objOps
	return m.rw.open || m.bc.open || m.sg.open
}

// objOpsDialogs draws the object-operation dialogs.
func (a *app) objOpsDialogs(c *ui.Context) {
	a.rewriteDialog(c)
	a.bucketCopyDialog(c)
	a.signedDialog(c)
}

// ---- scope counts ------------------------------------------------------------------------------

// estState is the count shown in a confirmation: "Counting…" while the service
// walks the scope, then "N objects · size".
type estState struct {
	state  int // delCounting, delReady, delFailed
	est    core.Estimate
	err    string
	gen    int
	cancel context.CancelFunc
}

// stop drops a count still on its way.
func (e *estState) stop() {
	e.gen++
	if e.cancel != nil {
		e.cancel()
		e.cancel = nil
	}
}

// summary is the one-line count; "at least" when the walk stopped early.
func (e *estState) summary() string {
	switch e.state {
	case delCounting:
		return locales.ScopeCountCounting()
	case delFailed:
		return locales.ScopeCountCountUnavailable()
	}
	n, size := locales.CommonObjects(e.est.Objects), humanBytes(e.est.Bytes)
	if e.est.Truncated {
		return locales.ScopeCountSummaryAtLeast(n, size)
	}
	return locales.ScopeCountSummary(n, size)
}

// countScope starts counting sel. known, when not nil, is an answer the caller
// already has (a selection of listed objects), which needs no request.
func (a *app) countScope(e *estState, sel core.Selection, known *core.Estimate) {
	e.stop()
	e.err = ""
	if known != nil {
		e.est, e.state = *known, delReady
		return
	}
	e.est, e.state = core.Estimate{}, delCounting
	gen := e.gen
	ctx, cancel := a.timeout(2 * time.Minute)
	e.cancel = cancel
	a.bg(func() func() {
		defer cancel()
		est, err := a.svc.Estimate(ctx, sel)
		return func() {
			if gen != e.gen {
				return
			}
			if err != nil {
				e.state, e.err = delFailed, humanError(err)
				return
			}
			e.est, e.state = est, delReady
		}
	})
}

// countRow draws the summary line with its spinner while counting.
func countRow(c *ui.Context, e *estState) {
	k := tk(c)
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		if e.state == delCounting {
			ui.Spinner(c).Size(12, 12)
		}
		ui.Text(c, e.summary()).FontSize(sizeBody).FontWeight(500).TextColor(k.Text)
	})
}

// selectionEstimate answers the count of the selected keys from the listing
// when they are all objects; folders need the service (nil).
func (a *app) selectionEstimate(keys []string) *core.Estimate {
	est := core.Estimate{}
	for _, k := range keys {
		if isFolderKey(k) {
			return nil
		}
		i, ok := a.byKey[k]
		if !ok {
			return nil
		}
		est.Objects++
		est.Bytes += a.rows[i].Size
	}
	return &est
}

// ---- shared dialog parts -------------------------------------------------------------------------

// keyListBlock is the dialog's list of keys: Sidebar fill, 12px mono, five
// lines and then "and N more".
func keyListBlock(c *ui.Context, keys []string) {
	k := tk(c)
	ui.Column(c).Gap(2).Padding(8, 10).Radius(6).Background(k.Sidebar).Children(func() {
		for i, key := range keys {
			if i == 5 {
				ui.Text(c, locales.ScopeCountMore(groupInt(len(keys)-5))).FontSize(sizeSecondary).TextColor(k.TextMuted)
				break
			}
			mono(ui.Text(c.Key(i), key).FontSize(sizeSecondary).TextColor(k.Text).SingleLine())
		}
	})
}

// endpointHTTPS reports whether the account sends its requests over https:
// AWS accounts without a custom endpoint always do.
func endpointHTTPS(ac Account) bool {
	ep := strings.TrimSpace(ac.Endpoint)
	if ep == "" {
		return true
	}
	return strings.HasPrefix(strings.ToLower(ep), "https://")
}

// bucketRoot is the scope of a whole bucket of the current account.
func (a *app) bucketRoot(name string) Scope { return Scope{AccountID: a.accountID, Bucket: name} }
