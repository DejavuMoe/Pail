package main

import (
	"runtime"
	"strings"
	"time"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
)

// chord is a keyboard shortcut and how it is written in tooltips and menus.
type chord struct {
	Mods ui.Modifiers
	Key  ui.Key
	Name string // "Ctrl+U", "F5"
}

func (c chord) String() string {
	if runtime.GOOS == "darwin" {
		return strings.NewReplacer("Ctrl+", "⌘", "Alt+", "⌥", "Shift+", "⇧").Replace(c.Name)
	}
	return c.Name
}

// command is one row of the single command table. The toolbar, the context
// menu, the native menu bar and the keyboard shortcuts all read it.
type command struct {
	ID    string
	Label func(m locales.Messages) string
	Icon  string
	// Group names the heading the command has in the command palette and in
	// the keyboard shortcuts list (group* below); Aliases are extra search words
	// (the palette searches them in every language, and the English label too).
	Group   string
	Aliases func(m locales.Messages) string
	// Chords are the in-window shortcuts; the first one is shown.
	Chords []chord
	// Accel is the native menu accelerator, left empty for keys a text field
	// needs (Backspace, Delete, Ctrl+A, Ctrl+C...), which the window handles
	// itself only while no text field has the focus.
	Accel   string
	Enabled func(a *app) bool
	// Visible hides the command from the context menu while false; nil is
	// always visible.
	Visible func(a *app) bool
	Run     func(a *app)
}

func (cmd *command) hint() string {
	if len(cmd.Chords) == 0 {
		return ""
	}
	return cmd.Chords[0].String()
}

// text is the label in the language the app shows now; english is the English
// one, which the palette also searches.
func (cmd *command) text() string    { return cmd.Label(locales.In(i18n.Locale())) }
func (cmd *command) english() string { return cmd.Label(locales.In("en")) }

// groupName is the localized heading of a command group.
func groupName(m locales.Messages, g string) string {
	switch g {
	case groupTransfer:
		return m.CmdGroupTransfer()
	case groupObject:
		return m.CmdGroupObject()
	case groupNavigate:
		return m.CmdGroupNavigate()
	case groupView:
		return m.CmdGroupView()
	case groupBucket:
		return m.CmdGroupBucket()
	case groupTools:
		return m.CmdGroupTools()
	case groupApp:
		return m.CmdGroupApp()
	}
	return g
}

// tooltip is the label plus shortcut.
func (cmd *command) tooltip() string {
	s := strings.TrimSuffix(cmd.text(), "…")
	if h := cmd.hint(); h != "" {
		return s + " (" + h + ")"
	}
	return s
}

func (a *app) browsing() bool { return a.tool == "" && a.scope.Bucket != "" }

func (a *app) cmd(id string) *command {
	if i, ok := a.commandIndex[id]; ok {
		return &a.commands[i]
	}
	return nil
}

func (a *app) enabled(cmd *command) bool {
	if cmd == nil {
		return false
	}
	return cmd.Enabled == nil || cmd.Enabled(a)
}

// run executes a command by id. from names the source ("menu", "key",
// "toolbar", "context"): a native menu accelerator and an in-window shortcut
// can both see one key press, so the same command from two different
// sources within 120ms counts once.
func (a *app) run(id, from string) {
	cmd := a.cmd(id)
	if !a.enabled(cmd) {
		return
	}
	now := time.Now().UnixMilli()
	if a.lastCmd == id && a.lastCmdFrom != from && now-a.lastCmdAt < 120 {
		return
	}
	a.lastCmd, a.lastCmdFrom, a.lastCmdAt = id, from, now
	cmd.Run(a)
}

