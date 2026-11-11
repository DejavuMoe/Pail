package main

import (
	"context"
	"fmt"
	"hash/maphash"
	"math/bits"
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
	saveID  int // the save last sent from this form (app.propsSaves); 0 once rebuilt
}

func (p *propsForm) reset(sig string) { *p = propsForm{sig: sig} }

// selectionSig identifies the current selection for the forms.
func (a *app) selectionSig() string {
	head := a.scope.AccountID + "|" + a.scope.Bucket + "|" + a.scope.Prefix + "|"
	if n := a.selectedCount(); n > sigExactMax {
		// A big selection is told apart by its size and an order-independent
		// 128-bit hash of its keys, instead of by all of them, sorted, every frame.
		var sum, mix uint64
		for k := range a.sel.All() {
			if _, ok := a.byKey[k]; ok {
				h := maphash.String(sigSeed, k)
				sum += h
				mix ^= bits.RotateLeft64(h*0x9e3779b97f4a7c15, int(h&63))
			}
		}
		return fmt.Sprintf("%s#%d:%016x%016x", head, n, sum, mix)
	}
	return head + strings.Join(a.selectedKeys(), "\x00")
}

// sigExactMax is the size up to which a selection signature holds the keys.
const sigExactMax = 16

var sigSeed = maphash.MakeSeed()

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

// rebase moves the edits onto a new base q (a form built from fresher details,
// or from the values a save wrote). Fields nobody edited show the new values;
// edited ones keep what was typed, and stop counting as edits where the new
// base already holds it. An open text field stays open on its field.
func (p *propsForm) rebase(q *propsForm) {
	for i := range stdHeaders {
		nb := q.hdrBase[i]
		if p.hdrSet[i] && !nb.mixed && p.hdrVal[i] == nb.baseVal {
			p.hdrSet[i], p.hdrVal[i] = false, ""
		}
		p.hdrBase[i] = nb
	}
	var moved []int
	p.meta, moved = rebaseItems(p.meta, q.meta, metaKey)
	remapEdit(&p.edit, "m", moved)
	p.readOnly = q.readOnly
}

// savedBase is the base the form describes once pt, sent from it, has been
// applied: the headers and metadata it set or removed take their new values.
func (p *propsForm) savedBase(pt core.PropertiesPatch) *propsForm {
	q := &propsForm{hdrBase: p.hdrBase, readOnly: p.readOnly}
	for i, name := range stdHeaders {
		if v, ok := pt.SetHeaders[name]; ok {
			q.hdrBase[i] = kvItem{key: name, baseVal: v}
		}
		if slices.Contains(pt.DelHeaders, name) {
			q.hdrBase[i] = kvItem{key: name}
		}
	}
	del := map[string]bool{}
	for _, k := range pt.DelMetadata {
		del[metaKey(k)] = true
	}
	set := map[string]string{}
	for k, v := range pt.SetMetadata {
		set[metaKey(k)] = v
	}
	for _, it := range p.meta {
		k := metaKey(it.key)
		if it.isNew || del[k] {
			continue
		}
		b := kvItem{key: it.key, baseVal: it.baseVal, mixed: it.mixed}
		if v, ok := set[k]; ok {
			b.baseVal, b.mixed = v, false
			delete(set, k)
		}
		if !b.mixed {
			b.val = b.baseVal
		}
		q.meta = append(q.meta, b)
	}
	for k, v := range set {
		q.meta = append(q.meta, kvItem{key: k, baseVal: v, val: v})
	}
	sort.SliceStable(q.meta, func(i, j int) bool { return q.meta[i].key < q.meta[j].key })
	return q
}

// metaKey is how a metadata name is compared: user metadata names are not
// case sensitive.
func metaKey(k string) string { return strings.ToLower(strings.TrimSpace(k)) }

// rebaseItems moves the edits of a key/value list onto base, the pairs loaded
// afresh (sorted, unedited). Pairs nobody edited follow base; a changed value,
// a removal or an added pair is kept, and an added pair or a changed value the
// base already holds stops being an edit. moved maps each old index to its new
// one, -1 for a pair that went away.
func rebaseItems(items, base []kvItem, norm func(string) string) (out []kvItem, moved []int) {
	out = slices.Clone(base)
	at := map[string]int{}
	for j, b := range out {
		at[norm(b.key)] = j
	}
	claimed := make([]bool, len(out))
	moved = make([]int, len(items))
	for i, it := range items {
		j, ok := at[norm(it.key)]
		if ok && claimed[j] {
			ok = false // a second pair of the same name: keep it as it is
		}
		edited := it.isNew || it.touched || it.removed
		switch {
		case it.isNew && (!ok || strings.TrimSpace(it.key) == ""), !it.isNew && !ok && it.touched && !it.removed:
			// Added, or changed on a pair the base no longer has: an addition.
			add := it
			add.isNew, add.removed, add.baseVal, add.mixed = true, false, "", false
			out = append(out, add)
			moved[i] = len(out) - 1
			continue
		case !ok:
			moved[i] = -1 // unedited or removed, and gone from the base
			continue
		}
		if edited {
			claimed[j] = true
		}
		b := &out[j]
		switch {
		case it.removed:
			b.removed = true
		case it.isNew || it.touched:
			if !b.mixed && it.val == b.baseVal {
				b.val, b.touched = b.baseVal, false
			} else {
				b.val, b.touched = it.val, true
			}
		}
		moved[i] = j
	}
	return out, moved
}

