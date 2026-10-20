package main

import (
	"context"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// options are the command-line settings.
type options struct {
	theme         string // system | light | dark; "" takes the saved setting
	width, height int
	tool          string
	demo          bool
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
type app struct {
	opts options
	svc  *core.Service
	ctx  context.Context
	stop context.CancelFunc

	// post runs fn on the UI thread (Window.Update in the real window, a
	// queue drained by the view in tests).
	post    func(fn func())
	pending chan func()
	wake    atomic.Pointer[func()]
	// pick opens a native file or folder chooser; it blocks, so it only runs
	// on a goroutine. Replaced in tests.
	pick func(dir bool) []string
	// reveal shows a path in the file manager (Shell.ShowItemInFolder).
	reveal func(path string)
	// openPath opens a file or folder with the desktop (Shell.OpenPath); it
	// blocks, so it only runs on a goroutine. Replaced in tests.
	openPath func(path string) error
	// openURL opens a web address in the default browser (Shell.OpenExternal);
	// it blocks, so it only runs on a goroutine. Replaced in tests.
	openURL func(url string) error
	// quit ends the app (App.Quit); themeSet tells the system which appearance
	// the app uses (Theme.SetSource), which colors the window controls and the
	// native menus. Both are injected by main and replaced in tests.
	quit        func()
	themeSet    func(mygo.ThemeSource)
	themeSynced string // the mode themeSet was last called with
	// lang is the language choice ("" follows the system); localeSet shows it
	// (App.SetLocale) and langSave keeps it (i18n.go). Replaced in tests.
	lang      string
	localeSet func(tag string)
	langSave  func(tag string)

	pal   paletteState // the command palette (palette.go)
	about aboutState   // the About dialog (about.go)
	keys  keysState    // the keyboard shortcuts dialog (about.go)
	// settingsMu orders the read-modify-write cycles of Settings, so two quick
	// changes in Settings cannot overwrite each other.
	settingsMu sync.Mutex

	toasts []toastReq
	clip   []string
	// busyOps counts short background requests that have no region of their
	// own (queueing an upload, signing a URL); tests wait for it to reach 0.
	busyOps atomic.Int32

	accounts  []Account
	accountID string

	buckets      []Bucket
	bucketsLoad  bool
	bucketsErr   string
	bucketsFix   string // what fixes bucketsErr: "unlock", "secret" or "" (Retry)
	bucketsGen   int
	bucketFilter string
	filterH      ui.Handle
	searchH      ui.Handle
	navSel       string // sidebar choice: "b:<bucket>" or "t:<tool>"
	tool         string // "" when the object browser shows
	scope        Scope

	raw         []Entry // everything loaded under the prefix
	rows        []Entry // filtered and sorted, what the table shows
	byKey       map[string]int
	rowsDirty   bool
	loading     bool
	listErr     string
	listGen     int
	cancelList  context.CancelFunc
	selectAfter string // key to select once the listing has loaded
	search      string
	sort        ui.SortOrder
	sortSeen    ui.SortOrder
	list        ui.ListState
	sel         ui.Selection[string]
	cursor      int // row last chosen, -1 for none
	hoverKey    string
	rowRects    map[string]ui.Rect

	inspectorOpen bool
	inspTab       int
	inspW         float32
	sidebarW      float32
	compact       bool    // the center column is narrower than compactBelow: icon-only path bar buttons
	tbCompact     bool    // the toolbar has less room than its labels need: icon-only commands
	tableW, pathW float32 // last laid-out widths of the object table and the path bar
	narrowSet     bool    // the narrow-window panel widths were applied
	head          core.ObjectInfo
	headKey       string
	headState     int // 0 none, 1 loading, 2 ready, 3 failed
	headErr       string
	headGen       int
	cancelHead    context.CancelFunc
	prev          previewState

	transfersOpen bool
	transTab      int
	transH        float32
	tasks         []Task // submission order; the panel shows newest first
	taskIdx       map[string]int
	inbox         eventInbox
	unsub         func()
	watches       map[string]*watch

	del       deleteState
	newFolder bool
	folderDlg folderState
	af        acctForm
	confirm   confirmState
	sf        settingsForm
	vu        vaultUI   // the encrypted vault (vault_ui.go)
	tl        toolState // the tool views (tools.go and the *_tool.go files)

	mg     mgmt        // editing, versions, clipboard and bucket dialogs (mgmt.go)
	objOps objOpsState // class, encryption, bucket copy, signed URL and flat listing (objops.go, bucket_copy.go, rewrite_storage.go, signedurl.go, flatlist.go)

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
		sort:      ui.SortOrder{Column: "name"},
		cursor:    -1,
		rowRects:  map[string]ui.Rect{},
		taskIdx:   map[string]int{},
		watches:   map[string]*watch{},
		pick:      func(bool) []string { return nil },
		reveal:    func(string) {},
		openPath:  func(string) error { return nil },
		openURL:   func(string) error { return nil },
		quit:      func() {},
		themeSet:  func(mygo.ThemeSource) {},
		localeSet: func(string) {}, langSave: func(string) {},
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
// wakes the window (when there is one) so it drains the queue.
func (a *app) enqueue(fn func()) {
	a.pending <- fn
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
			return
		}
	}
}

// idle reports whether no background request is in flight.
func (a *app) idle() bool {
	return !a.bucketsLoad && !a.loading && a.headState != 1 && a.prev.state != previewLoading &&
		(!a.del.open || a.del.state != delCounting) && !a.af.busy() && !a.folderDlg.busy && a.busyOps.Load() == 0 && !a.vu.busyNow()
}

// toast queues a message for the next frame.
func (a *app) toast(msg string) { a.toasts = append(a.toasts, toastReq{msg: msg}) }

func (a *app) toastAction(msg, label string, fn func()) {
	a.toasts = append(a.toasts, toastReq{msg: msg, action: label, fn: fn})
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
	return a.del.open || a.newFolder || a.af.open || a.confirm.open || a.tl.sync.dsc.open || a.mgModalOpen() || a.objOpsModalOpen() || a.vu.modal() ||
		a.pal.open || a.about.open || a.keys.open
}

// ---- accounts ------------------------------------------------------------------------

// refreshAccounts re-reads the saved accounts and keeps the selection valid.
func (a *app) refreshAccounts() {
	a.accounts = a.svc.Accounts()
	if a.account().ID == "" {
		a.selectAccount("")
		if len(a.accounts) > 0 {
			a.selectAccount(a.accounts[0].ID)
		}
	}
}

// selectAccount makes id the current account and loads its buckets.
func (a *app) selectAccount(id string) {
	if id == a.accountID && id != "" {
		return
	}
	a.accountID = id
	a.scope = Scope{AccountID: id}
	a.navSel, a.tool = "", ""
	a.buckets, a.bucketsErr = nil, ""
	a.clearListing()
	a.bucketsGen++
	a.bucketsLoad = false
	if id != "" {
		a.loadBuckets(true)
	}
}

// ---- buckets -------------------------------------------------------------------------

func (a *app) loadBuckets(openFirst bool) {
	id := a.accountID
	if id == "" {
		return
	}
	a.bucketsGen++
	gen := a.bucketsGen
	a.bucketsLoad, a.bucketsErr, a.bucketsFix = true, "", ""
	if a.account().SecretMissing { // nothing to connect with until the secret is entered
		a.bucketsLoad = false
		a.bucketsErr, a.bucketsFix = locales.StateBucketsSecretMissing(), "secret"
		return
	}
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	go func() {
		defer cancel()
		bs, err := a.svc.Buckets(ctx, id)
		a.post(func() {
			if gen != a.bucketsGen {
				return
			}
			a.bucketsLoad = false
			if err != nil {
				a.bucketsErr = a.noteErr(err, "buckets", func() {
					if a.accountID == id {
						a.loadBuckets(openFirst)
					}
				})
				if isLocked(err) {
					a.bucketsFix = "unlock"
				}
				return
			}
			a.buckets = bs
			if openFirst && len(bs) > 0 && a.scope.Bucket == "" && a.tool == "" {
				a.openBucket(bs[0].Name)
			}
		})
	}()
}

func (a *app) clearListing() {
	if a.cancelList != nil {
		a.cancelList()
	}
	a.listGen++
	a.raw, a.rows, a.byKey = nil, nil, nil
	a.loading, a.listErr, a.rowsDirty = false, "", false
	a.selectAfter = ""
	a.sel.Clear()
	a.cursor = -1
	a.resetHead()
}

func (a *app) openBucket(name string) {
	a.tool = ""
	a.objOps.flat = false
	a.navSel = "b:" + name
	a.openScope(Scope{AccountID: a.accountID, Bucket: name})
}

// openScope navigates to scope (a folder or the bucket root).
func (a *app) openScope(s Scope) {
	a.scope = s
	a.search = ""
	a.clearListing()
	a.reload()
}

func (a *app) openPrefix(prefix string) {
	s := a.scope
	s.Prefix = prefix
	a.openScope(s)
}

// up goes to the parent folder; false at the bucket root.
func (a *app) up() bool {
	parts := splitPrefix(a.scope.Prefix)
	if len(parts) == 0 {
		return false
	}
	p := strings.Join(parts[:len(parts)-1], "/")
	if p != "" {
		p += "/"
	}
	a.openPrefix(p)
	return true
}

// ---- listing -------------------------------------------------------------------------

// reload lists the current scope page by page. The old rows stay on screen
// until the first page arrives, and the table fills as pages come in; every
// further page is requested right away until the listing ends.
func (a *app) reload() {
	if a.scope.Bucket == "" || a.reloadHook() {
		return
	}
	if a.cancelList != nil {
		a.cancelList()
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.cancelList = cancel
	a.listGen++
	gen, scope, flat := a.listGen, a.scope, a.objOps.flat
	a.loading, a.listErr = true, ""
	go func() {
		token := ""
		for first := true; ; first = false {
			page, err := a.svc.List(ctx, scope, core.ListOptions{Token: token, Flat: flat})
			if ctx.Err() != nil {
				return
			}
			if flat && err == nil {
				page.Entries = flatEntries(page.Entries, scope.Prefix)
			}
			isFirst := first
			a.post(func() {
				if gen != a.listGen {
					return
				}
				if err != nil {
					a.listErr, a.loading = a.noteErr(err, "list", func() {
						if gen == a.listGen {
							a.reload()
						}
					}), false
					return
				}
				if isFirst {
					a.raw = page.Entries
				} else {
					a.raw = append(a.raw, page.Entries...)
				}
				a.rowsDirty = true
				if page.NextToken == "" {
					a.loading = false
					a.pruneSelection()
					a.applySelectAfter()
				}
			})
			if err != nil || page.NextToken == "" {
				return
			}
			token = page.NextToken
		}
	}()
}

// applySelectAfter selects the key a finished action asked for (a new folder).
func (a *app) applySelectAfter() {
	if a.selectAfter == "" {
		return
	}
	if _, ok := a.byKey[a.selectAfter]; ok {
		a.setSingle(a.selectAfter)
	}
	a.selectAfter = ""
}

// pruneSelection drops selected keys that are no longer listed.
func (a *app) pruneSelection() {
	a.rebuildRows()
	var gone []string
	for k := range a.sel.All() {
		if _, ok := a.byKey[k]; !ok {
			gone = append(gone, k)
		}
	}
	for _, k := range gone {
		a.sel.Remove(k)
	}
}

// rebuildRows filters and sorts raw into rows: folders always first.
func (a *app) rebuildRows() {
	q := strings.ToLower(strings.TrimSpace(a.search))
	rows := make([]Entry, 0, len(a.raw))
	for _, e := range a.raw {
		if q == "" || strings.Contains(strings.ToLower(e.Name), q) {
			rows = append(rows, e)
		}
	}
	desc := a.sort.Descending
	col := a.sort.Column
	slices.SortStableFunc(rows, func(x, y Entry) int {
		if x.Folder != y.Folder {
			if x.Folder {
				return -1
			}
			return 1
		}
		cmp := 0
		if !x.Folder {
			switch col {
			case "size":
				cmp = cmpInt(x.Size, y.Size)
			case "type":
				cmp = strings.Compare(extOf(x), extOf(y))
			case "modified":
				cmp = x.Modified.Compare(y.Modified)
			case "class":
				cmp = strings.Compare(x.StorageClass, y.StorageClass)
			}
			if desc {
				cmp = -cmp
			}
		}
		if cmp == 0 {
			// Ties (and folders) fall back to the name; only the name
			// column itself reverses with the sort direction.
			cmp = strings.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name))
			if col == "name" && desc {
				cmp = -cmp
			}
		}
		return cmp
	})
	a.rows = rows
	a.byKey = make(map[string]int, len(rows))
	for i, e := range rows {
		a.byKey[e.Key] = i
	}
	a.rowsDirty = false
	a.sortSeen = a.sort
}

