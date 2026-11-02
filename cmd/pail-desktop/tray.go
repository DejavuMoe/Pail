package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"runtime"
	"strings"
	"time"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/egoist/mygo"
)

// The icon in the notification area (Windows, Linux) or the menu bar (macOS),
// see docs/dev/design.md "Tray icon". It shows unless Settings turns it off.
// While it shows, closing the window hides it and Pail keeps running, so
// transfers finish; Quit Pail in its menu (or Quit in the app) ends it. Its
// menu is the system's on every platform, as the system draws the menus of
// these icons: Open Pail, the transfers' state, Show transfers, Upload
// files…, Quit Pail. A click on the icon itself opens the window on Windows
// and Linux; on macOS a click opens the menu, as menu bar items do.

// trayUI is the app's side of the icon: what the view and the settings ask
// of it. main.go fills it in; the tests leave the functions nil.
type trayUI struct {
	// set shows the icon or removes it; nil does nothing (tests).
	set func(on bool)
	// on is whether the icon shows: closing the window then keeps Pail running.
	on bool
	// visible reports whether the window shows; nil counts it as shown.
	visible func() bool
	// notify shows a system notification, used for what would be a toast
	// while the window is hidden; nil drops it.
	notify func(msg string)
	// status is the state line last given to the icon, refresh what to call
	// when it changes (nil: no icon yet).
	status  string
	refresh func(status string)
}

// windowHidden reports whether the window is hidden in the tray.
func (a *app) windowHidden() bool { return a.tray.visible != nil && !a.tray.visible() }

// setTrayShown shows or removes the icon and saves the choice.
func (a *app) setTrayShown(on bool) {
	a.tray.on = on
	if a.tray.set != nil {
		a.tray.set(on)
	}
	a.applySetting("tray", func(st *core.Settings) { st.HideTrayIcon = !on }, func() {
		a.tray.on = !on
		if a.tray.set != nil {
			a.tray.set(!on)
		}
	})
}

// trayStatus is the transfers' state as the icon says it: how many run and,
// when it is known, how far they are (the taskbar's progress, so the two
// agree).
func trayStatus(ts []Task) string {
	n := countTasks(ts)[0]
	if n == 0 {
		return locales.TrayIdle()
	}
	if p := taskbarProgress(ts); p.State == mygo.ProgressNormal {
		return locales.TrayActive(n, fmt.Sprint(int(math.Floor(p.Value*100))))
	}
	return locales.TrayActiveBusy(n)
}

// syncTray gives the icon the transfers' state when it changed. It runs after
// queued updates, as syncProgress does, so it keeps going while the window
// is hidden.
func (a *app) syncTray() {
	if a.tray.refresh == nil {
		return
	}
	if s := trayStatus(a.tasks); s != a.tray.status {
		a.tray.status = s
		a.tray.refresh(s)
	}
}

// ---- the system's icon ----------------------------------------------------------------------------

// trayIcon is the icon of main.go: the mygo Tray, its menu, and the window it
// shows. It only runs on the main thread.
type trayIcon struct {
	a      *app
	win    *mygo.Window
	redraw func()
	tray   *mygo.Tray
	menu   *mygo.Menu
}

// installTray makes the icon's controller and shows the icon when on.
func installTray(a *app, win *mygo.Window, redraw func(), on bool) *trayIcon {
	t := &trayIcon{a: a, win: win, redraw: redraw}
	a.tray.on = on
	a.tray.set = t.set
	a.tray.visible = win.IsVisible
	a.tray.notify = notifySystem
	t.set(on)
	return t
}

// set shows the icon or removes it.
func (t *trayIcon) set(on bool) {
	switch {
	case on && t.tray == nil:
		img, template := trayImage()
		t.menu = t.buildMenu()
		tray, err := mygo.NewTray(mygo.TrayOptions{
			ID: "pail", Icon: img, IconIsTemplate: template, ToolTip: "Pail", Menu: t.menu,
		})
		if err != nil {
			// No notification area (a Linux desktop without AppIndicator):
			// closing the window quits, as it did before.
			log.Printf("tray: %v", err)
			t.a.tray.on = false
			return
		}
		t.tray = tray
		tray.OnClick(t.open)
		t.a.tray.status = ""
		t.a.tray.refresh = t.refresh
		t.a.syncTray()
	case !on && t.tray != nil:
		t.a.tray.refresh = nil
		t.tray.Destroy()
		t.tray, t.menu = nil, nil
	}
}

// relabel builds the menu again in the language shown now.
func (t *trayIcon) relabel() {
	if t.tray == nil {
		return
	}
	t.menu = t.buildMenu()
	t.tray.SetMenu(t.menu)
	t.a.tray.status = ""
	t.a.syncTray()
}