var (
	chUpload    = chord{ui.Cmd, ui.KeyU, "Ctrl+U"}
	chUploadDir = chord{ui.Cmd | ui.Shift, ui.KeyU, "Ctrl+Shift+U"}
	chDownload  = chord{ui.Cmd, ui.KeyD, "Ctrl+D"}
	chNewFolder = chord{ui.Cmd | ui.Shift, ui.KeyN, "Ctrl+Shift+N"}
	chPalette   = chord{ui.Cmd, ui.KeyK, "Ctrl+K"}
	chPaletteP  = chord{ui.Cmd | ui.Shift, ui.KeyP, "Ctrl+Shift+P"}
)

// The command groups, in the order of the palette's unfiltered list: tasks,
// not menus.
const (
	groupTransfer = "transfer"
	groupObject   = "object"
	groupNavigate = "navigate"
	groupView     = "view"
	groupBucket   = "bucket"
	groupTools    = "tools"
	groupApp      = "app"
)

var paletteGroups = []string{groupTransfer, groupObject, groupNavigate, groupView, groupBucket, groupTools, groupApp}

// commandGroups and commandAliases give the table's commands their palette
// heading and search words, by id, so the table rows stay short.
var commandGroups = map[string]string{
	"upload.files": groupTransfer, "upload.folder": groupTransfer, "download": groupTransfer,
	"newfolder": groupObject, "open": groupObject, "copy": groupObject, "cut": groupObject, "paste": groupObject, "rename": groupObject,
	"delete": groupObject, "restore": groupObject, "restoreall": groupObject, "selectall": groupObject, "copykey": groupObject,
	"copyuri": groupObject, "copyurl": groupObject, "copycdn": groupObject, "opencdn": groupObject, "copysigned": groupObject,
	"storageclass": groupObject, "encryption": groupObject, "invalidate": groupObject,
	"up": groupNavigate, "refresh": groupNavigate, "search": groupNavigate,
	"inspector": groupView, "transfers": groupView, "theme.system": groupView, "theme.light": groupView, "theme.dark": groupView,
	"lang.system": groupView, "lang.en": groupView, "lang.zh-Hans": groupView,
	"newbucket": groupBucket, "bucketsettings": groupBucket,
	"palette": groupApp, "shortcuts": groupApp, "docs": groupApp, "about": groupApp, "quit": groupApp,
	"vault.lock": groupApp, "vault.unlock": groupApp,
}

var commandAliases = map[string]func(locales.Messages) string{
	"upload.files": locales.Messages.AliasUploadFiles, "upload.folder": locales.Messages.AliasUploadFolder, "download": locales.Messages.AliasDownload,
	"newfolder": locales.Messages.AliasNewFolder, "delete": locales.Messages.AliasDelete, "rename": locales.Messages.AliasRename,
	"refresh": locales.Messages.AliasRefresh, "up": locales.Messages.AliasUp, "inspector": locales.Messages.AliasInspector,
	"transfers": locales.Messages.AliasTransfers, "search": locales.Messages.AliasSearch, "newbucket": locales.Messages.AliasNewBucket,
	"bucketsettings": locales.Messages.AliasBucketSettings, "copysigned": locales.Messages.AliasCopySigned, "copyurl": locales.Messages.AliasCopyUrl,
	"copykey": locales.Messages.AliasCopyKey, "storageclass": locales.Messages.AliasStorageClass, "encryption": locales.Messages.AliasEncryption,
	"vault.lock": locales.Messages.AliasVault, "vault.unlock": locales.Messages.AliasVault,
	"tool.sync": locales.Messages.AliasToolSync, "tool.cloudfront": locales.Messages.AliasToolCloudfront, "tool.rules": locales.Messages.AliasToolRules,
	"tool.mime": locales.Messages.AliasToolMime, "tool.multipart": locales.Messages.AliasToolMultipart, "tool.settings": locales.Messages.AliasToolSettings,
	"palette": locales.Messages.AliasPalette, "shortcuts": locales.Messages.AliasShortcuts, "docs": locales.Messages.AliasDocs,
	"about": locales.Messages.AliasAbout, "quit": locales.Messages.AliasQuit,
	"theme.system": locales.Messages.AliasTheme, "theme.light": locales.Messages.AliasTheme, "theme.dark": locales.Messages.AliasTheme,
	"lang.system": locales.Messages.AliasLanguage, "lang.en": locales.Messages.AliasLanguage, "lang.zh-Hans": locales.Messages.AliasLanguage,
	"open": locales.Messages.AliasOpen, "copy": locales.Messages.AliasCopy, "paste": locales.Messages.AliasPaste, "cut": locales.Messages.AliasCut,
	"selectall": locales.Messages.AliasSelectAll,
}

