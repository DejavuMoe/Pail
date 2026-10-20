package main

import (
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	"github.com/egoist/mygo"
)

// nativeMenu builds the File/Edit/View/Bucket/Tools/Help bar from the
// command table. It is installed on macOS only (installMenu); Windows and
// Linux reach the same commands through the toolbar, the "more" menu and the
// command palette. Items run through app.run on the main thread, which is the
// thread the view builds on. Only accelerators that no text field needs are
// registered natively; the rest work as in-window shortcuts.
func (a *app) nativeMenu(redraw func()) *mygo.Menu {
	item := func(id string) *mygo.MenuItem {
		cmd := a.cmd(id)
		return &mygo.MenuItem{
			ID: id, Label: cmd.text(), Accelerator: cmd.Accel, Disabled: !a.enabled(cmd),
			Click: func(*mygo.MenuItem, *mygo.Window) { a.run(id, "menu"); redraw() },
		}
	}
	items := func(ids ...string) []*mygo.MenuItem {
		var out []*mygo.MenuItem
		for _, id := range ids {
			if id == "-" {
				out = append(out, mygo.Separator())
			} else {
				out = append(out, item(id))
			}
		}
		return out
	}
	file := items("upload.files", "upload.folder", "download", "newfolder", "-", "vault.lock", "vault.unlock", "-")
	file = append(file, &mygo.MenuItem{Role: mygo.RoleQuit})
	tools := items("tool.sync", "tool.cloudfront", "tool.rules", "tool.mime", "tool.multipart", "-", "tool.settings")
	m := mygo.NewMenu([]*mygo.MenuItem{
		{Role: mygo.RoleAppMenu},
		{Label: locales.MenuFile(), Submenu: file},
		{Label: locales.MenuEdit(), Submenu: items("copy", "cut", "paste", "rename", "delete", "-", "storageclass", "encryption", "copysigned", "copycdn", "opencdn", "-", "selectall", "-", "search")},
		{Label: locales.MenuView(), Submenu: items("refresh", "up", "-", "inspector", "transfers")},
		{Label: locales.MenuBucket(), Submenu: items("newbucket", "newfolder", "-", "bucketsettings")},
		{Label: locales.MenuTools(), Submenu: tools},
		{Label: locales.MenuLanguage(), Submenu: a.languageItems(redraw)},
		{Label: locales.MenuHelp(), Submenu: items("palette", "shortcuts", "docs", "about")},
	})
	a.menu = m
	a.menuState = map[string]bool{}
	return m
}

// syncMenu mirrors command availability into the native menu; it runs every
// frame and only touches items whose state changed.
func (a *app) syncMenu() {
	if a.menu == nil {
		return
	}
	for i := range a.commands {
		cmd := &a.commands[i]
		it := a.menu.ItemByID(cmd.ID)
		if it == nil {
			continue
		}
		on := a.enabled(cmd)
		if prev, seen := a.menuState[cmd.ID]; !seen || prev != on {
			a.menuState[cmd.ID] = on
			it.SetEnabled(on)
		}
	}
}