func cmpInt(x, y int64) int {
	switch {
	case x < y:
		return -1
	case x > y:
		return 1
	}
	return 0
}

// selectedKeys returns the selected keys that are listed, in table order.
func (a *app) selectedKeys() []string {
	var out []string
	for k := range a.sel.All() {
		if _, ok := a.byKey[k]; ok {
			out = append(out, k)
		}
	}
	slices.SortFunc(out, func(x, y string) int { return a.byKey[x] - a.byKey[y] })
	return out
}

// selectedInfo summarises the selection: counts and total object bytes.
func (a *app) selectedInfo() (folders, files int, bytes int64) {
	for k := range a.sel.All() {
		i, ok := a.byKey[k]
		if !ok {
			continue
		}
		if e := a.rows[i]; e.Folder {
			folders++
		} else {
			files++
			bytes += e.Size
		}
	}
	return
}

// setSingle selects exactly one key.
func (a *app) setSingle(key string) {
	a.sel.Clear()
	a.sel.Add(key)
	a.cursor = -1
	if i, ok := a.byKey[key]; ok {
		a.cursor = i
	}
}

// folderSummary totals the loaded listing.
func (a *app) folderSummary() (folders, files int, bytes int64) {
	for _, e := range a.raw {
		if e.Folder {
			folders++
		} else {
			files++
			bytes += e.Size
		}
	}
	return
}

