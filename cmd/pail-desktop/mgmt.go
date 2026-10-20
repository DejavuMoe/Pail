package main

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/icons"
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/egoist/mygo/ui"
)

// mgmt holds the state of the management features: property, tag and permission
// editing, versions, deleted items, the in-app clipboard, rename and the bucket
// dialogs. It is the single field the shared app struct carries for them.
type mgmt struct {
	follows []follow
	ver     map[string]*verInfo

	props propsForm
	tags  tagsForm
	acl   aclForm
	vers  versionsForm
	del   deletedMode
	clip  clipData
	ren   renameState
	nb    newBucketForm
	db    deleteBucketForm
	eb    emptyBucketForm
	bs    bucketSettings

	dropKey string // the folder row rows are being dragged over
}

// follow calls fn once the queued task id has finished.
type follow struct {
	id   string
	seen bool
	fn   func(t Task)
}

// follow registers fn for task id. Events that arrived before the call are
// folded in first, so a very fast task is not missed.
func (a *app) follow(id string, fn func(Task)) {
	a.mg.follows = append(a.mg.follows, follow{id: id, fn: fn})
	a.applyInbox()
	a.pollFollows()
}

// pollFollows runs the callbacks of the followed tasks that have finished.
func (a *app) pollFollows() {
	if len(a.mg.follows) == 0 {
		return
	}
	keep := make([]follow, 0, len(a.mg.follows))
	var done []func()
	for _, f := range a.mg.follows {
		i, ok := a.taskIdx[f.id]
		switch {
		case ok && a.tasks[i].Status.Finished():
			t, fn := a.tasks[i], f.fn
			done = append(done, func() { fn(t) })
		case ok:
			f.seen = true
			keep = append(keep, f)
		case f.seen: // dropped from the list (Clear finished): nothing to report
		default:
			keep = append(keep, f)
		}
	}
	a.mg.follows = keep
	for _, d := range done {
		d()
	}
}

// bulkSummary counts the outcome of a finished bulk task.
type bulkSummary struct {
	ok, skipped, failed int
	firstErr            string
}

func (a *app) bulkSummary(t Task) bulkSummary {
	var s bulkSummary
	for _, it := range a.svc.TaskItems(t.ID) {
		switch it.Status {
		case core.ItemSucceeded:
			s.ok++
		case core.ItemSkipped:
			s.skipped++
		default:
			s.failed++
			if s.firstErr == "" {
				s.firstErr = it.Err
			}
		}
	}
	if s.firstErr == "" {
		s.firstErr = t.Err
	}
	return s
}

// mgFrame runs once per frame after the view: task callbacks and the cached
// bucket facts the open views need.
func (a *app) mgFrame(c *ui.Context) {
	a.pollFollows()
	if a.browsing() {
		a.ensureVersioning(a.accountID, a.scope.Bucket)
	}
}

// mgModalOpen reports whether one of the management dialogs holds the keyboard.
func (a *app) mgModalOpen() bool {
	m := &a.mg
	return m.nb.open || m.db.open || m.eb.open || m.bs.open
}

// mgTyping reports whether an inline text field of these features is editing,
// so that the window's shortcut keys leave the keyboard to it.
func (a *app) mgTyping() bool {
	m := &a.mg
	return m.props.edit.id != "" || m.tags.edit.id != "" || m.ren.key != ""
}

// ---- cached bucket versioning ----------------------------------------------------------

type verInfo struct {
	state  int // 1 loading, 2 ready, 3 unknown (could not be read)
	status string
}

func verKey(acct, bucket string) string { return acct + "|" + bucket }

// versioningOf returns the cached versioning status of a bucket ("Enabled",
// "Suspended" or "") and whether it is known.
func (a *app) versioningOf(acct, bucket string) (string, bool) {
	if v := a.mg.ver[verKey(acct, bucket)]; v != nil && v.state == 2 {
		return v.status, true
	}
	return "", false
}

// versioned is true once the bucket has had versioning enabled.
func (a *app) versioned(acct, bucket string) bool {
	st, _ := a.versioningOf(acct, bucket)
	return st == "Enabled" || st == "Suspended"
}

func (a *app) ensureVersioning(acct, bucket string) {
	if acct == "" || bucket == "" {
		return
	}
	if a.mg.ver == nil {
		a.mg.ver = map[string]*verInfo{}
	}
	k := verKey(acct, bucket)
	if a.mg.ver[k] != nil {
		return
	}
	a.mg.ver[k] = &verInfo{state: 1}
	a.bg(func() func() {
		ctx, cancel := a.timeout(30 * time.Second)
		defer cancel()
		js, err := a.svc.GetBucketConfig(ctx, acct, bucket, core.ConfigVersioning)
		return func() {
			if err != nil {
				a.mg.ver[k] = &verInfo{state: 3}
				return
			}
			a.mg.ver[k] = &verInfo{state: 2, status: parseVersioning(js)}
		}
	})
}

