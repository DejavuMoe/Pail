package main

import (
	"strconv"
	"strings"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	"github.com/egoist/mygo/ui"
)

// kvItem is one editable key/value pair of the metadata or tag lists. Existing
// items keep the value they were loaded with so that the edit can tell what
// changed; mixed means the selected objects do not agree on it.
type kvItem struct {
	key, val string
	baseVal  string
	mixed    bool
	isNew    bool
	touched  bool // the value was replaced
	removed  bool
}

// changed reports whether the item differs from what was loaded.
func (it kvItem) changed() bool {
	if it.isNew {
		return strings.TrimSpace(it.key) != ""
	}
	return it.removed || it.touched
}

func anyChanged(items []kvItem) bool {
	for _, it := range items {
		if it.changed() {
			return true
		}
	}
	return false
}

// kvOpts tune kvList.
type kvOpts struct {
	prefix   string // element and edit ids
	editable bool
	maxItems int // 0: unlimited
	keyHint  string
	addLabel string
	errAt    func(i int) string // a message under item i, "" for none
}

// editRow draws one label over value row. The label line carries the row's
// action icons; the value is click-to-edit. It returns the new text when an
// edit was committed.
type editSpec struct {
	key         string // element key and edit id
	label       string
	labelMono   bool
	strike      bool
	value       string
	help        string // the "?" after the label ("" for none)
	placeholder string // shown in TextSubtle when value is empty
	changed     bool
	editable    bool
	mono        bool
	draft       string // the text an edit starts from
	err         string
	actions     []rowAction
}

type rowAction struct {
	icon, label string
	run         func()
}

func editRow(c *ui.Context, e *inlineEdit, s editSpec) (string, bool) {
	k := tk(c)
	var out string
	var committed bool
	ui.Column(c.Key(s.key)).Gap(1).Children(func() {
		editing := e.on(s.key)
		ui.Row(c).Height(24).Gap(2).AlignItems(ui.Center).Children(func() {
			// The label and its "?" share one growing box. The label itself must
			// not Grow: that also sets a 0 basis, which freezes a single-line text
			// (whose min-content is 0) at no width, leaving only an ellipsis.
			ui.Row(c).Gap(2).AlignItems(ui.Center).Grow(1).MinWidth(0).Children(func() {
				l := ui.Text(c, s.label).FontSize(sizeSecondary).TextColor(k.TextMuted).SingleLine().Shrink(1).MinWidth(0).Tooltip(s.label)
				if s.labelMono {
					mono(l)
				}
				if s.strike {
					l.Strikethrough()
				}
				if s.help != "" {
					helpIcon(c, s.help)
				}
			})
			if editing {
				return
			}
			if s.editable {
				if iconButton(c, iconOf("pencil"), locales.KveditEdit(s.label)).Clicked() {
					e.start(s.key, s.draft)
				}
			}
			for _, act := range s.actions {
				if iconButton(c, iconOf(act.icon), act.label).Clicked() {
					act.run()
				}
			}
		})
		if editing {
			done, keep := inlineInput(c, e, s.label, s.placeholder, inputOpts{mono: s.mono, invalid: s.err != ""})
			if done {
				e.stop()
				if keep {
					out, committed = e.draft, true
				}
			}
		} else {
			text, subtle := s.value, false
			if text == "" {
				text, subtle = s.placeholder, true
			}
			if s.changed {
				subtle = false
			}
			b := valueButton(c, s.key+":v", text, subtle, s.mono, s.editable, locales.KveditEdit(s.label))
			if b.Clicked() {
				e.start(s.key, s.draft)
			}
		}
		if s.err != "" {
			ui.Text(c, s.err).FontSize(sizeSecondary).TextColor(k.Danger)
		}
	})
	return out, committed
}

// kvList draws the rows of items and the "Add" button, and applies edits.
func kvList(c *ui.Context, e *inlineEdit, items *[]kvItem, o kvOpts) {
	k := tk(c)
	ui.Column(c.Key(o.prefix + "list")).Gap(8).Children(func() {
		if len(*items) == 0 {
			mutedText(c, locales.CommonNone())
		}
		for i := 0; i < len(*items); i++ {
			it := &(*items)[i]
			id := o.prefix + strconv.Itoa(i)
			errText := ""
			if o.errAt != nil {
				errText = o.errAt(i)
			}
			switch {
			case it.isNew && it.key == "" && o.editable:
				// A new pair: the key is typed first, then its value.
				kid := id + "k"
				if !e.on(kid) {
					e.start(kid, "")
				}
				ui.Column(c.Key(id)).Gap(1).Children(func() {
					ui.Text(c, locales.KveditNew(o.keyHint)).FontSize(sizeSecondary).TextColor(k.TextSubtle).Height(24)
					done, keep := inlineInput(c, e, o.keyHint, o.keyHint, inputOpts{mono: true})
					if done {
						e.stop()
						key := strings.TrimSpace(e.draft)
						if keep && key != "" {
							it.key = key
							e.start(id, "")
						} else {
							*items = append((*items)[:i], (*items)[i+1:]...)
							i--
						}
					}
				})
			case it.removed:
				editRow(c, e, editSpec{key: id, label: it.key, labelMono: true, strike: true, value: locales.KveditRemoved(), changed: false,
					actions: []rowAction{{"undo-2", locales.KveditKeep(it.key), func() { it.removed = false }}}})
			default:
				placeholder := locales.KveditEmpty()
				val := it.val
				if it.mixed && !it.touched {
					placeholder, val = locales.KveditMixed(), ""
				}
				draft := it.val
				if it.mixed && !it.touched {
					draft = ""
				}
				acts := []rowAction{{"x", locales.KveditRemove(it.key), func() {
					if it.isNew {
						*items = append((*items)[:i], (*items)[i+1:]...)
						return
					}
					it.removed = true
				}}}
				if !o.editable {
					acts = nil
				}
				nv, ok := editRow(c, e, editSpec{key: id, label: it.key, labelMono: true, value: val, placeholder: placeholder,
					changed: it.touched || it.isNew, editable: o.editable, mono: false, draft: draft, err: errText, actions: acts})
				if ok {
					nv = strings.TrimSpace(nv)
					switch {
					case it.isNew:
						it.val, it.touched = nv, true
					case it.mixed && !it.touched && nv == "":
						// An empty answer for a value the objects disagree on keeps it.
					case !it.mixed && nv == it.baseVal:
						it.val, it.touched = it.baseVal, false
					default:
						it.val, it.touched = nv, true
					}
				}
			}
		}
		if o.editable {
			add := ghostButton(c, iconOf("plus"), o.addLabel, o.addLabel, o.maxItems > 0 && len(*items) >= o.maxItems).AlignSelf(ui.Start)
			if add.Clicked() {
				*items = append(*items, kvItem{isNew: true})
			}
		}
	})
}
