package main

import (
	"strconv"
	"time"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/icons"
	"github.com/egoist/mygo/ui"
)

// The motion of the in-window menus (ctxmenu.go): each panel of the chain comes
// in on the overlay spring from where it hangs, and a menu that closes fades
// out as a copy, because the layer that held it must go the moment it closes.

// menuEnter brings panel, level li of the open menu, in: scaled from overlayFrom
// about the corner it hangs from, moved popRise toward its anchor (a menu
// that flips above its anchor comes from below), a submenu moved
// submenuSlide out of its parent. Where mygo will put the panel is not known
// before the first layout, so it is predicted from the anchor and the size of
// the menu (flips).
func (a *app) menuEnter(c *ui.Context, panel ui.Element, li int) {
	m := &a.popup
	lv := m.levels[li]
	winW, winH := c.Size()
	w := max(lv.box.W, menuMinW)
	h := min(menuHeight(lv.items), winH-2*menuWindowMargin)
	var dx, dy, ox, oy float32
	switch {
	case li > 0:
		row := m.levels[li-1].rects[lv.parent]
		if flips(row.X+row.W, row.X-w, w, winW) {
			dx, ox = submenuSlide, 1
		} else {
			dx = -submenuSlide
		}
	case m.point:
		flipX := flips(m.x, m.x-w, w, winW)
		flipY := flips(m.y, m.y-h, h, winH)
		dx, dy, ox, oy = 0, -popRise, b2f(flipX), b2f(flipY)
		if flipY {
			dy = popRise
		}
	default:
		b := m.anchor.Bounds()
		flipX := flips(b.X, b.X+b.W-w, w, winW)
		flipY := flips(b.Y+b.H+4, b.Y-h-4, h, winH)
		dy, ox, oy = -popRise, b2f(flipX), b2f(flipY)
		if flipY {
			dy = popRise
		}
	}
	overlayEnter(c, panel, dx, dy, ox, oy)
}

func b2f(b bool) float32 {
	if b {
		return 1
	}
	return 0
}

// menuGhost is what a menu that just closed showed: it is drawn again, without
// life, for durExit while it fades out.
type menuGhost struct {
	at     time.Time
	levels []ghostLevel
}

type ghostLevel struct {
	items []*menuItem
	cur   int
	box   ui.Rect
	slot  bool
}

// menuGhostSlack is how long past its fade a ghost lingers before it is
// dropped, so that the last frame of the fade is drawn.
const menuGhostSlack = 40 * time.Millisecond

// menuLeaves notes the menu that closes (a.popup, in this frame still as the
// last frame showed it) as a ghost to fade out. A menu whose panels were never
// laid out leaves none.
func (a *app) menuLeaves(c *ui.Context) {
	m := &a.popup
	if a.opts.still {
		return
	}
	g := &menuGhost{at: c.Now()}
	for _, lv := range m.levels {
		if lv.box.W <= 0 || lv.box.H <= 0 {
			return
		}
		slot := false
		for _, it := range lv.items {
			slot = slot || it.icon != ""
		}
		g.levels = append(g.levels, ghostLevel{items: lv.items, cur: lv.cur, box: lv.box, slot: slot})
	}
	a.popGhost = g
}

// menuGhostLayer draws the ghost of the menu that closed, if one still fades:
// panels in the look of the real ones at the places they were, in a layer that
// takes neither the pointer nor the keys (so nothing waits for it), fading out
// in durExit with no bounce. It goes before a menu opening in the same frame,
// which is built after it and shows above.
func (a *app) menuGhostLayer(c *ui.Context) {
	g := a.popGhost
	if g == nil {
		return
	}
	left := durExit + menuGhostSlack - c.Now().Sub(g.at)
	if left <= 0 {
		a.popGhost = nil
		return
	}
	c.After(left)
	ui.Overlay(c, func() {
		host := ui.Box(c.Key("menu-ghost")).Absolute().Left(0).Top(0).Right(0).Bottom(0).PassThrough()
		leavePose(c, host, ui.Pose{Fade: 1}, exitAnim)
		host.Children(func() {
			for li, lv := range g.levels {
				panel := ui.Column(c.Key("menu-ghost:" + strconv.Itoa(li))).Absolute().Left(lv.box.X).Top(lv.box.Y).Width(lv.box.W).Height(lv.box.H).Clip().PassThrough()
				overlayPanel(c, panel, overlayPopover)
				panel.Children(func() {
					ui.Column(c).Padding(4).PassThrough().Children(func() {
						for i, it := range lv.items {
							if it.separator {
								listSeparator(c)
								continue
							}
							var ic *ui.SVG
							if it.icon != "" {
								ic = icons.Get(it.icon)
							}
							shortcut := ""
							if it.shortcut != nil {
								shortcut = it.shortcut.String()
							}
							listRow(c, ui.Row(c).PassThrough(), rowSpec{
								Icon: ic, IconSlot: lv.slot, Label: it.label, Shortcut: shortcut,
								Checked: it.checked, Disabled: it.disabled, Highlighted: lv.cur == i && !it.disabled, Submenu: it.submenu,
							})
						}
					})
				})
			}
		})
	})
}