// ---- object details --------------------------------------------------------------

func (a *app) resetHead() {
	if a.cancelHead != nil {
		a.cancelHead()
		a.cancelHead = nil
	}
	a.headGen++
	a.headKey, a.headState, a.headErr = "", 0, ""
	a.resetPreview()
}

// ensureHead starts loading the details of the single selected object.
func (a *app) ensureHead() {
	keys := a.selectedKeys()
	if len(keys) != 1 || strings.HasSuffix(keys[0], "/") {
		if a.headKey != "" {
			a.resetHead()
		}
		return
	}
	key := keys[0]
	if key == a.headKey {
		return
	}
	a.resetHead()
	a.headKey, a.headState = key, 1
	gen, scope := a.headGen, a.scope
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	a.cancelHead = cancel
	go func() {
		defer cancel()
		info, err := a.svc.Head(ctx, scope, key, "")
		a.post(func() {
			if gen != a.headGen {
				return
			}
			if err != nil {
				a.headState, a.headErr = 3, a.noteErr(err, "head", func() {
					if gen == a.headGen {
						a.resetHead() // the next frame asks again
					}
				})
				return
			}
			a.head, a.headState = info, 2
		})
	}()
}

// ---- tasks ---------------------------------------------------------------------------

func (a *app) setTasks(ts []Task) {
	a.tasks = append(a.tasks[:0], ts...)
	a.reindexTasks()
}

