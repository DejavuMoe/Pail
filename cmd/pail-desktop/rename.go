package main

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/egoist/mygo/ui"
)

// renameState is the inline rename of one row of the object table.
type renameState struct {
	key   string // the row being renamed, "" when none
	name  string
	err   string
	fresh int // frames before a lost focus ends the edit
}

func canRename(a *app) bool {
	return a.browsing() && len(a.selectedKeys()) == 1 && !a.mg.del.on && !a.objOps.flat // a flat row lives in another folder
}

// startRename opens the name of the selected row for editing.
func (a *app) startRename() {
	keys := a.selectedKeys()
	if len(keys) != 1 || a.mg.del.on || a.objOps.flat {
		return
	}
	e := a.rows[a.byKey[keys[0]]]
	a.mg.ren = renameState{key: e.Key, name: e.Name, fresh: 3}
}

func (a *app) cancelRename() { a.mg.ren = renameState{} }

// renameError validates the new name of e; "" means valid.
func (a *app) renameError(e Entry, name string) string {
	switch {
	case name == "":
		return locales.RenameEnterName()
	case strings.Contains(name, "/"):
		return locales.RenameNoSlash()
	case name == "." || name == "..":
		return locales.RenameNotAllowed()
	case len(name) > 255:
		return locales.RenameTooLong()
	case strings.ContainsAny(name, "\x00\r\n"):
		return locales.RenameControlChars()
	}
	if name != e.Name {
		for _, o := range a.raw {
			if o.Folder == e.Folder && o.Name == name {
				if e.Folder {
					return locales.RenameFolderExists()
				}
				return locales.RenameFileExists()
			}
		}
	}
	return ""
}

// commitRename renames the row. An unchanged name just closes the field.
func (a *app) commitRename(fromEnter bool) {
	r := &a.mg.ren
	i, ok := a.byKey[r.key]
	if !ok {
		a.cancelRename()
		return
	}
	e := a.rows[i]
	name := strings.TrimSpace(r.name)
	if name == e.Name {
		a.cancelRename()
		return
	}
	if msg := a.renameError(e, name); msg != "" {
		if !fromEnter {
			a.cancelRename()
			return
		}
		r.err = msg
		a.toast(msg)
		return
	}
	loc, key := a.scope, e.Key
	a.cancelRename()
	a.bg(func() func() {
		ctx, cancel := a.timeout(30 * time.Second)
		defer cancel()
		id, err := a.svc.Rename(ctx, loc, key, name)
		return func() {
			if err != nil {
				a.toast(locales.RenameFailed(humanError(err)))
				return
			}
			a.follow(id, func(t Task) {
				sum := a.bulkSummary(t)
				if t.Status != core.StatusSucceeded || sum.failed > 0 {
					msg := sum.firstErr
					if msg == "" {
						msg = t.Err
					}
					a.toast(locales.RenameFailed(msg))
					a.reloadSame(loc)
					return
				}
				if loc == a.scope {
					sel := loc.Prefix + name
					if isFolderKey(key) {
						sel += "/"
					}
					a.selectAfter = sel
					a.headKey = ""
				}
				a.reloadSame(loc)
				a.toast(locales.RenameDone(name))
			})
		}
	})
}

// renameInput is the name cell while its row is being renamed.
func (a *app) renameInput(c *ui.Context, e Entry) {
	k := tk(c)
	r := &a.mg.ren
	invalid := r.err != "" || (strings.TrimSpace(r.name) != e.Name && a.renameError(e, strings.TrimSpace(r.name)) != "")
	box := ui.Row(c.Key("rename")).Height(24).Padding(0, 6).Radius(6).AlignItems(ui.Center).Background(k.Surface).Grow(1).MinWidth(0).Margin(-3, 0)
	if invalid {
		box.Border(1, k.Danger)
	} else {
		box.Border(1, k.Focus)
	}
	var in ui.Element
	box.Children(func() {
		in = ui.TextInputBase(c.Key("rename-in"), &r.name).Label(locales.RenameLabel()).AutoFocus().FontSize(sizeBody).Grow(1).MinWidth(0)
	})
	if in.Changed() {
		r.err = ""
	}
	switch {
	case in.Submitted():
		a.commitRename(true)
	case in.Shortcut(0, ui.KeyEscape):
		a.cancelRename()
	case r.fresh > 0:
		// Select the base name, not the extension, while the field settles.
		in.SetTextSelection(0, utf8.RuneCountInString(renameStem(e)))
		r.fresh--
	case !in.Focused():
		a.commitRename(false)
	}
}

// renameStem is the part of the name a rename selects first: the whole name of
// a folder, the name without its last extension for a file (dotfiles keep it).
func renameStem(e Entry) string {
	if e.Folder {
		return e.Name
	}
	if i := strings.LastIndex(e.Name, "."); i > 0 {
		return e.Name[:i]
	}
	return e.Name
}
