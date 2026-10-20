package main

import (
	"context"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/egoist/mygo/ui"
)

// The six standard headers the service lets users edit, in display order.
var stdHeaders = [6]string{"Content-Type", "Content-Encoding", "Content-Language", "Content-Disposition", "Cache-Control", "Expires"}

// propsForm is the Headers tab: the headers and user metadata of the selected
// object (or the values the selected objects share), with the edits made to
// them. Save sends the difference as one PropertiesPatch.
type propsForm struct {
	sig      string // the selection the form was built for
	gen      int    // head generation it was built from (single object)
	ready    bool
	loading  bool
	multi    bool // several objects, or folders, are behind the form
	count    int
	readOnly string

	hdrBase [6]kvItem // mixed + baseVal of each header
	hdrVal  [6]string
	hdrSet  [6]bool
	meta    []kvItem
	edit    inlineEdit
	err     string
	saving  bool
}

func (p *propsForm) reset(sig string) { *p = propsForm{sig: sig} }

// selectionSig identifies the current selection for the forms.
func (a *app) selectionSig() string {
	return a.scope.AccountID + "|" + a.scope.Bucket + "|" + a.scope.Prefix + "|" + strings.Join(a.selectedKeys(), "\x00")
}

// build fills the form from the objects' details. unknown marks values that
// could not be compared (folders, too many objects): every field then reads
// "Mixed".
func (p *propsForm) build(infos []core.ObjectInfo, unknown bool, folders int) {
	p.ready, p.loading = true, false
	p.count = len(infos) + folders
	p.multi = p.count > 1 || unknown
	p.readOnly = ""
	for _, in := range infos {
		if p.readOnly == "" {
			p.readOnly = in.ReadOnlyReason
		}
	}
	for i, name := range stdHeaders {
		var vals []string
		for _, in := range infos {
			v := ""
			for _, h := range in.Headers {
				if h.Name == name {
					v = h.Value
				}
			}
			vals = append(vals, v)
		}
		it := kvItem{key: name}
		if len(vals) > 0 {
			it.baseVal = vals[0]
			for _, v := range vals[1:] {
				if v != vals[0] {
					it.mixed = true
				}
			}
		}
		if unknown {
			it.mixed = true
		}
		p.hdrBase[i] = it
		p.hdrVal[i], p.hdrSet[i] = "", false
	}
	p.meta = p.meta[:0]
	if unknown {
		return
	}
	keys := map[string]int{}
	for _, in := range infos {
		for k := range in.Metadata {
			keys[k]++
		}
	}
	names := make([]string, 0, len(keys))
	for k := range keys {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		it := kvItem{key: k, baseVal: infos[0].Metadata[k], val: infos[0].Metadata[k]}
		if keys[k] != len(infos) {
			it.mixed = true
		}
		for _, in := range infos[1:] {
			if v, ok := in.Metadata[k]; ok && v != it.baseVal {
				it.mixed = true
			}
		}
		if it.mixed {
			it.val = ""
		}
		p.meta = append(p.meta, it)
	}
}

func (p *propsForm) dirty() bool {
	for _, s := range p.hdrSet {
		if s {
			return true
		}
	}
	return anyChanged(p.meta)
}

// revert drops every edit.
func (p *propsForm) revert() {
	for i := range p.hdrSet {
		p.hdrSet[i], p.hdrVal[i] = false, ""
	}
	kept := p.meta[:0]
	for _, it := range p.meta {
		if it.isNew {
			continue
		}
		it.removed, it.touched = false, false
		if it.mixed {
			it.val = ""
		} else {
			it.val = it.baseVal
		}
		kept = append(kept, it)
	}
	p.meta = kept
	p.edit.stop()
	p.err = ""
}

// patch turns the edits into the change the service applies.
func (p *propsForm) patch() (core.PropertiesPatch, string) {
	var out core.PropertiesPatch
	for i, set := range p.hdrSet {
		if !set {
			continue
		}
		if v := p.hdrVal[i]; v == "" {
			out.DelHeaders = append(out.DelHeaders, stdHeaders[i])
		} else {
			if out.SetHeaders == nil {
				out.SetHeaders = map[string]string{}
			}
			out.SetHeaders[stdHeaders[i]] = v
		}
	}
	seen := map[string]bool{}
	for _, it := range p.meta {
		if !it.isNew {
			seen[it.key] = true
		}
	}
	for _, it := range p.meta {
		key := strings.ToLower(strings.TrimSpace(it.key))
		switch {
		case it.isNew:
			if key == "" {
				continue
			}
			if seen[key] {
				return out, locales.EditPropsDuplicate(key)
			}
			seen[key] = true
			if out.SetMetadata == nil {
				out.SetMetadata = map[string]string{}
			}
			out.SetMetadata[key] = it.val
		case it.removed:
			out.DelMetadata = append(out.DelMetadata, it.key)
		case it.touched:
			if out.SetMetadata == nil {
				out.SetMetadata = map[string]string{}
			}
			out.SetMetadata[it.key] = it.val
		}
	}
	return out, ""
}