// themeName is a theme mode's name.
func themeName(m locales.Messages, mode string) string {
	switch mode {
	case "light":
		return m.ModeLight()
	case "dark":
		return m.ModeDark()
	}
	return m.ModeSystem()
}

// awsBrowsing is true while browsing a bucket of an AWS account, the only
// accounts CloudFront belongs to.
func awsBrowsing(a *app) bool { return a.browsing() && a.cfAvailable() }

func deletedOn(a *app) bool    { return a.browsing() && a.mg.del.on }
func liveBrowsing(a *app) bool { return a.browsing() && !a.mg.del.on }
func oneSelected(a *app) bool  { return a.browsing() && len(a.selectedKeys()) == 1 }

// oneObject is a single selected object (not a folder).
func oneObject(a *app) bool {
	k := a.selectedKeys()
	return a.browsing() && len(k) == 1 && !strings.HasSuffix(k[0], "/")
}

func commandTable() []command {
	toolCmd := func(id string, label func(locales.Messages) string, icon string) command {
		return command{ID: "tool." + id, Label: label, Icon: icon, Run: func(a *app) { a.openTool(id) }}
	}
	themeCmd := func(mode, icon string) command {
		return command{ID: "theme." + mode, Icon: icon, Run: func(a *app) { a.setTheme(mode) },
			Label: func(m locales.Messages) string { return m.CmdTheme(themeName(m, mode)) }}
	}
	langCmd := func(tag string) command {
		id := "lang." + tag
		if tag == "" {
			id = "lang.system"
		}
		return command{ID: id, Icon: "globe", Run: func(a *app) { a.setLanguage(tag) },
			Label: func(m locales.Messages) string {
				if tag == "" {
					return m.CmdLanguage(m.LanguageFollowSystem())
				}
				return m.CmdLanguage(i18n.Autonym(tag))
			}}
	}
	cmds := []command{
		{ID: "upload.files", Label: locales.Messages.CmdUploadFiles, Icon: "upload", Chords: []chord{chUpload}, Accel: "CmdOrCtrl+U",
			Enabled: (*app).browsing, Run: func(a *app) { a.startUpload(false) }},
		{ID: "upload.folder", Label: locales.Messages.CmdUploadFolder, Icon: "folder-up", Chords: []chord{chUploadDir}, Accel: "CmdOrCtrl+Shift+U",
			Enabled: (*app).browsing, Run: func(a *app) { a.startUpload(true) }},
		{ID: "download", Label: locales.Messages.CmdDownload, Icon: "download", Chords: []chord{chDownload}, Accel: "CmdOrCtrl+D",
			Enabled: liveSelection, Run: func(a *app) { a.startDownload() }},
		{ID: "newfolder", Label: locales.Messages.CmdNewFolder, Icon: "folder-plus", Chords: []chord{chNewFolder}, Accel: "CmdOrCtrl+Shift+N",
			Enabled: (*app).browsing, Run: func(a *app) { a.openNewFolder() }},
		{ID: "open", Label: locales.Messages.CmdOpen, Icon: "folder-open",
			Enabled: func(a *app) bool {
				k := a.selectedKeys()
				return a.browsing() && len(k) == 1 && strings.HasSuffix(k[0], "/")
			},
			Run: func(a *app) { a.openPrefix(a.selectedKeys()[0]) }},
		{ID: "copy", Label: locales.Messages.CmdCopy, Icon: "copy", Chords: []chord{{ui.Cmd, ui.KeyC, "Ctrl+C"}},
			Enabled: liveSelection, Run: func(a *app) { a.copySelection(false) }},
		{ID: "cut", Label: locales.Messages.CmdCut, Icon: "scissors", Chords: []chord{{ui.Cmd, ui.KeyX, "Ctrl+X"}},
			Enabled: liveSelection, Run: func(a *app) { a.copySelection(true) }},
		{ID: "paste", Label: locales.Messages.CmdPaste, Icon: "clipboard-paste", Chords: []chord{{ui.Cmd, ui.KeyV, "Ctrl+V"}},
			Enabled: canPaste, Run: func(a *app) { a.paste() }},
		{ID: "rename", Label: locales.Messages.CmdRename, Icon: "square-pen", Chords: []chord{{0, ui.KeyF2, "F2"}}, Accel: "F2",
			Enabled: canRename, Run: func(a *app) { a.startRename() }},
		{ID: "delete", Label: locales.Messages.CmdDelete, Icon: "trash-2", Chords: []chord{{0, ui.KeyDelete, "Del"}},
			Enabled: liveSelection, Run: func(a *app) { a.askDelete() }},
		{ID: "restore", Label: locales.Messages.CmdRestore, Icon: "undo-2", Visible: deletedOn,
			Enabled: func(a *app) bool { return deletedOn(a) && a.sel.Len() > 0 }, Run: func(a *app) { a.restoreSelected() }},
		{ID: "restoreall", Label: locales.Messages.CmdRestoreAll, Icon: "undo-2", Visible: deletedOn,
			Enabled: func(a *app) bool { return deletedOn(a) && len(a.rows) > 0 }, Run: func(a *app) { a.askRestoreAll() }},
		{ID: "refresh", Label: locales.Messages.CmdRefresh, Icon: "refresh-cw", Chords: []chord{{0, ui.KeyF5, "F5"}, {ui.Cmd, ui.KeyR, "Ctrl+R"}}, Accel: "F5",
			Enabled: (*app).browsing, Run: func(a *app) { a.reload() }},
		{ID: "selectall", Label: locales.Messages.CmdSelectAll, Icon: "check-check", Chords: []chord{{ui.Cmd, ui.KeyA, "Ctrl+A"}},
			Enabled: func(a *app) bool { return a.browsing() && len(a.rows) > 0 }, Run: func(a *app) { a.selectAll() }},
		{ID: "up", Label: locales.Messages.CmdUp, Icon: "arrow-up", Chords: []chord{{0, ui.KeyBackspace, "Backspace"}, {ui.Alt, ui.KeyUp, "Alt+Up"}},
			Enabled: func(a *app) bool { return a.browsing() && a.scope.Prefix != "" }, Run: func(a *app) { a.up() }},
		{ID: "copykey", Label: locales.Messages.CmdCopyKey, Icon: "key-round", Enabled: oneSelected,
			Run: func(a *app) { a.copyText(a.selectedKeys()[0]); a.toast(locales.CmdKeyCopied()) }},
		{ID: "copyuri", Label: locales.Messages.CmdCopyUri, Icon: "link", Enabled: oneSelected,
			Run: func(a *app) { a.copyText(a.objectURI(a.selectedKeys()[0])); a.toast(locales.CmdUriCopied()) }},
		{ID: "copyurl", Label: locales.Messages.CmdCopyUrl, Icon: "link", Enabled: oneObject,
			Run: func(a *app) { a.copyText(a.objectURL(a.selectedKeys()[0])); a.toast(locales.CmdUrlCopied()) }},
		{ID: "copycdn", Label: locales.Messages.CmdCopyCdn, Icon: "globe", Visible: bucketHasCDN, Enabled: hasCDNSelection,
			Run: func(a *app) { a.copyCDN() }},
		{ID: "opencdn", Label: locales.Messages.CmdOpenCdn, Icon: "external-link", Visible: bucketHasCDN, Enabled: oneCDNObject,
			Run: func(a *app) { a.openCDN() }},
		{ID: "copysigned", Label: locales.Messages.CmdCopySigned, Icon: "link", Enabled: hasObjectSelection,
			Run: func(a *app) { a.openSigned(a.signedObjects(), "") }},
		{ID: "storageclass", Label: locales.Messages.CmdStorageClass, Icon: "layers", Visible: liveBrowsing, Enabled: liveSelection,
			Run: func(a *app) { a.openRewriteSelection(false) }},
		{ID: "encryption", Label: locales.Messages.CmdEncryption, Icon: "file-lock", Visible: liveBrowsing, Enabled: liveSelection,
			Run: func(a *app) { a.openRewriteSelection(true) }},
		{ID: "invalidate", Label: locales.Messages.CmdInvalidate, Icon: "cloud", Visible: awsBrowsing, Enabled: liveSelection,
			Run: func(a *app) { a.openInvalidation(invalidationPaths(a.selectedKeys())) }},
		{ID: "inspector", Label: locales.Messages.CmdInspector, Icon: "panel-right", Chords: []chord{{ui.Cmd, ui.KeyI, "Ctrl+I"}}, Accel: "CmdOrCtrl+I",
			Run: func(a *app) { a.inspectorOpen = !a.inspectorOpen }},
		{ID: "transfers", Label: locales.Messages.CmdTransfers, Icon: "panel-bottom", Chords: []chord{{ui.Cmd, ui.KeyJ, "Ctrl+J"}}, Accel: "CmdOrCtrl+J",
			Run: func(a *app) { a.transfersOpen = !a.transfersOpen }},
		{ID: "search", Label: locales.Messages.CmdSearch, Icon: "search", Chords: []chord{{ui.Cmd, ui.KeyF, "Ctrl+F"}}, Accel: "CmdOrCtrl+F",
			Enabled: (*app).browsing, Run: func(a *app) { a.searchH.Focus() }},
		{ID: "newbucket", Label: locales.Messages.CmdNewBucket, Icon: "plus", Enabled: (*app).hasAccount, Run: func(a *app) { a.openNewBucket() }},
		{ID: "bucketsettings", Label: locales.Messages.CmdBucketSettings, Icon: "sliders-horizontal", Enabled: (*app).browsing,
			Run: func(a *app) { a.openBucketSettings(a.scope.Bucket) }},
		{ID: "vault.lock", Label: locales.Messages.CmdVaultLock, Icon: "lock", Visible: (*app).vaultMode,
			Enabled: func(a *app) bool { return a.vaultMode() && a.vu.state.Unlocked }, Run: (*app).lockVault},
		{ID: "vault.unlock", Label: locales.Messages.CmdVaultUnlock, Icon: "lock-open", Visible: (*app).vaultMode,
			Enabled: (*app).vaultLocked, Run: func(a *app) { a.askUnlock("menu", nil) }},
		toolCmd("sync", locales.Messages.CmdToolSync, "refresh-ccw"),
		toolCmd("cloudfront", locales.Messages.CmdToolCloudfront, "cloud"),
		toolCmd("rules", locales.Messages.CmdToolRules, "list-filter"),
		toolCmd("mime", locales.Messages.CmdToolMime, "file-type"),
		toolCmd("multipart", locales.Messages.CmdToolMultipart, "layers"),
		toolCmd("settings", locales.Messages.CmdToolSettings, "settings"),
		{ID: "palette", Label: locales.Messages.CmdPalette, Icon: "command", Chords: []chord{chPalette, chPaletteP}, Accel: "CmdOrCtrl+K",
			Run: func(a *app) { a.openPalette() }},
		themeCmd("system", "monitor"),
		themeCmd("light", "sun"),
		themeCmd("dark", "moon"),
		langCmd(""),
		langCmd("en"),
		langCmd("zh-Hans"),
		{ID: "shortcuts", Label: locales.Messages.CmdShortcuts, Icon: "keyboard", Run: func(a *app) { a.keys.open = true }},
		{ID: "docs", Label: locales.Messages.CmdDocs, Icon: "book-open", Run: func(a *app) { a.openExternal(docsURL) }},
		{ID: "about", Label: locales.Messages.CmdAbout, Icon: "info", Run: func(a *app) { a.about.open = true }},
		{ID: "quit", Label: locales.Messages.CmdQuit, Icon: "power", Run: func(a *app) { a.quit() }},
	}
	for i := range cmds {
		c := &cmds[i]
		if g, ok := commandGroups[c.ID]; ok {
			c.Group = g
		} else if strings.HasPrefix(c.ID, "tool.") {
			c.Group = groupTools
		}
		c.Aliases = commandAliases[c.ID]
	}
	return cmds
}