func (a *app) reindexTasks() {
	clear(a.taskIdx)
	for i, t := range a.tasks {
		a.taskIdx[t.ID] = i
	}
}

// onEvent is the Service subscription callback; it runs off the UI thread.
func (a *app) onEvent(e core.Event) {
	in := &a.inbox
	in.mu.Lock()
	in.events = append(in.events, e)
	schedule := !in.scheduled
	in.scheduled = true
	in.mu.Unlock()
	if schedule {
		a.post(a.applyInbox)
	}
}

// applyInbox folds the queued task events into the task list and settles the
// watches of the groups and tasks they touched.
func (a *app) applyInbox() {
	in := &a.inbox
	in.mu.Lock()
	evs := in.events
	in.events, in.scheduled = nil, false
	in.mu.Unlock()
	if len(evs) == 0 {
		return
	}
	touched := map[string]bool{}
	for _, e := range evs {
		t := e.Task
		touched[t.ID] = true
		if t.GroupID != "" {
			touched[t.GroupID] = true
		}
		i, ok := a.taskIdx[t.ID]
		switch {
		case e.Removed:
			if ok {
				a.tasks = slices.Delete(a.tasks, i, i+1)
				a.reindexTasks()
			}
		case ok:
			a.tasks[i] = t
		default:
			a.taskIdx[t.ID] = len(a.tasks)
			a.tasks = append(a.tasks, t)
		}
	}
	for k := range touched {
		if a.watches[k] != nil {
			a.checkWatch(k)
		}
	}
}

// taskCounts totals the tasks of the three panel tabs.
func countTasks(ts []Task) (n [3]int) {
	for _, t := range ts {
		n[taskGroup(t)]++
	}
	return
}

// cancelTask and retryTask are the panel's buttons.
func (a *app) cancelTask(id string) {
	if err := a.svc.Cancel(id); err != nil {
		a.toast(locales.StateCouldNotCancel(humanError(err)))
	}
}

func (a *app) retryTask(id string) {
	if _, err := a.svc.Retry(id); err != nil {
		a.toast(locales.StateCouldNotRetry(humanError(err)))
		return
	}
	a.transTab = 0
}

func (a *app) clearFinished() { a.svc.ClearFinished() }
