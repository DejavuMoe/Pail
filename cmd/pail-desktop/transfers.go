package main

import (
	"fmt"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/icons"
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/egoist/mygo/ui"
)

// taskGroup maps a task to the tab it shows in: 0 active (queued and
// running), 1 completed, 2 failed (failed, partly failed and canceled).
func taskGroup(t Task) int {
	switch t.Status {
	case core.StatusQueued, core.StatusRunning:
		return 0
	case core.StatusSucceeded:
		return 1
	}
	return 2
}

// trAuto is what the panel remembers to open and close itself.
type trAuto struct {
	init          bool
	seen          int  // tasks at the last frame
	prevOpen      bool // panel state at the last frame
	userCollapsed bool // the user closed the panel while tasks existed
}

// autoTransfers runs once per frame before the layout: with no tasks at all
// the panel collapses to its header, and it opens again when a new task
// appears, unless the user collapsed it by hand since.
func (a *app) autoTransfers() {
	t := &a.tl.tr
	n := len(a.tasks)
	if !t.init {
		t.init = true
		if n == 0 {
			a.transfersOpen = false
		}
		t.seen, t.prevOpen = n, a.transfersOpen
		return
	}
	if a.transfersOpen != t.prevOpen && n > 0 {
		t.userCollapsed = !a.transfersOpen
	}
	switch {
	case n == 0 && t.seen > 0:
		a.transfersOpen, t.userCollapsed = false, false
	case n > t.seen && !t.userCollapsed:
		a.transfersOpen = true
	}
	t.seen, t.prevOpen = n, a.transfersOpen
}

// transfersBar is the one-line strip shown when the panel is collapsed.
func (a *app) transfersBar(c *ui.Context) {
	k := tk(c)
	n := countTasks(a.tasks)
	bar := ui.Row(c).Height(collapsedBar).Padding(0, 12).Gap(8).AlignItems(ui.Center).
		BorderWidth(1, 0, 0, 0).BorderColor(k.Border).Background(k.Background).Transition(hoverFade())
	if bar.Hovered() {
		bar.Background(k.SurfaceHover)
	}
	bar.Children(func() {
		ui.Icon(c, icons.Get("chevron-up")).FontSize(14).TextColor(k.TextMuted)
		ui.Text(c, locales.TransfersTitle()).FontWeight(600).FontSize(sizeSecondary).TextColor(k.Text)
		ui.Text(c, locales.TransfersSummary(fmt.Sprint(n[0]), fmt.Sprint(n[1]), fmt.Sprint(n[2]))).FontSize(sizeSecondary).TextColor(k.TextMuted).ContentTransition(countRoll)
	})
	if bar.Clicked() {
		a.transfersOpen = true
	}
}

// Column widths of a transfer row (DESIGN.md, transfers panel).
const (
	colProgress = 160
	colBytes    = 128
	colSpeed    = 112
	rowGap      = 12
	// maxTaskRows bounds the rows built per frame; a folder upload can queue
	// thousands of files.
	maxTaskRows = 200
)