// summary words the patch for the confirmation of a multi-object save.
func (p propsForm) summary() string {
	pt, _ := p.patch()
	var parts []string
	for _, h := range stdHeaders {
		if _, ok := pt.SetHeaders[h]; ok {
			parts = append(parts, locales.EditPropsSet(h))
		}
		if slices.Contains(pt.DelHeaders, h) {
			parts = append(parts, locales.EditPropsRemove(h))
		}
	}
	for _, k := range sortedKeys(pt.SetMetadata) {
		parts = append(parts, locales.EditPropsSet(k))
	}
	for _, k := range pt.DelMetadata {
		parts = append(parts, locales.EditPropsRemove(k))
	}
	return strings.Join(parts, locales.EditPropsSeparator())
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---- loading ---------------------------------------------------------------------------

// ensureProps builds the form for the current selection once the details it
// needs have arrived: the single object's HEAD, or one HEAD per selected
// object.
func (a *app) ensureProps() {
	p := &a.mg.props
	keys := a.selectedKeys()
	if len(keys) == 0 || a.mg.del.on {
		return
	}
	sig := a.selectionSig()
	if len(keys) == 1 && !strings.HasSuffix(keys[0], "/") {
		if a.headState != 2 || a.headKey != keys[0] {
			return
		}
		if p.sig == sig && p.ready && p.gen == a.headGen {
			return
		}
		p.reset(sig)
		p.gen = a.headGen
		p.build([]core.ObjectInfo{a.head}, false, 0)
		return
	}
	if p.sig == sig && (p.ready || p.loading) {
		return
	}
	p.reset(sig)
	p.loading = true
	var files []string
	folders := 0
	for _, k := range keys {
		if strings.HasSuffix(k, "/") {
			folders++
		} else {
			files = append(files, k)
		}
	}
	unknown := folders > 0
	if len(files) > 100 {
		files, unknown = files[:100], true
	}
	loc := a.scope
	total := len(keys)
	a.bg(func() func() {
		ctx, cancel := a.timeout(60 * time.Second)
		defer cancel()
		infos, ok := a.headAll(ctx, loc, files)
		return func() {
			if a.mg.props.sig != sig {
				return
			}
			a.mg.props.build(infos, unknown || !ok, 0)
			a.mg.props.count = total
			a.mg.props.multi = true
		}
	})
}

// headAll reads the details of keys with a few parallel requests. ok is false
// when any of them failed.
func (a *app) headAll(ctx context.Context, loc Scope, keys []string) ([]core.ObjectInfo, bool) {
	infos := make([]core.ObjectInfo, len(keys))
	okAll := true
	var mu sync.Mutex
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i, k := range keys {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			in, err := a.svc.Head(ctx, loc, k, "")
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				okAll = false
				return
			}
			infos[i] = in
		}()
	}
	wg.Wait()
	if !okAll {
		return nil, false
	}
	return infos, true
}

// ---- view ------------------------------------------------------------------------------

// headersTab is the Headers tab of a single object.
func (a *app) headersTab(c *ui.Context) {
	a.ensureProps()
	a.propsBody(c)
}

// multiHeaders is the Headers tab of several selected items.
func (a *app) multiHeaders(c *ui.Context) {
	_, _, bytes := a.selectedInfo()
	a.header(c, "layers", locales.CommonItems(a.sel.Len())+" · "+humanBytes(bytes), locales.InspectorMultiple())
	a.ensureProps()
	a.propsBody(c)
}