func (a *app) invalidateVersioning(acct, bucket string) { delete(a.mg.ver, verKey(acct, bucket)) }

func parseVersioning(js string) string {
	var v struct{ Status string }
	_ = json.Unmarshal([]byte(js), &v)
	return v.Status
}

// ---- small shared widgets --------------------------------------------------------------

// inlineEdit is the one text field that is being edited in place in a view.
type inlineEdit struct {
	id    string
	draft string
	fresh int // frames to wait before a lost focus ends the edit
}

func (e *inlineEdit) start(id, draft string) { e.id, e.draft, e.fresh = id, draft, 3 }
func (e *inlineEdit) stop()                  { e.id = "" }
func (e *inlineEdit) on(id string) bool      { return e.id == id }

// inlineInput draws the edit field and reports whether the edit ended and, if
// it did, whether the text typed is kept (Enter, focus moved away) or not
// (Escape).
func inlineInput(c *ui.Context, e *inlineEdit, label, placeholder string, o inputOpts) (done, keep bool) {
	o.focus = true
	in := inputBox(c, &e.draft, label, placeholder, o)
	switch {
	case in.Submitted():
		return true, true
	case in.Shortcut(0, ui.KeyEscape):
		return true, false
	case e.fresh > 0:
		e.fresh--
	case !in.Focused():
		return true, true
	}
	return false, false
}

// valueButton is a click-to-edit value: plain text that gains a hover fill when
// it can be edited. It returns the button for Clicked.
func valueButton(c *ui.Context, key, text string, subtle, monospace, editable bool, tip string) ui.Element {
	k := tk(c)
	label := tip
	if !editable {
		label = text
	}
	b := ui.ButtonBase(c.Key(key)).Label(label).Height(24).Padding(0, 6).Margin(0, 0, 0, -6).Radius(6).AlignItems(ui.Center).Justify(ui.Start).MinWidth(0).Disabled(!editable)
	if tip != "" && editable {
		b.Tooltip(tip)
	}
	if editable && b.Hovered() {
		b.Background(k.SurfaceHover)
	}
	return b.Children(func() {
		col := k.Text
		if subtle {
			col = k.TextSubtle
		}
		t := ui.Text(c, text).FontSize(sizeBody).TextColor(col).SingleLine().MinWidth(0)
		if monospace {
			mono(t.FontSize(sizeSecondary))
		}
	})
}

// badge is a small neutral pill ("Latest", "Deleted").
func badge(c *ui.Context, label string) ui.Element {
	k := tk(c)
	return ui.Box(c.Key("badge:"+label)).Padding(1, 6).Radius(4).Background(k.ActiveFill).Shrink(0).Center().Children(func() {
		ui.Text(c, label).FontSize(sizeLabel).FontWeight(500).TextColor(k.TextMuted).SingleLine()
	})
}

// noteLine is a muted one-line note with an optional leading icon.
func noteLine(c *ui.Context, s string) {
	mutedText(c, s).SingleLine()
}

// errorLine is a danger icon with a wrapping message.
func errorLine(c *ui.Context, msg string) {
	resultLine(c, "circle-alert", tk(c).Danger, msg)
}

// plainError words a service error for a dialog: the three kinds the design
// names get fixed sentences, the rest keep the service's message.
func plainError(err error, what string) string {
	switch core.KindOf(err) {
	case core.ErrAccessDenied:
		return locales.MgmtNoPermission(what)
	case core.ErrUnsupported:
		return locales.MgmtNotSupported()
	}
	return humanError(err)
}

// timeoutCtx is a short request context for the bucket settings.
func (a *app) shortCtx() (context.Context, context.CancelFunc) { return a.timeout(30 * time.Second) }

// scopeRows are the account and bucket rows every confirmation starts with.
func (a *app) scopeRows(loc Scope) []KV {
	return []KV{{locales.CommonAccount(), a.account().Name}, {locales.CommonBucket(), loc.Bucket}}
}

// keyBlock returns a one-line description of keys for a confirmation: the
// first key, or "N items".
func describeKeys(keys []string) string {
	if len(keys) == 1 {
		return keys[0]
	}
	return locales.CommonItems(len(keys))
}

func baseName(key string) string {
	k := strings.TrimSuffix(key, "/")
	if i := strings.LastIndexByte(k, '/'); i >= 0 {
		k = k[i+1:]
	}
	return k
}

// shortVersion shortens a version ID for display.
func shortVersion(id string) string {
	if id == "" || id == "null" {
		return "null"
	}
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func iconOf(name string) *ui.SVG { return icons.Get(name) }
