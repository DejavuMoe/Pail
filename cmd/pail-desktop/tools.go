package main

import (
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/icons"
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	"github.com/egoist/mygo/ui"
)

// toolInfo is the title of each tool view: functions, because the language can
// change at runtime. Use toolText to read one.
var toolInfo = map[string]struct{ title, line func() string }{
	"sync":       {locales.ToolsSyncTitle, locales.ToolsSyncLine},
	"cloudfront": {locales.ToolsCloudfrontTitle, locales.ToolsCloudfrontLine},
	"rules":      {locales.ToolsRulesTitle, locales.ToolsRulesLine},
	"mime":       {locales.ToolsMimeTitle, locales.ToolsMimeLine},
	"multipart":  {locales.ToolsMultipartTitle, locales.ToolsMultipartLine},
	"settings":   {locales.ToolsSettingsTitle, locales.ToolsSettingsLine},
}

// toolText is the title and the subtitle line of tool id in the language shown
// now; an unknown tool shows its id.
func toolText(id string) (title, line string) {
	info, ok := toolInfo[id]
	if !ok {
		return id, ""
	}
	return info.title(), info.line()
}

// toolState is the state of the tool views; each part lives in its own file.
type toolState struct {
	active  string // the tool whose state is loaded
	rules   rulesState
	mime    mimeState
	mp      mpState
	cf      cfState
	sync    syncState
	secrets secretsState
	tr      trAuto
}

// toolSync loads the state of the tool that just opened and releases the one
// that closed. It runs every frame from the sidebar and the tool view.
func (a *app) toolSync() {
	if a.tl.active == a.tool {
		return
	}
	prev := a.tl.active
	a.tl.active = a.tool
	switch prev {
	case "cloudfront":
		a.cfLeave()
	}
	switch a.tool {
	case "rules":
		a.rulesEnter()
	case "mime":
		a.mimeEnter()
	case "multipart":
		a.mpEnter()
	case "cloudfront":
		a.cfEnter()
	case "sync":
		a.syncEnter()
	}
}

// toolHeader is the left end of the top bar while a tool is open: a back
// affordance, the tool's icon and its title.
func (a *app) toolHeader(c *ui.Context) {
	k := tk(c)
	a.toolSync()
	title, _ := toolText(a.tool)
	cmd := a.cmd("tool." + a.tool)
	back := locales.ToolsBackToBrowser()
	if a.scope.Bucket != "" {
		back = locales.ToolsBackTo(a.scope.Bucket)
	}
	if ghostButton(c, icons.Get("arrow-left"), locales.CommonBack(), back, false).Clicked() {
		if a.tool == "sync" {
			a.syncGuard(a.closeTool)
		} else {
			a.closeTool()
		}
	}
	vline(c, 16).Margin(0, 4)
	if cmd != nil {
		ui.Icon(c, icons.Get(cmd.Icon)).TextColor(k.TextMuted)
	}
	sectionTitle(c, title)
}

// toolView replaces the browser in the main area, below the top bar.
func (a *app) toolView(c *ui.Context) {
	a.toolSync()
	switch a.tool {
	case "settings":
		a.settingsView(c)
	case "rules":
		a.rulesView(c)
	case "mime":
		a.mimeView(c)
	case "multipart":
		a.mpView(c)
	case "cloudfront":
		a.cfView(c)
	case "sync":
		a.syncView(c)
	default:
		title, line := toolText(a.tool)
		emptyBlock(c, "info", title, line, 0)
	}
	a.toolDialogs(c)
}

// toolDialogs draws the dialogs that belong to the open tool.
func (a *app) toolDialogs(c *ui.Context) {
	switch a.tool {
	case "rules":
		a.secretsDialog(c)
	case "cloudfront":
		a.invalidateDialog(c)
	}
}

// refChoices lists the secret references as select options, led by none.
func (a *app) refChoices(none, current string) []choice {
	out := []choice{{"", none}}
	seen := current == ""
	for _, r := range a.svc.SecretRefs() {
		out = append(out, choice{r, r})
		if r == current {
			seen = true
		}
	}
	if !seen {
		out = append(out, choice{current, locales.ToolsMissing(current)})
	}
	return out
}
