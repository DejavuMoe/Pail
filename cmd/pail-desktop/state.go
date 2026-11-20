package main

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// loadPhase is how far a background read has got.
type loadPhase uint8

const (
	loadNone loadPhase = iota
	loadBusy
	loadReady
	loadFailed
)

// options are the command-line settings.
type options struct {
	theme         string // system | light | dark; "" takes the saved setting
	width, height int
	tool          string
	demo          bool
	// still turns every animation off (c.DisableAnimations in each frame): the
	// tests of layout and behaviour run so, since the tester's clock does not
	// move and a frame of an entrance would be what they saw. The tests of the
	// motion itself (motion_test.go) run with it off.
	still bool
}

// toastReq is a toast waiting for the next frame: toasts are shown through the
// frame's Context, but results arrive between frames.
type toastReq struct {
	msg, action string
	fn          func()
}

// app is the whole UI state. Everything here is read and written on the UI
// thread only; goroutines hand results back through post, and every result
// carries the generation it was requested under so that a stale answer (the
// user moved on) is dropped.
//
// The state is grouped by what it is about, in embedded structs whose fields
// read as the app's own (a.rows, a.sel): the hooks into the system, where the
// user is, the listing, the choice in it, the panes, the object details, the
// tasks and the commands. The dialogs and tools keep a struct each below.
type app struct {
	opts options
	svc  *core.Service
	ctx  context.Context
	stop context.CancelFunc

	appPlatform
	appNav
	appListing
	appOpsLog
	appSelection
	appPanes
	appDetails
	appTasks
	appCommands

	// lang is the language choice ("" follows the system); localeSet shows it
	// and langSave keeps it (i18n.go).
	lang string
	// collapseTools folds the sidebar's TOOLS group to its header (sidebar.go);
	// it is kept in settings.json (collapseTools).
	collapseTools bool

	tray  trayUI       // the notification area / menu bar icon (tray.go)
	popup menuState    // the in-window menu (ctxmenu.go)
	pal   paletteState // the command palette (palette.go)
	about aboutState   // the About dialog (about.go)
	keys  keysState    // the keyboard shortcuts dialog (about.go)
	// settingsTail preserves UI submission order; settingsPending ignores
	// completions superseded by a newer intent for the same key.
	settingsMu      sync.Mutex
	settingsTail    <-chan struct{}
	settingsPending map[string]chan struct{}

	toasts []toastReq
	clip   []string
	// busyOps counts short background requests that have no region of their
	// own (queueing an upload, signing a URL); tests wait for it to reach 0.
	busyOps atomic.Int32

	del       deleteState
	newFolder bool
	folderDlg folderState
	alias     aliasState // the Set alias dialog (bucket_alias.go)
	af        acctForm
	confirm   confirmState
	sf        settingsForm
	srail     settingsRail // the Settings page: its scrolling and its rail (settings_layout.go)
	vu        vaultUI      // the encrypted vault (vault_ui.go)
	tl        toolState    // the tool views (tools.go and the *_tool.go files)

	mg     mgmt        // editing, versions, clipboard and bucket dialogs (mgmt.go)
	objOps objOpsState // class, encryption, bucket copy, signed URL and flat listing (objops.go, bucket_copy.go, rewrite_storage.go, signedurl.go, flatlist.go)
}

// appPlatform is how the app reaches the system: the UI thread, pickers, the
// file manager, the browser, the clipboard, the taskbar. main injects the real
// ones; newApp's defaults do nothing, and tests replace them.
type appPlatform struct {
	// post runs fn on the UI thread (Window.Update in the real window, a
	// queue drained by the view in tests).
	post    func(fn func())
	pending chan func()
	wake    atomic.Pointer[func()]
	// pick opens a native file or folder chooser; it blocks, so it only runs
	// on a goroutine.
	pick func(dir bool) []string
	// reveal shows a path in the file manager (Shell.ShowItemInFolder).
	reveal func(path string)
	// openPath opens a file or folder with the desktop (Shell.OpenPath); it
	// blocks, so it only runs on a goroutine.
	openPath func(path string) error
	// writeSecret writes confidential text to the system clipboard
	// (Clipboard.Write with Concealed); nil in headless tests.
	writeSecret func(text string) error
	// keepRunning keeps the app at full speed while tasks run with its
	// window hidden (Power.KeepRunning, macOS App Nap); nil in headless
	// tests. runRelease ends the current request.
	keepRunning func(reason string) (release func())
	runRelease  func()
	// openURL opens a web address in the default browser (Shell.OpenExternal);
	// it blocks, so it only runs on a goroutine.
	openURL func(url string) error
	// quit ends the app (App.Quit); themeSet tells the system which appearance
	// the app uses (Theme.SetSource), which colors the window controls and the
	// native menus.
	quit        func()
	themeSet    func(mygo.ThemeSource)
	themeSynced string // the mode themeSet was last called with
	// progressSet shows the transfers' progress on the taskbar button (the
	// Dock icon on macOS) with Window.SetProgressBar; progressShown is what
	// it was last called with.
	progressSet   func(mygo.ProgressBar)
	progressShown mygo.ProgressBar
	// localeSet shows a language (App.SetLocale), langSave keeps the choice.
	localeSet func(tag string)
	langSave  func(tag string)
}