// contextLayout is the order of the context menu: groups of command ids.
var contextLayout = [][]string{
	{"open", "download"},
	{"copy", "cut", "paste", "rename"},
	{"restore", "restoreall"},
	{"storageclass", "encryption"},
	{"copykey", "copyuri", "copyurl", "copycdn", "opencdn", "copysigned", "invalidate"},
	{"upload.files", "newfolder", "refresh", "selectall"},
	{"delete"},
}

// buildMenu fills a context menu from the command table.
func (a *app) buildMenu(m *ui.Menu) {
	// A right click on a row outside the selection selects it first, as file
	// managers do (hoverKey is the row under the pointer, "" over blank space).
	if a.hoverKey != "" && !a.sel.Has(a.hoverKey) {
		a.setSingle(a.hoverKey)
	}
	shown := 0
	for _, group := range contextLayout {
		var ids []string
		for _, id := range group {
			if cmd := a.cmd(id); cmd.Visible == nil || cmd.Visible(a) {
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			continue
		}
		if shown > 0 {
			m.Separator()
		}
		shown++
		for _, id := range ids {
			cmd := a.cmd(id)
			item := m.Item(cmd.text()).Disabled(!a.enabled(cmd))
			if len(cmd.Chords) > 0 {
				item.Shortcut(cmd.Chords[0].Mods, cmd.Chords[0].Key)
			}
			if item.Chosen() {
				a.run(id, "context")
			}
		}
	}
}

// pollShortcuts runs the in-window shortcuts of the command table. Keys
// that a text field uses are ignored while one has the focus; modal
// dialogs hold the keyboard themselves.
func (a *app) pollShortcuts(c *ui.Context) {
	if a.modalOpen() {
		return
	}
	typing := a.searchH.Focused(c) || a.filterH.Focused(c) || a.mgTyping()
	for i := range a.commands {
		cmd := &a.commands[i]
		for _, ch := range cmd.Chords {
			if !c.Shortcut(ch.Mods, ch.Key) {
				continue
			}
			if typing && (ch.Mods == 0 || ch.Key == ui.KeyA || ch.Key == ui.KeyC || ch.Key == ui.KeyV || ch.Key == ui.KeyX) {
				continue
			}
			a.run(cmd.ID, "key")
		}
	}
}

func (a *app) selectAll() {
	for _, e := range a.rows {
		a.sel.Add(e.Key)
	}
	a.cursor = len(a.rows) - 1
}

func (a *app) openTool(id string) {
	a.tool = id
	a.navSel = "t:" + id
	if id == "settings" {
		a.sf = settingsForm{}
		a.vu.lastReset = nil
		a.refreshVault()
	}
}

func (a *app) closeTool() {
	a.tool = ""
	if a.scope.Bucket != "" {
		a.navSel = "b:" + a.scope.Bucket
	} else {
		a.navSel = ""
	}
}
