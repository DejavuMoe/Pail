package main

import (
	"maps"
	"mime"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/icons"
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	"github.com/egoist/mygo/ui"
)

// MIME types tool: the user's extension -> Content-Type table, edited as a
// draft and saved as a whole.

type mimeRow struct{ ext, typ string }

type mimeState struct {
	saved    map[string]string
	rows     []mimeRow
	addExt   string
	addType  string
	addErr   string
	err      string
	saving   bool
	testName string
	list     ui.ListState
}

func mimeColumns() []ui.TableColumn {
	return []ui.TableColumn{
		{Title: locales.MimeExtension(), ID: "ext", Width: 160, MinWidth: 100},
		{Title: locales.MimeContentType(), ID: "type", MinWidth: 200},
		{Title: "", ID: "del", Width: 44, Fixed: true},
	}
}

// normExt lower-cases an extension and adds the leading dot; "" stays "".
func normExt(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" || s == "." {
		return ""
	}
	if !strings.HasPrefix(s, ".") {
		s = "." + s
	}
	return s
}

// table is the draft as a map plus the first duplicated extension.
func (m *mimeState) table() (map[string]string, string) {
	out := map[string]string{}
	dup := ""
	for _, r := range m.rows {
		e := normExt(r.ext)
		if e == "" {
			continue
		}
		if _, ok := out[e]; ok && dup == "" {
			dup = e
		}
		out[e] = strings.TrimSpace(r.typ)
	}
	return out, dup
}

func (m *mimeState) dirty() bool {
	t, dup := m.table()
	return dup != "" || !maps.Equal(t, m.saved)
}