// propsBody draws the headers and metadata sections.
func (a *app) propsBody(c *ui.Context) {
	p := &a.mg.props
	k := tk(c)
	if !p.ready {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c).Size(14, 14)
			mutedText(c, locales.EditPropsLoading())
		})
		return
	}
	editable := p.readOnly == ""
	ui.Column(c).Gap(16).Children(func() {
		ui.Column(c).Gap(8).Children(func() {
			help := locales.EditPropsHeadersHelp()
			if p.multi || p.count > 1 {
				help = locales.EditPropsHeadersMultiHelp()
			}
			labelHelp(c, help, func() ui.Element { return sectionTitle(c, locales.EditPropsHeaders()).FontSize(sizeBody) })
			for i := range stdHeaders {
				a.headerRow(c, i, editable)
			}
		})
		ui.Column(c).Gap(8).Children(func() {
			labelHelp(c, locales.EditPropsMetadataHelp(), func() ui.Element {
				return sectionTitle(c, locales.EditPropsMetadata()).FontSize(sizeBody)
			})
			kvList(c, &p.edit, &p.meta, kvOpts{prefix: "m", editable: editable, keyHint: locales.EditPropsKeyHint(), addLabel: locales.EditPropsAdd()})
		})
		if !editable {
			ui.Row(c).Gap(6).AlignItems(ui.Start).Children(func() {
				mutedText(c, locales.EditPropsReadOnly(p.readOnly)).Grow(1).MinWidth(0)
			})
		}
		_ = k
	})
}

func (a *app) headerRow(c *ui.Context, i int, editable bool) {
	p := &a.mg.props
	base := p.hdrBase[i]
	id := "h" + strconv.Itoa(i)
	val, placeholder, draft := base.baseVal, locales.EditPropsNotSet(), base.baseVal
	if base.mixed {
		val, placeholder, draft = "", locales.EditPropsMixed(), ""
	}
	if p.hdrSet[i] {
		val, draft = p.hdrVal[i], p.hdrVal[i]
		placeholder = locales.EditPropsNotSet()
	}
	var acts []rowAction
	if editable && base.mixed && !p.hdrSet[i] {
		acts = append(acts, rowAction{"x", locales.EditPropsClear(stdHeaders[i]), func() { p.hdrSet[i], p.hdrVal[i] = true, "" }})
	}
	if editable && p.hdrSet[i] {
		acts = append(acts, rowAction{"undo-2", locales.EditPropsUndo(stdHeaders[i]), func() { p.hdrSet[i], p.hdrVal[i] = false, "" }})
	}
	nv, ok := editRow(c, &p.edit, editSpec{key: id, label: stdHeaders[i], help: headerHelp(i), value: val, placeholder: placeholder, changed: p.hdrSet[i],
		editable: editable, draft: draft, actions: acts})
	if !ok {
		return
	}
	nv = strings.TrimSpace(nv)
	switch {
	case base.mixed && !p.hdrSet[i] && nv == "":
	case !base.mixed && nv == base.baseVal:
		p.hdrSet[i], p.hdrVal[i] = false, ""
	default:
		p.hdrSet[i], p.hdrVal[i] = true, nv
	}
}

// headerHelp is the "?" text of the i-th standard header.
func headerHelp(i int) string {
	switch stdHeaders[i] {
	case "Content-Type":
		return locales.EditPropsHelpContentType()
	case "Content-Encoding":
		return locales.EditPropsHelpContentEncoding()
	case "Content-Language":
		return locales.EditPropsHelpContentLanguage()
	case "Content-Disposition":
		return locales.EditPropsHelpContentDisposition()
	case "Cache-Control":
		return locales.EditPropsHelpCacheControl()
	case "Expires":
		return locales.EditPropsHelpExpires()
	}
	return ""
}

// ---- saving ----------------------------------------------------------------------------

// saveProps applies the edits to the selection. Several objects are rewritten,
// so that goes through the standard confirmation first.
func (a *app) saveProps() {
	p := &a.mg.props
	if p.saving || !p.ready {
		return
	}
	// An edit still open counts as made.
	a.commitInlineEdit()
	pt, msg := p.patch()
	if msg != "" {
		p.err = msg
		return
	}
	if !p.dirty() {
		return
	}
	keys := a.selectedKeys()
	loc := a.scope
	if p.multi || p.count > 1 {
		n := p.count
		if n < len(keys) {
			n = len(keys)
		}
		rows := a.scopeRows(loc)
		rows = append(rows, KV{locales.EditPropsScope(), locales.CommonObjects(n)}, KV{locales.EditPropsChanges(), p.summary()})
		a.confirm = confirmState{
			open: true, title: locales.EditPropsUpdateTitle(len(keys)), rows: rows,
			action: locales.EditPropsUpdateAction(len(keys)), message: locales.EditPropsUpdateMessage(),
			run: func() { a.submitProps(loc, keys, pt) },
		}
		return
	}
	a.submitProps(loc, keys, pt)
}