// remapEdit keeps an open field of a key/value list (ids prefix+index, and
// +"k" for the name of a new pair) on its pair after the list was rebuilt.
func remapEdit(e *inlineEdit, prefix string, moved []int) {
	if e.id == "" || !strings.HasPrefix(e.id, prefix) {
		return
	}
	s := strings.TrimPrefix(e.id, prefix)
	suffix := ""
	if strings.HasSuffix(s, "k") {
		s, suffix = strings.TrimSuffix(s, "k"), "k"
	}
	i, err := strconv.Atoi(s)
	if err != nil || i < 0 || i >= len(moved) || moved[i] < 0 {
		e.stop()
		return
	}
	e.id = prefix + strconv.Itoa(moved[i]) + suffix
}

// ---- loading ---------------------------------------------------------------------------

// ensureProps builds the form for the current selection once the details it
// needs have arrived: the single object's HEAD, or one HEAD per selected
// object.
func (a *app) ensureProps() {
	p := &a.mg.props
	n := a.selectedCount()
	if n == 0 || a.mg.del.on {
		return
	}
	sig := a.selectionSig()
	if one, _ := a.soleSelected(); n == 1 && !strings.HasSuffix(one, "/") {
		if a.headState != loadReady || a.headKey != one {
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
	keys := a.selectedKeys()
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
	// Saving several objects is decided where it is pressed: the button says
	// how many it rewrites (propsSaveLabel), so no second question follows.
	a.submitProps(a.scope, a.selectedKeys(), pt)
}

// propsSaveLabel is the Save button of the Headers tab: "Save" for one object,
// "Update N items" when the save rewrites several.
func (a *app) propsSaveLabel() string {
	p := &a.mg.props
	if n := len(a.selectedKeys()); p.multi || p.count > 1 || n > 1 {
		return locales.EditPropsUpdateAction(max(n, p.count))
	}
	return locales.CommonSave()
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

// submitProps sends pt for keys. The form may be rebuilt for another
// selection while the save runs: its end then only touches the form it was
// sent from (saveID), and only the details of the objects it changed.
func (a *app) submitProps(loc Scope, keys []string, pt core.PropertiesPatch) {
	p := &a.mg.props
	a.propsSaves++
	save := a.propsSaves
	p.saveID = save
	p.saving, p.err = true, ""
	a.bg(func() func() {
		id, err := a.svc.SetProperties(core.Selection{Location: loc, Keys: keys}, pt)
		return func() {
			if err != nil {
				if p.saveID == save {
					p.saving, p.err = false, humanError(err)
				} else {
					a.toast(locales.EditPropsSaveFailed(humanError(err)))
				}
				return
			}
			a.follow(id, func(t Task) {
				own := p.saveID == save
				if own {
					p.saving = false
				}
				sum := a.bulkSummary(t)
				if t.Status != core.StatusSucceeded || sum.failed > 0 {
					msg := sum.firstErr
					if msg == "" {
						msg = locales.EditPropsNotApplied()
					}
					if own {
						p.err = msg
					}
					a.toast(locales.EditPropsSaveFailed(msg))
					a.reloadSame(loc)
					return
				}
				if own {
					a.propsSaved(pt)
				}
				a.headTouched(loc, false, keys...) // read the new values
				a.reloadSame(loc)
				a.toast(locales.CommonSaved())
			})
		}
	})
}

// propsSaved takes the values pt wrote as the base of the form it was sent
// from. Edits made while the save ran stay; with none, the form is read
// afresh from the objects.
func (a *app) propsSaved(pt core.PropertiesPatch) {
	p := &a.mg.props
	p.err = ""
	p.rebase(p.savedBase(pt))
	if !p.dirty() && p.edit.id == "" {
		p.sig, p.ready = "", false
	}
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
	saveLabel := locales.CommonSave()
	versionNote := false
	switch a.inspTab {
	case 1:
		p := &a.mg.props
		dirty, saving, errText = p.ready && p.dirty(), p.saving, p.err
		revert, save = func() { p.revert() }, a.saveProps
		saveLabel = a.propsSaveLabel()
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
			if primaryButton(c, saveLabel, saving || !dirty).Clicked() {
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