// transfersPanel is the collapsible bottom panel: a 32px header with text
// filter tabs, then 32px rows.
func (a *app) transfersPanel(c *ui.Context) {
	k := tk(c)
	n := countTasks(a.tasks)
	ui.Column(c).Fill().MinHeight(0).Background(k.Background).BorderWidth(1, 0, 0, 0).BorderColor(k.Border).Children(func() {
		ui.Row(c).Height(32).Padding(0, 8, 0, 16).Gap(16).AlignItems(ui.Center).Shrink(0).Children(func() {
			ui.Text(c, locales.TransfersTitle()).FontWeight(600).FontSize(sizeSecondary).TextColor(k.Text)
			tabs := ui.TabsBase(c, &a.transTab, 3)
			tabs.List.Height(32).Gap(16).AlignItems(ui.Center).Children(func() {
				for i, label := range []string{
					locales.TransfersTabActive(fmt.Sprint(n[0])), locales.TransfersTabCompleted(fmt.Sprint(n[1])), locales.TransfersTabFailed(fmt.Sprint(n[2])),
				} {
					tab := tabs.Tab(i).Height(32).Center().Label(label)
					on := i == a.transTab
					tab.Children(func() {
						col, w := k.TextMuted, 400
						switch {
						case on:
							col, w = k.Text, 600
						case tab.Hovered():
							col = k.Text
						}
						ui.Text(c, label).FontSize(sizeSecondary).FontWeight(w).TextColor(tab.AnimateColor("ink", col, colorAnim)).SingleLine().ContentTransition(countRoll)
						if on {
							tabIndicator(c, "transfer-tabs")
						}
					})
				}
			})
			ui.Spacer(c)
			finished := n[1] > 0 || n[2] > 0
			ghostButton(c, nil, locales.TransfersClearFinished(), locales.TransfersClearFinished(), !finished).Height(24).OnClick(a.clearFinished)
			if iconButton(c, icons.Get("chevron-down"), locales.TransfersCollapse()).Clicked() {
				a.transfersOpen = false
			}
		})
		hairline(c)
		// Newest first.
		var shown []Task
		more := 0
		for i := len(a.tasks) - 1; i >= 0; i-- {
			t := a.tasks[i]
			if taskGroup(t) != a.transTab {
				continue
			}
			if len(shown) == maxTaskRows {
				more++
				continue
			}
			shown = append(shown, t)
		}
		if len(shown) == 0 {
			ui.Column(c).Grow(1).Center().Children(func() {
				mutedText(c, []string{locales.TransfersEmptyActive(), locales.TransfersEmptyCompleted(), locales.TransfersEmptyFailed()}[a.transTab])
			})
			return
		}
		ui.Scroll(c).Grow(1).MinHeight(0).ScrollbarInsets(0, 2, 0, 0).Children(func() {
			for _, t := range shown {
				a.taskRow(c.Key(t.ID), t)
			}
			if more > 0 {
				ui.Row(c).Height(32).Padding(0, 16).AlignItems(ui.Center).Children(func() {
					subtleText(c, locales.TransfersMore(groupInt(more)))
				})
			}
		})
	})
}

// progressBar is the fixed 160x4 bar; frac is 0..1, danger paints it Danger.
// The fill's width eases to each new fraction in durStandard (a task reports
// about ten times a second, so the target moves on before the ease ends and
// the fill just keeps going), and a change of state fades the colour.
func progressBar(c *ui.Context, frac float64, danger bool) {
	k := tk(c)
	fill := k.Accent
	if danger {
		fill = k.Danger
	}
	frac = max(0, min(1, frac))
	ui.Box(c).Width(colProgress).Height(4).Radius(2).Background(k.Border).Shrink(0).Clip().Children(func() {
		bar := ui.Box(c).Height(4).Radius(2)
		bar.Width(bar.AnimateTo("w", float32(frac*colProgress), fadeAnim)).Background(bar.AnimateColor("fill", fill, colorAnim))
	})
}

// taskProgress is the fraction done and the "x of y" text: bytes when the task
// moves bytes, items otherwise.
func taskProgress(t Task) (frac float64, text string) {
	switch {
	case t.BytesTotal > 0:
		return float64(t.BytesDone) / float64(t.BytesTotal), locales.TransfersBytesOf(humanBytes(t.BytesDone), humanBytes(t.BytesTotal))
	case t.Items > 0:
		return float64(t.ItemsDone) / float64(t.Items), locales.TransfersItemsOf(groupInt(t.ItemsDone), groupInt(t.Items))
	}
	return 0, "—"
}