// appNav is where the user is: the account, its buckets, and the bucket,
// folder or tool that is open (navigation.go, sidebar.go, pathbar.go).
type appNav struct {
	accounts  []Account
	accountID string

	buckets      []Bucket
	bucketsLoad  bool
	bucketsErr   string
	bucketsFix   bucketsFix // what the bucket list error offers (sidebar.go)
	bucketsGen   int
	bucketFilter string
	filterH      ui.Handle
	searchH      ui.Handle
	crumbH       ui.Handle  // the last breadcrumb: where Esc in the filter goes
	filter       pathFilter // the folder filter of a compact path bar (pathbar.go)
	navSel       string     // sidebar choice: "b:<bucket>" or "t:<tool>"
	navMark      markState  // the fill of the sidebar's current row (currentFill)
	tool         string     // "" when the object browser shows
	scope        Scope
}

// appListing is the open folder: what was loaded, and the rows the table
// shows of it (listing.go, objects.go).
type appListing struct {
	raw         []Entry // everything loaded under the prefix
	rows        []Entry // filtered and sorted, what the table shows
	byKey       map[string]int
	rowsDirty   bool
	rebuiltAt   time.Time // when the rows were last rebuilt
	rebuildWake bool      // a timer is set to bring the next rebuild about
	loading     bool
	listErr     string
	listGen     int
	cancelList  context.CancelFunc
	selectAfter string // key to select once the listing has loaded
	listQuiet   bool   // the list being started is a refresh after an action: it keeps took
	search      string
	sort        ui.SortOrder
	sortSeen    ui.SortOrder
	list        ui.ListState
	sizes       folderSizes // the folder sizes asked for (foldersize.go)
}

// appOpsLog is how long operations took: the last one in the status bar and
// the recent ones in their list (optime.go, recentops.go).
type appOpsLog struct {
	took    opTime    // how long the last finished operation took
	ops     []opEntry // the recent operations, oldest first
	opSeq   uint64    // how many operations were recorded (opEntry.seq)
	opsOpen bool      // the list of them shows above the status bar
}

// appSelection is the choice of rows in the table and the pointer over it
// (selection.go, marquee.go).
type appSelection struct {
	sel       selection
	selSum    selSummary // selectedCount and selectedInfo, kept until sel.gen moves
	cursor    int        // row last chosen, -1 for none
	hoverKey  string
	rowRects  map[string]ui.Rect
	rowTops   map[string]float32 // top of each row built, in the window, by key (marquee.go)
	tblRect   ui.Rect            // the object table, in the window, as last laid out
	drag      dragSel            // a drag selecting rows (marquee.go)
	extendKey string             // a Shift-click on a row's box: the choice extends to it once the table is built
}

// appPanes is the shape of the window: the sidebar, the inspector and the
// transfers panel, and this frame's layout (layout.go, shell.go).
type appPanes struct {
	inspectorOpen bool
	inspTab       int
	inspW         float32
	sidebarW      float32
	transfersOpen bool
	transTab      int
	transH        float32
	lay           layoutMetrics // this frame's widths and breakpoint tiers
	layMem        layoutState   // what the breakpoints remember between frames
	bodyH         float32       // this frame's height above the status bar and the transfers panel
	fxSide        splitFx       // divider styles of the three Splits
	fxInsp        splitFx
	fxTrans       splitFx
}

// appDetails is the chosen object's details and preview, for the inspector
// (head.go, inspector.go, preview.go).
type appDetails struct {
	head       core.ObjectInfo
	headKey    string
	headState  loadPhase
	headErr    string
	headGen    int
	headSeq    int // fresh reads of the details that keep the forms (refreshHead)
	cancelHead context.CancelFunc
	propsSaves int // saves sent from the Headers tab, to tell their ends apart
	prev       previewState
}

// appTasks is the queue as the UI knows it, and what to do when a task ends
// (tasks.go, transfers.go).
type appTasks struct {
	tasks        []Task // submission order; the panel shows newest first
	taskIdx      map[string]int
	inbox        eventInbox
	unsub        func()
	watches      map[string]*watch
	retryWatches map[string]*watch // original context while a task/group remains retryable
}

// appCommands is the command table and the menu built from it (commands.go,
// menu.go).
type appCommands struct {
	lastCmd      string // dedupe of menu vs shortcut double delivery
	lastCmdAt    int64
	lastCmdFrom  string
	menu         *mygo.Menu
	menuState    map[string]bool
	commands     []command
	commandIndex map[string]int
}

// eventInbox collects task events from the queue's goroutine; one pending
// wake-up drains them all, so a burst of progress events is one UI update.
type eventInbox struct {
	mu        sync.Mutex
	events    []core.Event
	scheduled bool
}

