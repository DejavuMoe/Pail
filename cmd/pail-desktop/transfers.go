package main

import (
	"fmt"
	"math"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/icons"
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// taskGroup maps a task to the tab it shows in: 0 active (queued and
// running), 1 completed, 2 failed (failed, partly failed and canceled).
func taskGroup(t Task) int {
	switch t.Status {
	case core.StatusQueued, core.StatusRunning, core.StatusPaused:
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

// taskbarProgress is what the taskbar button shows of the active (queued,
// running and paused) work: the share of its bytes done, or of its files and
// items when none of it counts bytes, at steps of half a percent so that a
// transfer's ten reports a second do not each reach the system. A group of
// files counts as a whole through its progress (finished files with their
// whole size, queued ones with the size they were submitted with), so the
// share does not fall back when a file starts or finishes; a folder still
// being scanned can still grow its total. Tasks of a group are not counted
// again; other active tasks count their own bytes, or items. With active work
// and nothing counted yet it is indeterminate; with none, empty.
func taskbarProgress(ts []Task, gs []core.GroupProgress) mygo.ProgressBar {
	var bytes, bytesDone, items, itemsDone int64
	active := len(gs) > 0
	inGroup := make(map[string]bool, len(gs))
	for _, g := range gs {
		inGroup[g.ID] = true
		bytes, bytesDone = bytes+g.BytesTotal, bytesDone+g.BytesDone
		items, itemsDone = items+int64(g.Files), itemsDone+int64(g.FilesFinished())
	}
	for _, t := range ts {
		if taskGroup(t) != 0 {
			continue
		}
		active = true
		if inGroup[t.GroupID] && t.GroupID != "" && (t.Kind != core.KindOther || isScanTask(t)) {
			continue // the group's progress has it
		}
		bytes, bytesDone = bytes+t.BytesTotal, bytesDone+t.BytesDone
		items, itemsDone = items+int64(t.Items), itemsDone+int64(t.ItemsDone)
	}
	step := func(done, total int64) mygo.ProgressBar {
		v := math.Round(float64(done)/float64(total)*200) / 200
		return mygo.ProgressBar{State: mygo.ProgressNormal, Value: max(0, min(1, v))}
	}
	switch {
	case !active:
		return mygo.ProgressBar{}
	case bytes > 0:
		return step(bytesDone, bytes)
	case items > 0:
		return step(itemsDone, items)
	}
	return mygo.ProgressBar{State: mygo.ProgressIndeterminate}
}

// syncProgress runs after queued updates, even while the window is not drawing.
func (a *app) syncProgress() {
	if p := a.tv.progress; p != a.progressShown {
		a.progressShown = p
		a.progressSet(p)
	}
}

// transfersBar is the one-line strip shown when the panel is collapsed and the
// list has tasks; with no task at all it is not built (see view), and the
// status bar's transfer counts are the way to the panel.
func (a *app) transfersBar(c *ui.Context) {
	k := tk(c)
	n := a.tv.n
	bar := ui.Row(c).Height(collapsedBar).Padding(0, 16).Gap(8).AlignItems(ui.Center).
		BorderWidth(1, 0, 0, 0).BorderColor(k.Border).Background(k.Background)
	if bar.Hovered() {
		bar.Background(k.SurfaceHover)
	}
	bar.Children(func() {
		ui.Icon(c, icons.Get("chevron-up")).FontSize(14).TextColor(k.TextMuted)
		ui.Text(c, locales.TransfersTitle()).FontWeight(600).FontSize(sizeBody).TextColor(k.Text)
		// Three counts, each its own text.
		for i, s := range []string{
			locales.TransfersSummaryActive(fmt.Sprint(n[0])), locales.TransfersSummaryCompleted(fmt.Sprint(n[1])), locales.TransfersSummaryFailed(fmt.Sprint(n[2])),
		} {
			if i > 0 {
				ui.Text(c.Key(i), "·").FontSize(sizeSecondary).TextColor(k.TextSubtle)
			}
			countText(c, "summary-"+fmt.Sprint(i), s, k.TextMuted)
		}
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
	// groupRowH is a group's row: two lines of text in the number columns.
	groupRowH = 44
	// memberIndent moves a group's files in from the group's own row.
	memberIndent = 24
	// maxTaskRows bounds the rows built per frame; a folder upload can queue
	// thousands of files.
	maxTaskRows = 200
)

// transfersPanel is the collapsible bottom panel: a 32px header with text
// filter tabs, then 32px rows.
func (a *app) transfersPanel(c *ui.Context) {
	k := tk(c)
	n := a.tv.n
	ui.Column(c).Fill().MinHeight(0).Background(k.Background).BorderWidth(1, 0, 0, 0).BorderColor(k.Border).Children(func() {
		ui.Row(c).Height(32).Padding(0, 8, 0, 16).Gap(16).AlignItems(ui.Center).Shrink(0).Children(func() {
			ui.Text(c, locales.TransfersTitle()).FontWeight(600).FontSize(sizeBody).TextColor(k.Text)
			tabs := ui.TabsBase(c, &a.transTab, 3)
			line := markOf(tabs.List)
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
							col, w = k.Text, 500
						case tab.Hovered():
							col = k.Text
						}
						ui.Text(c, label).FontSize(sizeSecondary).FontWeight(w).TextColor(col).SingleLine().FontFeatures("tnum")
						if on {
							tabIndicator(c, line, i, tab)
						}
					})
				}
			})
			ui.Spacer(c)
			if a.transTab == 0 {
				if a.tv.pausable {
					ghostButton(c, icons.Get("pause"), locales.TransfersPauseAll(), locales.TransfersPauseAll(), false).Height(24).OnClick(a.svc.PauseAll)
				}
				if a.tv.paused {
					ghostButton(c, icons.Get("play"), locales.TransfersResumeAll(), locales.TransfersResumeAll(), false).Height(24).OnClick(a.svc.ResumeAll)
				}
			}
			finished := n[1] > 0 || n[2] > 0
			ghostButton(c, nil, locales.TransfersClearFinished(), locales.TransfersClearFinished(), !finished).Height(24).OnClick(a.clearFinished)
			if iconButton(c, icons.Get("chevron-down"), locales.TransfersCollapse()).Clicked() {
				a.transfersOpen = false
			}
		})
		hairline(c)
		// The Active tab is in queue order (running, then paused, then queued;
		// oldest first within each), the finished tabs newest first; all of it
		// was worked out when the tasks last changed (rebuildTransfers).
		rows, more := a.tv.active, a.tv.activeMore
		if a.transTab > 0 {
			list := a.tv.finished[a.transTab-1]
			more = a.tv.finishedMore[a.transTab-1]
			rows = make([]trRow, len(list))
			for i, t := range list {
				rows[i] = trRow{kind: trTask, t: t}
			}
		}
		if len(rows) == 0 {
			ui.Column(c).Grow(1).Center().Children(func() {
				mutedText(c, []string{locales.TransfersEmptyActive(), locales.TransfersEmptyCompleted(), locales.TransfersEmptyFailed()}[a.transTab])
			})
			return
		}
		paneScroll(c, func() {
			for _, r := range rows {
				switch r.kind {
				case trGroup:
					a.groupRow(c.Key("g:"+r.g.ID), r.g)
				case trQueued:
					a.memberLine(c.Key("q:"+r.g.ID), locales.TransfersGroupQueued(groupInt(r.n)))
				case trPaused:
					a.memberLine(c.Key("p:"+r.g.ID), locales.TransfersGroupPaused(groupInt(r.n)))
				default:
					a.taskRow(c.Key(r.t.ID), r.t, r.member)
				}
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
// The fill snaps to the whole pixel of each new fraction. It does not ease: a
// task reports about ten times a second and the bar is 160 px wide, so a
// report moves it by a pixel or two at most, while an ease that its next
// target interrupts never settles, and the window would draw (and rebuild its
// view) at the display's rate for as long as a transfer runs.
func progressBar(c *ui.Context, frac float64, danger bool) {
	k := tk(c)
	fill := k.Accent
	if danger {
		fill = k.Danger
	}
	frac = max(0, min(1, frac))
	ui.Box(c).Width(colProgress).Height(4).Radius(RadiusBar).Background(k.Border).Shrink(0).Clip().Children(func() {
		ui.Box(c).Height(4).Radius(RadiusBar).Width(float32(math.Round(frac * colProgress))).Background(fill)
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

// taskRow is one task; member indents it under its group's row.
func (a *app) taskRow(c *ui.Context, t Task, member bool) {
	k := tk(c)
	var icon, direction string
	switch t.Kind {
	case core.KindUpload:
		icon, direction = "arrow-up", locales.TransfersUpload()
	case core.KindDownload:
		icon, direction = "arrow-down", locales.TransfersDownload()
	default:
		icon, direction = "layers", locales.TransfersTask()
	}
	// The right padding leaves the scroll bar its own gutter, clear of the
	// cancel / retry buttons.
	left := float32(16)
	if member {
		left += memberIndent
	}
	row := ui.Row(c).Height(32).Padding(0, scrollGutter+2, 0, left).Gap(rowGap).AlignItems(ui.Center)
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
			switch t.Status {
			case core.StatusQueued, core.StatusPaused:
				label := locales.TransfersQueued()
				if t.Status == core.StatusPaused {
					label = locales.TransfersPaused()
				}
				ui.Row(c).Width(colProgress).Shrink(0).Gap(6).AlignItems(ui.Center).Children(func() {
					if t.Status == core.StatusPaused {
						ui.Icon(c, icons.Get("pause")).FontSize(12).TextColor(k.TextMuted)
					}
					ui.Text(c, label).FontSize(sizeSecondary).TextColor(k.TextMuted)
				})
				bytesCell("")
				speedCell("")
			default:
				progressBar(c, frac, false)
				bytesCell(progress)
				switch {
				case t.Phase != "":
					speedCell(phaseLabel(t.Phase)) // no bytes move: say what happens instead
				case t.BytesTotal > 0:
					speedCell(formatSpeed(t.Speed) + " · " + formatETA(t.BytesDone, t.BytesTotal, t.Speed))
				default:
					speedCell("")
				}
			}
			switch {
			case t.Status == core.StatusPaused:
				if iconButton(c, icons.Get("play"), locales.TransfersResume()).Clicked() {
					a.resumeTask(t.ID)
				}
			case t.Pausable:
				if iconButtonTip(c, icons.Get("pause"), locales.TransfersPause(), locales.TransfersPauseTip()).Clicked() {
					a.pauseTask(t.ID)
				}
			default:
				ui.Box(c).Size(24, 24).Shrink(0) // keeps the cancel buttons in one column
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
			// A cancel is the user's own doing, not an error: the recent
			// operations list draws it with an x in TextMuted, so does this.
			icon, col := "circle-alert", k.Danger
			switch {
			case t.Status == core.StatusCanceled:
				label, icon, col = locales.TransfersCanceled(), "x", k.TextMuted
			case label == "" && t.Status == core.StatusPartiallyFailed:
				label = locales.TransfersItemsFailed(fmt.Sprint(t.ItemsFailed), fmt.Sprint(t.Items))
			case label == "":
				label = locales.TransfersFailed()
			}
			ui.Row(c).Width(colProgress + colBytes + colSpeed + 2*rowGap).Shrink(0).Gap(6).AlignItems(ui.Center).Tooltip(label).Children(func() {
				ui.Icon(c, icons.Get(icon)).FontSize(14).TextColor(col).Shrink(0)
				ui.Text(c, label).FontSize(sizeSecondary).TextColor(col).SingleLine().MinWidth(0)
			})
			if iconButton(c, icons.Get("rotate-cw"), locales.CommonRetry()).Clicked() {
				a.retryTask(t.ID)
			}
		}
	})
}

// groupRow is the summary row of a transfer of many files (or of one whose scan
// is not done): its name and destination, the progress over all bytes known so
// far, the files done of the files known, the summed speed, and the buttons
// that pause, resume and cancel the whole group. Its running and paused files
// follow as member rows.
func (a *app) groupRow(c *ui.Context, g core.GroupProgress) {
	k := tk(c)
	icon, direction := "arrow-up", locales.TransfersUpload()
	if g.Kind == core.KindDownload {
		icon, direction = "arrow-down", locales.TransfersDownload()
	}
	title := groupTitle(g)
	tip := title
	if g.Detail != "" {
		tip += "  " + g.Detail
	}
	row := ui.Row(c).Height(groupRowH).Padding(0, scrollGutter+2, 0, 16).Gap(rowGap).AlignItems(ui.Center)
	if row.Hovered() {
		row.Background(k.SurfaceHover)
	}
	// Two lines in the number columns: the files above the bytes, the speed
	// above the hint that the total still grows.
	cell := func(w float32, end bool, top, bottom string, topCol, bottomCol ui.Color) {
		line := func(i int, s string, col ui.Color) {
			t := ui.Text(c.Key(i), s).FontSize(sizeSecondary).TextColor(col).SingleLine().MinWidth(0)
			if end {
				t.TextAlign(ui.End)
			}
		}
		ui.Column(c).Width(w).Shrink(0).Children(func() {
			line(0, top, topCol)
			line(1, bottom, bottomCol)
		})
	}
	var frac float64
	bytes := ""
	if g.BytesTotal > 0 {
		frac = float64(g.BytesDone) / float64(g.BytesTotal)
		bytes = locales.TransfersBytesOf(humanBytes(g.BytesDone), humanBytes(g.BytesTotal))
	}
	speed, hint := "", ""
	if g.Scanning && !g.Paused {
		hint = locales.TransfersScanning()
	}
	if !g.Paused && g.FilesRunning > 0 && g.BytesTotal > 0 {
		speed = formatSpeed(g.Speed) + " · " + formatETA(g.BytesDone, g.BytesTotal, g.Speed)
	}
	row.Children(func() {
		ui.Icon(c, icons.Get(icon)).FontSize(14).TextColor(k.TextMuted).Shrink(0).Tooltip(direction)
		ui.Row(c).Grow(1).Basis(0).MinWidth(120).Gap(8).AlignItems(ui.Center).Tooltip(tip).Children(func() {
			ui.Text(c, title).FontSize(sizeBody).FontWeight(500).TextColor(k.Text).SingleLine().Shrink(0).MaxWidthPercent(70)
			if g.Detail != "" {
				ui.Text(c, g.Detail).FontSize(sizeSecondary).TextColor(k.TextMuted).SingleLine().MinWidth(0)
			}
		})
		if g.Paused {
			ui.Row(c).Width(colProgress).Shrink(0).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icons.Get("pause")).FontSize(12).TextColor(k.TextMuted)
				ui.Text(c, locales.TransfersPaused()).FontSize(sizeSecondary).TextColor(k.TextMuted)
			})
		} else {
			progressBar(c, frac, false) // empty while nothing is known
		}
		cell(colBytes, true, groupFilesText(g), bytes, k.TextMuted, k.TextSubtle)
		cell(colSpeed, false, speed, hint, k.TextSubtle, k.TextSubtle)
		switch {
		case g.Paused:
			if iconButton(c, icons.Get("play"), locales.TransfersResumeGroup()).Clicked() {
				a.resumeGroup(g.ID)
			}
		case g.Pausable:
			if iconButtonTip(c, icons.Get("pause"), locales.TransfersPauseGroup(), locales.TransfersPauseGroupTip()).Clicked() {
				a.pauseGroup(g.ID)
			}
		default:
			ui.Box(c).Size(24, 24).Shrink(0)
		}
		if iconButton(c, icons.Get("x"), locales.TransfersCancelGroup()).Clicked() {
			a.cancelGroup(g.ID)
		}
	})
}

// memberLine is a muted line under a group that counts files instead of
// listing them: those waiting for their turn, or paused with the group.
func (a *app) memberLine(c *ui.Context, text string) {
	ui.Row(c).Height(24).Padding(0, scrollGutter+2, 0, 16+memberIndent+14+rowGap).AlignItems(ui.Center).Children(func() {
		subtleText(c, text)
	})
}