func (t *trayIcon) buildMenu() *mygo.Menu {
	a := t.a
	upload := a.cmd("upload.files")
	return mygo.NewMenu([]*mygo.MenuItem{
		{ID: "tray.open", Label: locales.TrayOpen(), Click: func(*mygo.MenuItem, *mygo.Window) { t.open() }},
		mygo.Separator(),
		{ID: "tray.status", Label: trayStatus(a.tasks), Disabled: true},
		{ID: "tray.transfers", Label: locales.TrayShowTransfers(), Click: func(*mygo.MenuItem, *mygo.Window) {
			a.transfersOpen = true
			t.open()
		}},
		mygo.Separator(),
		{ID: "tray.upload", Label: upload.text(), Click: func(*mygo.MenuItem, *mygo.Window) {
			t.open()
			a.run("upload.files", "menu")
			t.redraw()
		}},
		mygo.Separator(),
		{ID: "tray.quit", Label: locales.TrayQuit(), Click: func(*mygo.MenuItem, *mygo.Window) { mygo.App.Quit() }},
	})
}

// refresh shows status in the menu, the tooltip and, on macOS while
// transfers run, as the percentage beside the icon. Upload files… follows
// whether a bucket is open.
func (t *trayIcon) refresh(status string) {
	if t.tray == nil {
		return
	}
	if it := t.menu.ItemByID("tray.status"); it != nil {
		it.SetLabel(status)
	}
	if it := t.menu.ItemByID("tray.upload"); it != nil {
		it.SetEnabled(t.a.enabled(t.a.cmd("upload.files")))
	}
	t.tray.SetToolTip(locales.TrayTooltip(status))
	if runtime.GOOS == "darwin" {
		t.tray.SetTitle(trayTitle(taskbarProgress(t.a.tasks)))
	}
}

// trayTitle is the percentage beside the macOS icon while transfers run, its
// digits padded with figure spaces so that the item keeps its width.
func trayTitle(p mygo.ProgressBar) string {
	if p.State != mygo.ProgressNormal {
		return ""
	}
	s := fmt.Sprintf("%3d%%", int(math.Floor(p.Value*100)))
	return strings.ReplaceAll(s, " ", " ")
}

// open shows the window in front, restored if it was minimized.
func (t *trayIcon) open() {
	if t.win.IsMinimized() {
		t.win.Restore()
	}
	t.win.Show()
	t.win.Focus()
	t.redraw()
}

// notifySystem shows msg as a system notification where the system offers
// them; it waits for the system, so it runs on a goroutine.
func notifySystem(msg string) {
	go func() {
		if !mygo.NotificationsSupported() {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		n := mygo.NewNotification(mygo.NotificationOptions{Title: "Pail", Body: msg, Group: "pail-transfers"})
		if err := n.ShowContext(ctx); err != nil {
			log.Printf("notification: %v", err)
		}
	}()
}

// ---- the image -------------------------------------------------------------------------------

// trayImage is the icon's picture and whether it is a template. macOS and
// Windows show the glyph of assets/brand/pail-tray.svg as a template, which
// the system tints to match the menu bar or the taskbar. Linux panels do not
// tint (AppIndicator says nothing of the panel's colors), so a black glyph
// would vanish on a dark panel: they show the app icon itself.
func trayImage() (img []byte, template bool) {
	if runtime.GOOS == "linux" {
		return appIcon, false
	}
	return trayGlyph(32), true
}

// trayBar is one stratum of the glyph, in the 32-unit grid of pail-tray.svg:
// a pill with a hole (the brand's dot) near its right end.
type trayBar struct{ x, y, w, h, dotX float64 }

// trayBars mirrors assets/brand/pail-tray.svg: the app icon's three strata,
// tapering as they do there (168, 140, 112 → 28, 24, 20), each with its dot.
// Every edge is on an even unit, so that at 16px (a taskbar at 100%) the bars
// are whole pixels and the gaps between them a clear pixel.
var trayBars = []trayBar{
	{2, 4, 28, 6, 26.5},
	{4, 12, 24, 6, 24.5},
	{6, 20, 20, 6, 22.5},
}

const trayDotR = 1.6

// trayGlyph draws the glyph at size x size pixels: black, its edges
// anti-aliased in the alpha channel.
func trayGlyph(size int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	unit := 32 / float64(size) // grid units per pixel
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			x, y := (float64(px)+0.5)*unit, (float64(py)+0.5)*unit
			cover := 0.0
			for _, b := range trayBars {
				r := b.h / 2
				// A pill: the distance to the segment between its end centres.
				cx := math.Max(b.x+r, math.Min(x, b.x+b.w-r))
				d := math.Hypot(x-cx, y-(b.y+r)) - r
				// The dot is cut out of it.
				d = math.Max(d, trayDotR-math.Hypot(x-b.dotX, y-(b.y+r)))
				cover = math.Max(cover, math.Min(1, math.Max(0, 0.5-d/unit)))
			}
			img.SetNRGBA(px, py, color.NRGBA{A: uint8(math.Round(cover * 255))})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}
