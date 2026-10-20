package main

import (
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/icons"
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	"github.com/egoist/mygo/ui"
)

// topBar is the 44px row across the top of the main area: the command row of
// the object browser, or the header of a tool view. Empty space in it drags
// the window and a double click maximizes it; it keeps clear of the system
// window buttons at its right end.
func (a *app) topBar(c *ui.Context) {
	if a.tool != "" {
		a.topBarRow(c, locales.ToolbarTool(), func() { a.toolHeader(c) }, func() { a.barRight(c) })
		return
	}
	a.topBarRow(c, locales.ToolbarCommands(), func() { a.toolbarCommands(c) }, func() { a.barRight(c) })
}

// topBarRow lays out left, a flexible gap, the panel toggles, the "more" menu
// and the room of the window controls.
func (a *app) topBarRow(c *ui.Context, label string, left, right func()) {
	k := tk(c)
	bar := c.TitleBar()
	ui.Row(c).Height(topBarH).Shrink(0).Padding(0, 12+bar.Right, 0, 12).Gap(2).MinWidth(0).AlignItems(ui.Center).
		Background(k.Background).BorderWidth(0, 0, 1, 0).BorderColor(k.Border).Label(label).DragWindow().Children(func() {
		if left != nil {
			left()
		}
		ui.Spacer(c)
		right()
	})
}

func (a *app) toolbarCommands(c *ui.Context) {
	a.uploadMenu(c)
	a.cmdButton(c, "download", true)
	a.cmdButton(c, "newfolder", true)
	toolbarGap(c)
	a.cmdButton(c, "copy", false)
	a.cmdButton(c, "cut", false)
	a.cmdButton(c, "paste", false)
	a.cmdButton(c, "rename", false)
	a.cmdButton(c, "delete", false)
	toolbarGap(c)
	a.cmdButton(c, "refresh", false)
}

func toolbarGap(c *ui.Context) { vline(c, 16).Margin(0, 6) }

// uploadMenu is the Upload button with its file/folder menu.
func (a *app) uploadMenu(c *ui.Context) {
	files, folder := a.cmd("upload.files"), a.cmd("upload.folder")
	text := locales.ToolbarUpload()
	if a.lay.tbCompact {
		text = ""
	}
	b := ghostButton(c, icons.Get(files.Icon), text, files.tooltip(), !a.enabled(files)).Label(locales.ToolbarUpload())
	b.Menu(func(m *ui.Menu) {
		for _, cmd := range []*command{files, folder} {
			item := m.Item(cmd.text()).Disabled(!a.enabled(cmd))
			item.Shortcut(cmd.Chords[0].Mods, cmd.Chords[0].Key)
			if item.Chosen() {
				a.run(cmd.ID, "toolbar")
			}
		}
	})
}

// cmdButton is a ghost toolbar button for a command; icon only unless label.
func (a *app) cmdButton(c *ui.Context, id string, label bool) {
	cmd := a.cmd(id)
	text := ""
	if label && !a.lay.tbCompact {
		text = cmd.text()
	}
	b := ghostButton(c, icons.Get(cmd.Icon), text, cmd.tooltip(), !a.enabled(cmd))
	if text == "" {
		b.Label(cmd.text())
	}
	b.OnClick(func() { a.run(id, "toolbar") })
}

// toggleButton is an icon-only panel toggle.
func (a *app) toggleButton(c *ui.Context, id string, on bool) {
	cmd := a.cmd(id)
	toggleIconButton(c, icons.Get(cmd.Icon), cmd.text(), cmd.tooltip(), on).OnClick(func() { a.run(id, "toolbar") })
}