// draftType is the Content-Type of name under the draft: the custom table,
// then the platform's table, then application/octet-stream.
func (m *mimeState) draftType(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	if ext == "" {
		return "application/octet-stream"
	}
	t, _ := m.table()
	if ct := t[ext]; ct != "" {
		return ct
	}
	if ct := mime.TypeByExtension(ext); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

func (a *app) mimeEnter() {
	m := &a.tl.mime
	*m = mimeState{}
	a.mimeReload()
}

func (a *app) mimeReload() {
	m := &a.tl.mime
	m.saved = a.svc.MIMETypes()
	m.rows = m.rows[:0]
	for _, e := range slices.Sorted(maps.Keys(m.saved)) {
		m.rows = append(m.rows, mimeRow{e, m.saved[e]})
	}
	m.err = ""
}

func (a *app) mimeAdd() {
	m := &a.tl.mime
	ext, typ := normExt(m.addExt), strings.TrimSpace(m.addType)
	switch {
	case ext == "":
		m.addErr = locales.MimeEnterExt()
		return
	case typ == "" || !strings.Contains(typ, "/"):
		m.addErr = locales.MimeEnterType()
		return
	}
	for _, r := range m.rows {
		if normExt(r.ext) == ext {
			m.addErr = locales.MimeAlreadyListed(ext)
			return
		}
	}
	m.rows = append([]mimeRow{{ext, typ}}, m.rows...)
	m.addExt, m.addType, m.addErr = "", "", ""
}

func (a *app) mimeSave() {
	m := &a.tl.mime
	if m.saving {
		return
	}
	t, dup := m.table()
	if dup != "" {
		m.err = locales.MimeDuplicate(dup)
		return
	}
	m.saving, m.err = true, ""
	a.bg(func() func() {
		err := a.svc.SaveMIMETypes(t)
		return func() {
			m.saving = false
			if err != nil {
				m.err = humanError(err)
				return
			}
			a.mimeReload()
			a.toast(locales.MimeSaved())
		}
	})
}

func (a *app) mimeView(c *ui.Context) {
	k := tk(c)
	m := &a.tl.mime
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		ui.Row(c).Padding(12, 16, 8, 16).Gap(8).AlignItems(ui.Start).Shrink(0).Children(func() {
			ui.Box(c).Width(160).Shrink(0).Children(func() {
				formFieldHelp(c, locales.MimeExtension(), locales.MimeExtensionHelp(), "", func() {
					in := inputBox(c, &m.addExt, locales.MimeNewExt(), ".ext", inputOpts{mono: true, invalid: m.addErr != ""})
					if in.Changed() {
						m.addErr = ""
					}
					if in.Submitted() {
						a.mimeAdd()
					}
				})
			})
			ui.Box(c).Grow(1).MinWidth(0).Children(func() {
				formFieldHelp(c, locales.MimeContentType(), locales.MimeContentTypeHelp(), m.addErr, func() {
					in := inputBox(c, &m.addType, locales.MimeNewType(), "type/subtype", inputOpts{mono: true, invalid: m.addErr != ""})
					if in.Changed() {
						m.addErr = ""
					}
					if in.Submitted() {
						a.mimeAdd()
					}
				})
			})
			ui.Box(c).Margin(18, 0, 0, 0).Children(func() {
				if secondaryButton(c, locales.CommonAdd(), false).Clicked() {
					a.mimeAdd()
				}
			})
		})
		if len(m.rows) == 0 {
			emptyBlock(c, "file-type", locales.MimeEmpty(), "", 0)
		} else {
			rows := m.rows
			m.list.Key = func(i int) any { return i }
			del := -1
			toolTable(c, &m.list, mimeColumns(), len(rows), func(row, col int) {
				switch col {
				case 0:
					ui.Box(c.Key("ext")).Height(24).Children(func() {
						mono(ui.TextInputBase(c.Key("mx"), &rows[row].ext).Label(locales.MimeExtensionN(strconv.Itoa(row + 1))).FontSize(sizeSecondary).Grow(1).MinWidth(0))
					})
				case 1:
					ui.Box(c.Key("typ")).Height(24).Children(func() {
						mono(ui.TextInputBase(c.Key("mt"), &rows[row].typ).Label(locales.MimeContentTypeN(strconv.Itoa(row + 1))).FontSize(sizeSecondary).Grow(1).MinWidth(0))
					})
				case 2:
					if iconButton(c, icons.Get("trash-2"), locales.MimeDeleteExt(rows[row].ext)).Size(22, 22).Clicked() {
						del = row
					}
				}
			}).Grow(1).MinHeight(0).Label(locales.MimeTableLabel())
			if del >= 0 {
				m.rows = slices.Delete(slices.Clone(m.rows), del, del+1)
			}
		}
		ui.Row(c).Height(48).Padding(0, 16).Gap(12).AlignItems(ui.Center).Shrink(0).
			BorderWidth(1, 0, 0, 0).BorderColor(k.Border).Children(func() {
			labelHelp(c, locales.MimeTestHelp(), func() ui.Element {
				return ui.Text(c, locales.MimeTest()).FontSize(sizeSecondary).FontWeight(600).TextColor(k.Text)
			})
			inputBox(c, &m.testName, locales.MimeFileName(), locales.MimeFileName(), inputOpts{width: 260, mono: true})
			if strings.TrimSpace(m.testName) != "" {
				ui.Text(c, locales.MimeDraftResult()).FontSize(sizeSecondary).TextColor(k.TextMuted).Shrink(0)
				mono(ui.Text(c, m.draftType(strings.TrimSpace(m.testName))).FontSize(sizeSecondary).TextColor(k.Text).SingleLine())
			}
			ui.Spacer(c)
			if m.err != "" {
				ui.Text(c, m.err).FontSize(sizeSecondary).TextColor(k.Danger).SingleLine().MinWidth(0)
			}
			dirty := m.dirty()
			if dirty && ghostButton(c, nil, locales.CommonRevert(), locales.MimeDiscardTip(), m.saving).Clicked() {
				a.mimeReload()
			}
			if primaryButton(c, locales.CommonSave(), !dirty || m.saving).Clicked() {
				a.mimeSave()
			}
		})
	})
}
