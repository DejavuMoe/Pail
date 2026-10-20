package main

import (
	"sort"
	"strings"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/egoist/mygo/ui"
)

const maxObjectTags = 10

// tagsForm is the Tags tab of a single object.
type tagsForm struct {
	key    string
	gen    int
	ready  bool
	items  []kvItem
	edit   inlineEdit
	err    string
	fix    bool // the tags have mistakes: the footer says so (in the language of the moment)
	saving bool
}

func (t *tagsForm) load(key string, gen int, tags map[string]string) {
	*t = tagsForm{key: key, gen: gen, ready: true}
	names := make([]string, 0, len(tags))
	for k := range tags {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		t.items = append(t.items, kvItem{key: k, val: tags[k], baseVal: tags[k]})
	}
}

func (t *tagsForm) dirty() bool { return anyChanged(t.items) }

func (t *tagsForm) revert() {
	kept := t.items[:0]
	for _, it := range t.items {
		if it.isNew {
			continue
		}
		it.removed, it.touched, it.val = false, false, it.baseVal
		kept = append(kept, it)
	}
	t.items = kept
	t.edit.stop()
	t.err, t.fix = "", false
}

// result is the tag set the form describes.
func (t *tagsForm) result() map[string]string {
	out := map[string]string{}
	for _, it := range t.items {
		if it.removed || strings.TrimSpace(it.key) == "" {
			continue
		}
		out[strings.TrimSpace(it.key)] = it.val
	}
	return out
}

// errAt validates tag i: keys are not empty or repeated.
func (t *tagsForm) errAt(i int) string {
	it := t.items[i]
	if it.removed {
		return ""
	}
	key := strings.TrimSpace(it.key)
	if key == "" {
		return locales.EditTagsEnterKey()
	}
	if strings.HasPrefix(strings.ToLower(key), "aws:") {
		return locales.EditTagsReserved()
	}
	for j, o := range t.items {
		if j != i && !o.removed && strings.TrimSpace(o.key) == key {
			return locales.EditTagsTwice()
		}
	}
	return ""
}

func (t *tagsForm) valid() bool {
	for i := range t.items {
		if t.errAt(i) != "" {
			return false
		}
	}
	return true
}

// ensureTags loads the form when the details of the selected object are in.
func (a *app) ensureTags() {
	t := &a.mg.tags
	keys := a.selectedKeys()
	if len(keys) != 1 || strings.HasSuffix(keys[0], "/") || a.headState != 2 || a.headKey != keys[0] {
		return
	}
	if t.ready && t.key == keys[0] && t.gen == a.headGen {
		return
	}
	t.load(keys[0], a.headGen, a.head.Tags)
}

// tagsTab is the Tags tab: key/value rows with add and remove.
func (a *app) tagsTab(c *ui.Context) {
	a.ensureTags()
	t := &a.mg.tags
	if a.head.TagsErr != nil {
		mutedText(c, locales.EditTagsUnavailable(humanError(a.head.TagsErr)))
		return
	}
	if !t.ready {
		return
	}
	ui.Column(c).Gap(8).Children(func() {
		labelHelp(c, locales.EditTagsHelp(), func() ui.Element { return sectionTitle(c, locales.EditTagsTitle()).FontSize(sizeBody) })
		kvList(c, &t.edit, &t.items, kvOpts{prefix: "t", editable: true, maxItems: maxObjectTags, keyHint: locales.EditTagsKeyHint(),
			addLabel: locales.EditTagsAdd(), errAt: t.errAt})
	})
}

func (a *app) saveTags() {
	t := &a.mg.tags
	if t.saving || !t.ready {
		return
	}
	// An open text field counts as typed.
	if t.edit.id != "" {
		t.edit.stop()
	}
	if !t.valid() {
		t.fix = true
		return
	}
	tags := t.result()
	loc, key := a.scope, t.key
	t.saving, t.err, t.fix = true, "", false
	a.bg(func() func() {
		ctx, cancel := a.shortCtx()
		defer cancel()
		err := a.svc.SetTags(ctx, loc, core.ObjectRef{Key: key}, tags)
		return func() {
			t.saving = false
			if err != nil {
				t.err = humanError(err)
				return
			}
			if loc == a.scope && a.headKey == key {
				a.headKey = "" // read the new tags
			}
			t.ready = false
			a.toast(locales.CommonSaved())
		}
	})
}