func (a *app) taskRow(c *ui.Context, t Task) {
	k := tk(c)
	icon := map[core.Kind]string{core.KindUpload: "arrow-up", core.KindDownload: "arrow-down", core.KindOther: "layers"}[t.Kind]
	direction := map[core.Kind]string{core.KindUpload: locales.TransfersUpload(), core.KindDownload: locales.TransfersDownload(), core.KindOther: locales.TransfersTask()}[t.Kind]
	// The right padding leaves the scroll bar its own gutter, clear of the
	// cancel / retry buttons.
	row := ui.Row(c).Height(32).Padding(0, scrollGutter+2, 0, 16).Gap(rowGap).AlignItems(ui.Center).Transition(hoverFade())
	if row.Hovered() {
		row.Background(k.SurfaceHover)
	}
	frac, progress := taskProgress(t)
	bytesCell := func(s string) {
		ui.Text(c, s).FontSize(sizeSecondary).TextColor(k.TextMuted).SingleLine().Width(colBytes).Shrink(0).TextAlign(ui.End)
	}
	speedCell := func(s string) {
		ui.Text(c, s).FontSize(sizeSecondary).TextColor(k.TextSubtle).SingleLine().Width(colSpeed).Shrink(0)
	}
	tip := t.Title
	if t.Detail != "" {
		tip += "  " + t.Detail
	}
	row.Children(func() {
		ui.Icon(c, icons.Get(icon)).FontSize(14).TextColor(k.TextMuted).Shrink(0).Tooltip(direction)
		ui.Row(c).Grow(1).Basis(0).MinWidth(120).Gap(8).AlignItems(ui.Center).Tooltip(tip).Children(func() {
			ui.Text(c, t.Title).FontSize(sizeBody).TextColor(k.Text).SingleLine().Shrink(0).MaxWidthPercent(70)
			if t.Detail != "" {
				ui.Text(c, t.Detail).FontSize(sizeSecondary).TextColor(k.TextMuted).SingleLine().MinWidth(0)
			}
		})
		switch taskGroup(t) {
		case 0:
			if t.Status == core.StatusQueued {
				ui.Row(c).Width(colProgress).Shrink(0).Children(func() {
					ui.Text(c, locales.TransfersQueued()).FontSize(sizeSecondary).TextColor(k.TextMuted)
				})
				bytesCell("")
				speedCell("")
			} else {
				progressBar(c, frac, false)
				bytesCell(progress)
				if t.BytesTotal > 0 {
					speedCell(formatSpeed(t.Speed) + " · " + formatETA(t.BytesDone, t.BytesTotal, t.Speed))
				} else {
					speedCell("")
				}
			}
			if iconButton(c, icons.Get("x"), locales.CommonCancel()).Clicked() {
				a.cancelTask(t.ID)
			}
		case 1:
			ui.Row(c).Width(colProgress).Shrink(0).Children(func() {
				ui.Icon(c, icons.Get("check")).FontSize(14).TextColor(k.Success)
			})
			if t.BytesTotal > 0 {
				bytesCell(humanBytes(t.BytesTotal))
			} else {
				bytesCell(locales.CommonItems(t.Items))
			}
			speedCell("")
			ui.Box(c).Size(24, 24).Shrink(0)
		default:
			label := t.Err
			switch {
			case t.Status == core.StatusCanceled:
				label = locales.TransfersCanceled()
			case label == "" && t.Status == core.StatusPartiallyFailed:
				label = locales.TransfersItemsFailed(fmt.Sprint(t.ItemsFailed), fmt.Sprint(t.Items))
			case label == "":
				label = locales.TransfersFailed()
			}
			ui.Row(c).Width(colProgress + colBytes + colSpeed + 2*rowGap).Shrink(0).Gap(6).AlignItems(ui.Center).Tooltip(label).Children(func() {
				ui.Icon(c, icons.Get("circle-alert")).FontSize(14).TextColor(k.Danger).Shrink(0)
				ui.Text(c, label).FontSize(sizeSecondary).TextColor(k.Danger).SingleLine().MinWidth(0)
			})
			if iconButton(c, icons.Get("rotate-cw"), locales.CommonRetry()).Clicked() {
				a.retryTask(t.ID)
			}
		}
	})
}