// commitInlineEdit applies a text field that is still open to its form.
func (a *app) commitInlineEdit() {
	p := &a.mg.props
	if p.edit.id == "" {
		return
	}
	id, draft := p.edit.id, strings.TrimSpace(p.edit.draft)
	p.edit.stop()
	switch {
	case strings.HasPrefix(id, "h"):
		i, err := strconv.Atoi(id[1:])
		if err != nil || i < 0 || i >= len(stdHeaders) {
			return
		}
		base := p.hdrBase[i]
		switch {
		case base.mixed && !p.hdrSet[i] && draft == "":
		case !base.mixed && draft == base.baseVal:
			p.hdrSet[i], p.hdrVal[i] = false, ""
		default:
			p.hdrSet[i], p.hdrVal[i] = true, draft
		}
	case strings.HasPrefix(id, "m"):
		s := strings.TrimPrefix(id, "m")
		s = strings.TrimSuffix(s, "k")
		i, err := strconv.Atoi(s)
		if err != nil || i < 0 || i >= len(p.meta) {
			return
		}
		it := &p.meta[i]
		if strings.HasSuffix(id, "k") {
			if draft == "" {
				p.meta = append(p.meta[:i], p.meta[i+1:]...)
			} else {
				it.key = draft
			}
			return
		}
		switch {
		case it.isNew:
			it.val, it.touched = draft, true
		case it.mixed && !it.touched && draft == "":
		case !it.mixed && draft == it.baseVal:
			it.val, it.touched = it.baseVal, false
		default:
			it.val, it.touched = draft, true
		}
	}
}

func (a *app) submitProps(loc Scope, keys []string, pt core.PropertiesPatch) {
	p := &a.mg.props
	sig := p.sig
	p.saving, p.err = true, ""
	a.bg(func() func() {
		id, err := a.svc.SetProperties(core.Selection{Location: loc, Keys: keys}, pt)
		return func() {
			if err != nil {
				p.saving, p.err = false, humanError(err)
				return
			}
			a.follow(id, func(t Task) {
				p.saving = false
				sum := a.bulkSummary(t)
				if t.Status != core.StatusSucceeded || sum.failed > 0 {
					msg := sum.firstErr
					if msg == "" {
						msg = locales.EditPropsNotApplied()
					}
					if a.mg.props.sig == sig {
						p.err = msg
					}
					a.toast(locales.EditPropsSaveFailed(msg))
					a.reloadSame(loc)
					return
				}
				if a.mg.props.sig == sig {
					p.sig, p.ready, p.err = "", false, ""
					p.edit.stop()
				}
				if loc == a.scope {
					a.headKey = "" // read the new values
				}
				a.reloadSame(loc)
				a.toast(locales.CommonSaved())
			})
		}
	})
}

// reloadSame refreshes the listing when it still shows loc's bucket.
func (a *app) reloadSame(loc Scope) {
	if loc.AccountID == a.scope.AccountID && loc.Bucket == a.scope.Bucket && a.scope.Bucket != "" {
		a.reload()
	}
}

// inspectorFooter is the slim bar with Revert and Save that appears while the
// open tab has unsaved edits.
func (a *app) inspectorFooter(c *ui.Context) {
	k := tk(c)
	var dirty bool
	var saving bool
	var errText string
	var revert, save func()
	versionNote := false
	switch a.inspTab {
	case 1:
		p := &a.mg.props
		dirty, saving, errText = p.ready && p.dirty(), p.saving, p.err
		revert, save = func() { p.revert() }, a.saveProps
		versionNote = a.versioningOn()
	case 2:
		t := &a.mg.tags
		dirty, saving, errText = t.ready && t.dirty(), t.saving, t.err
		if t.fix {
			errText = locales.EditTagsFix()
		}
		revert, save = func() { t.revert() }, a.saveTags
	default:
		return
	}
	if !dirty && !saving && errText == "" {
		return
	}
	ui.Column(c.Key("insp-footer")).Padding(8, 16, 8, 16).Gap(6).Background(k.Background).BorderWidth(1, 0, 0, 0).BorderColor(k.Border).Children(func() {
		if errText != "" {
			errorLine(c, errText)
		}
		ui.Row(c).Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(func() {
			if saving {
				ui.Spinner(c).Size(14, 14)
			}
			if ghostButton(c, nil, locales.CommonRevert(), locales.EditPropsRevertTip(), saving).Clicked() {
				revert()
			}
			if primaryButton(c, locales.CommonSave(), saving || !dirty).Clicked() {
				save()
			}
		})
		if versionNote && a.inspTab == 1 {
			noteLine(c, locales.EditPropsNewVersion())
		}
	})
}

// versioningOn is true when the open bucket keeps versions of what it saves.
func (a *app) versioningOn() bool {
	st, _ := a.versioningOf(a.accountID, a.scope.Bucket)
	return st == "Enabled"
}