func newApp(svc *core.Service, opts options) *app {
	a := &app{
		opts: opts, svc: svc,
		pending:       make(chan func(), 4096),
		inspectorOpen: true, transfersOpen: true,
		inspW: 320, sidebarW: 248, transH: 168,
		layMem:    newLayoutState(),
		sort:      ui.SortOrder{Column: "name"},
		cursor:    -1,
		rowRects:  map[string]ui.Rect{},
		rowTops:   map[string]float32{},
		taskIdx:   map[string]int{},
		watches:   map[string]*watch{},
		pick:      func(bool) []string { return nil },
		reveal:    func(string) {},
		openPath:  func(string) error { return nil },
		openURL:   func(string) error { return nil },
		quit:      func() {},
		themeSet:  func(mygo.ThemeSource) {},
		localeSet: func(string) {}, langSave: func(string) {},
		progressSet:  func(mygo.ProgressBar) {},
		retryWatches: map[string]*watch{},
	}
	a.ctx, a.stop = context.WithCancel(context.Background())
	a.post = a.enqueue
	a.commands = commandTable()
	a.commandIndex = map[string]int{}
	for i, c := range a.commands {
		a.commandIndex[c.ID] = i
	}
	if a.opts.theme == "" {
		a.opts.theme = svc.Settings().Theme
	}
	a.tray.on = !svc.Settings().HideTrayIcon
	a.tray.quitIdle = svc.Settings().QuitWhenIdle
	a.collapseTools = svc.Settings().CollapseTools
	a.unsub = svc.Subscribe(a.onEvent)
	a.setTasks(svc.Tasks())
	a.accounts = svc.Accounts()
	if len(a.accounts) > 0 {
		a.accountID = a.accounts[0].ID
	}
	if opts.tool != "" {
		a.openTool(opts.tool)
	}
	a.vaultAtStart()
	a.loadBuckets(true)
	go a.vaultPoll()
	return a
}

// close stops background work and the task subscription.
func (a *app) close() {
	a.unsub()
	a.stop()
}

// enqueue is safe from any goroutine: it queues fn for the UI thread and
// wakes the window (when there is one) so it drains the queue. Once the app is
// stopped nothing drains the queue any more, so work that is still finishing
// drops its result instead of blocking forever on a full queue.
func (a *app) enqueue(fn func()) {
	select {
	case a.pending <- fn:
	case <-a.ctx.Done():
		return
	}
	if w := a.wake.Load(); w != nil {
		(*w)()
	}
}

// drain runs the callbacks that background work queued (tests; the real
// window uses Window.Update).
func (a *app) drain() {
	for {
		select {
		case fn := <-a.pending:
			fn()
		default:
			a.syncProgress()
			a.syncTray()
			return
		}
	}
}

// toast queues a message for the next frame. While the window is hidden in
// the tray nobody would see it: it goes to the system's notifications instead
// (without its action, which needs the window).
func (a *app) toast(msg string) {
	if a.notifyHidden(msg) {
		return
	}
	a.toasts = append(a.toasts, toastReq{msg: msg})
}

func (a *app) toastAction(msg, label string, fn func()) {
	if a.notifyHidden(msg) {
		return
	}
	a.toasts = append(a.toasts, toastReq{msg: msg, action: label, fn: fn})
}

// notifyHidden shows msg as a system notification when the window is hidden.
func (a *app) notifyHidden(msg string) bool {
	if !a.windowHidden() || a.tray.notify == nil {
		return false
	}
	a.tray.notify(msg)
	return true
}

// copyText queues text for the system clipboard.
func (a *app) copyText(s string) { a.clip = append(a.clip, s) }

// flushOutput hands the queued toasts and clipboard text to the frame.
func (a *app) flushOutput(c *ui.Context) {
	for _, t := range a.toasts {
		if t.action != "" {
			c.ToastAction(t.msg, t.action, t.fn)
		} else {
			c.Toast(t.msg)
		}
	}
	a.toasts = nil
	if n := len(a.clip); n > 0 {
		c.WriteClipboard(a.clip[n-1])
		a.clip = nil
	}
}

func (a *app) account() Account {
	for _, ac := range a.accounts {
		if ac.ID == a.accountID {
			return ac
		}
	}
	return Account{}
}

func (a *app) hasAccount() bool { return a.account().ID != "" }

func (a *app) bucket() (Bucket, bool) {
	for _, b := range a.buckets {
		if b.Name == a.scope.Bucket {
			return b, true
		}
	}
	return Bucket{}, false
}

// modalOpen reports whether a dialog holds the keyboard.
func (a *app) modalOpen() bool {
	return a.del.open || a.newFolder || a.alias.open || a.af.open || a.confirm.open || a.tl.sync.dsc.open || a.mgModalOpen() || a.objOpsModalOpen() || a.vu.modal() ||
		a.pal.open || a.about.open || a.keys.open
}
