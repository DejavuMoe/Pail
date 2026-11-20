package main

import (
	"context"
	"strings"
	"time"

	core "github.com/DejavuMoe/Pail/internal/app"
)

func (a *app) resetHead() {
	if a.cancelHead != nil {
		a.cancelHead()
		a.cancelHead = nil
	}
	a.headGen++
	a.headKey, a.headState, a.headErr = "", loadNone, ""
	a.resetPreview()
	a.mg.tags = tagsForm{}
	// The Permissions and Versions tabs describe the same object as the head:
	// another object (perhaps of the same name in another bucket) or a new
	// version of this one loads them again. The generations move on, so a
	// reply still on its way is dropped.
	a.mg.acl = aclForm{gen: a.mg.acl.gen + 1}
	a.mg.vers = versionsForm{gen: a.mg.vers.gen + 1}
}

// headTouched is told that an operation in loc has ended which may have
// changed keys (and what lies under the folder keys among them; all: the whole
// of loc's folder). The details on show are read again only when they are
// those of one of these objects: an operation never reloads, and so never
// resets, the forms of another object chosen while it ran. Unsaved edits of
// the object itself are kept (refreshHead).
func (a *app) headTouched(loc Scope, all bool, keys ...string) {
	if !a.headShows(loc, all, keys...) {
		return
	}
	if a.headState != loadReady || !a.headDrafts() {
		a.headKey = "" // nothing to keep: the next frame reads it from scratch
		return
	}
	a.refreshHead()
}

// headShows reports whether the details on show are those of one of keys in
// loc (see headTouched).
func (a *app) headShows(loc Scope, all bool, keys ...string) bool {
	k := a.headKey
	if k == "" || loc.AccountID != a.scope.AccountID || loc.Bucket != a.scope.Bucket {
		return false
	}
	if all && strings.HasPrefix(k, loc.Prefix) {
		return true
	}
	for _, x := range keys {
		if x == k || strings.HasSuffix(x, "/") && strings.HasPrefix(k, x) {
			return true
		}
	}
	return false
}

// headDrafts reports whether the Headers or Tags form of the object on show
// holds something a reload would lose: edits, an open field, a save under way.
func (a *app) headDrafts() bool {
	p := &a.mg.props
	if p.ready && !p.multi && p.gen == a.headGen && p.sig == a.selectionSig() && (p.dirty() || p.edit.id != "" || p.saving) {
		return true
	}
	t := &a.mg.tags
	return t.ready && t.key == a.headKey && t.gen == a.headGen && (t.dirty() || t.edit.id != "" || t.saving)
}

// refreshHead reads the details on show again without starting the forms over:
// when they arrive, the Headers and Tags forms take them as their base and
// keep their edits on top. What has no edits (preview, permissions, versions)
// loads again as after any other change of the object.
func (a *app) refreshHead() {
	a.headSeq++
	seq, gen, key, scope := a.headSeq, a.headGen, a.headKey, a.scope
	a.resetPreview()
	a.mg.acl = aclForm{gen: a.mg.acl.gen + 1}
	a.mg.vers = versionsForm{gen: a.mg.vers.gen + 1}
	a.bg(func() func() {
		ctx, cancel := a.timeout(30 * time.Second)
		defer cancel()
		info, err := a.svc.Head(ctx, scope, key, "")
		return func() {
			if seq != a.headSeq || gen != a.headGen || key != a.headKey || err != nil {
				return // superseded; on a failure the details and edits on show stay
			}
			a.head = info
			if p := &a.mg.props; p.ready && !p.multi && p.gen == gen && p.sig == a.selectionSig() {
				var q propsForm
				q.build([]core.ObjectInfo{info}, false, 0)
				p.rebase(&q)
			}
			if t := &a.mg.tags; t.ready && t.key == key && t.gen == gen {
				rebaseTags(t, info.Tags)
			}
		}
	})
}

// ensureHead starts loading the details of the single selected object.
func (a *app) ensureHead() {
	key, one := a.soleSelected()
	if !one || strings.HasSuffix(key, "/") {
		if a.headKey != "" {
			a.resetHead()
		}
		return
	}
	if key == a.headKey {
		return
	}
	a.resetHead()
	a.headKey, a.headState = key, loadBusy
	gen, scope := a.headGen, a.scope
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	a.cancelHead = cancel
	a.bg(func() func() {
		defer cancel()
		info, err := a.svc.Head(ctx, scope, key, "")
		return func() {
			if gen != a.headGen {
				return
			}
			if err != nil {
				a.headState, a.headErr = loadFailed, a.noteErr(err, "head", func() {
					if gen == a.headGen {
						a.resetHead() // the next frame asks again
					}
				})
				return
			}
			a.head, a.headState = info, loadReady
		}
	})
}
