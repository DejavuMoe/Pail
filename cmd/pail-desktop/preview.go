package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// The Preview tab fetches the first bytes of one object with Service.Preview
// and shows them by what they are: JSON pretty-printed in the read-only code
// editor, CSV and TSV as a table, XML, Markdown and other text as text,
// images as pictures and everything else as a hex dump.

const (
	previewIdle = iota
	previewLoading
	previewReady
	previewFailed
	previewNone
)

const (
	noneEmpty = iota + 1
	noneLarge
)

const (
	previewLimit    = 1 << 20 // bytes fetched for text
	previewHexLen   = 4 << 10 // bytes a hex dump shows
	maxImagePreview = 8 << 20
	previewCSVRows  = 200 // rows of a CSV table, header included
	previewCSVCols  = 40
)

type previewState struct {
	key   string
	state int
	kind  string // "json" | "csv" | "text" | "hex" | "image"
	text  string // what the editor shows
	note  string // the muted line under the content
	ed    *editor.State
	rows  [][]string // csv
	colW  []float32  // csv column widths
	img   *ui.Bitmap
	w, h  int
	msg   string
	none  int // previewNone: why there is nothing to show (noneEmpty, noneLarge)
	limit int64
	bad   bool // previewFailed: the image does not decode (msg is a server answer otherwise)
	gen   int

	cancel context.CancelFunc
}

func (a *app) resetPreview() {
	if a.prev.cancel != nil {
		a.prev.cancel()
	}
	a.prev = previewState{gen: a.prev.gen + 1}
}

var textExts = map[string]bool{
	".txt": true, ".md": true, ".markdown": true, ".log": true, ".jsonl": true, ".ndjson": true, ".yaml": true, ".yml": true,
	".xml": true, ".html": true, ".htm": true, ".css": true, ".js": true, ".ts": true,
	".go": true, ".py": true, ".sh": true, ".sql": true, ".ini": true, ".toml": true, ".conf": true, ".cfg": true, ".svg": true,
}

// previewKind says how a file can be previewed: "image", "json", "csv",
// "text", or "" when only a hex dump fits.
func previewKind(key, contentType string) string {
	ext := strings.ToLower(path.Ext(key))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return "image"
	case ".json":
		return "json"
	case ".csv", ".tsv":
		return "csv"
	}
	if textExts[ext] {
		return "text"
	}
	ct := strings.ToLower(contentType)
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	ct = strings.TrimSpace(ct)
	switch {
	case ct == "image/png", ct == "image/jpeg", ct == "image/gif", ct == "image/webp":
		return "image"
	case ct == "application/json", ct == "text/json", strings.HasSuffix(ct, "+json"):
		return "json"
	case ct == "text/csv", ct == "text/tab-separated-values", ct == "application/csv":
		return "csv"
	case strings.HasPrefix(ct, "text/"), ct == "application/xml", ct == "application/x-ndjson",
		ct == "application/yaml", ct == "application/x-yaml", ct == "application/javascript", ct == "image/svg+xml",
		strings.HasSuffix(ct, "+xml"):
		return "text"
	}
	return ""
}

// sniffable reports whether a file of no known kind may still be text: one
// with no extension and no type to say otherwise ("Makefile", "LICENSE"). Its
// first 4 KiB decide between text and a hex dump; every other unknown file is a
// hex dump.
func sniffable(key, contentType string) bool {
	if path.Ext(key) != "" {
		return false
	}
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	return ct == "" || ct == "application/octet-stream" || ct == "binary/octet-stream"
}

// isTSV reports whether a CSV-kind file is tab separated.
func isTSV(key, contentType string) bool {
	return strings.EqualFold(path.Ext(key), ".tsv") || strings.Contains(strings.ToLower(contentType), "tab-separated")
}

// previewLanguage names the editor language of a text preview ("" for none).
func previewLanguage(kind, key, contentType string) string {
	if kind == "json" {
		return "json"
	}
	ext := strings.ToLower(path.Ext(key))
	ct := strings.ToLower(contentType)
	switch {
	case ext == ".xml" || ext == ".svg" || strings.Contains(ct, "xml"):
		return "xml"
	case ext == ".md" || ext == ".markdown" || strings.Contains(ct, "markdown"):
		return "markdown"
	}
	return ""
}

// ensurePreview loads the preview of the selected object once its tab is open.
func (a *app) ensurePreview() {
	if a.tool != "" || !a.inspectorOpen || a.inspTab != tabPreview || a.headKey == "" || a.headState == 1 {
		return
	}
	if a.prev.key == a.headKey {
		return
	}
	key := a.headKey
	i, ok := a.byKey[key]
	if !ok {
		return
	}
	size := a.rows[i].Size
	ct := ""
	if a.headState == 2 {
		ct = a.head.ContentType
	}
	kind := previewKind(key, ct)
	if kind == "" && sniffable(key, ct) {
		kind = "sniff"
	}
	a.prev.key = key
	limit := int64(previewLimit)
	switch kind {
	case "image":
		limit = maxImagePreview
	case "", "sniff":
		limit = previewHexLen
	}
	switch {
	case size == 0:
		a.prev.state, a.prev.none = previewNone, noneEmpty
		return
	case kind == "image" && size > limit:
		a.prev.state, a.prev.none, a.prev.limit = previewNone, noneLarge, limit
		return
	}
	a.prev.state, a.prev.kind = previewLoading, kind
	a.markMemory("preview-start")
	gen, loc := a.prev.gen, a.scope
	tsv := isTSV(key, ct)
	ctx, cancel := a.timeout(60 * time.Second)
	a.prev.cancel = cancel
	go func() {
		defer cancel()
		data, truncated, err := a.svc.Preview(ctx, loc, key, "", limit)
		a.post(func() {
			if gen != a.prev.gen || a.prev.key != key {
				return
			}
			defer a.markMemory("preview-finished")
			if err != nil {
				a.prev.state, a.prev.msg = previewFailed, humanError(err)
				return
			}
			if kind == "image" {
				bm, err := ui.DecodeBitmap(data)
				if err != nil {
					a.prev.state, a.prev.bad = previewFailed, true
					return
				}
				a.prev.img = bm
				a.prev.w, a.prev.h = bm.Size()
				a.prev.state = previewReady
				return
			}
			b := buildPreview(kind, tsv, data, truncated, limit)
			a.prev.kind, a.prev.text, a.prev.note, a.prev.rows = b.kind, b.text, b.note, b.rows
			a.prev.colW = csvWidths(b.rows)
			if b.kind != "csv" {
				lang := ""
				if b.kind != "hex" {
					lang = previewLanguage(b.kind, key, ct)
				}
				a.prev.ed = newPreviewEditor(b.text, lang, locales.PreviewLabelOf(baseName(key)))
			}
			a.prev.state = previewReady
		})
	}()
}

// previewBody is a fetched object ready to show.
type previewBody struct {
	kind string
	text string
	note string
	rows [][]string
}

// buildPreview decides how data is shown: kind is what the name and type say
// ("" for unknown), truncated that the object goes on past data, limit how many
// bytes were asked for.
func buildPreview(kind string, tsv bool, data []byte, truncated bool, limit int64) previewBody {
	if kind == "" || !looksText(data) {
		b := previewBody{kind: "hex"}
		n := min(len(data), previewHexLen)
		b.text = hexDump(data[:n])
		if truncated || len(data) > previewHexLen {
			b.note = locales.PreviewFirst4k()
		}
		return b
	}
	cut := ""
	if truncated {
		cut = locales.PreviewLimited(humanBytes(limit))
	}
	text := strings.ToValidUTF8(string(data), "�")
	switch kind {
	case "json":
		if !truncated && json.Valid(data) {
			var out bytes.Buffer
			if json.Indent(&out, data, "", "  ") == nil {
				text = out.String()
			}
		}
	case "csv":
		if rows, more := parseCSV(text, tsv, truncated); len(rows) > 0 {
			note := cut
			if more {
				if note != "" {
					note += " · "
				}
				note += locales.PreviewFirstRows(previewCSVRows)
			}
			return previewBody{kind: "csv", rows: rows, note: note}
		}
		kind = "text"
	case "sniff":
		kind = "text"
	}
	return previewBody{kind: kind, text: text, note: cut}
}

// looksText reports whether data reads as text: valid UTF-8 (a character cut
// by the end of the range does not count against it) with no NUL and hardly any
// control characters.
func looksText(data []byte) bool {
	if bytes.IndexByte(data, 0) >= 0 {
		return false
	}
	ok := false
	for cut := 0; cut <= utf8.UTFMax-1 && cut <= len(data); cut++ {
		if utf8.Valid(data[:len(data)-cut]) {
			ok = true
			break
		}
	}
	if !ok {
		return false
	}
	ctl := 0
	for _, b := range data {
		if b < 0x20 && b != '\t' && b != '\n' && b != '\r' && b != '\f' || b == 0x7f {
			ctl++
		}
	}
	return ctl*50 <= len(data)
}

// parseCSV reads the first rows of a CSV or TSV text. more says the file has
// further rows than the table shows (the cut-off of a long object is the
// caller's note); a cut-off text drops its last row, which may be partial.
func parseCSV(text string, tsv, truncated bool) (rows [][]string, more bool) {
	r := csv.NewReader(strings.NewReader(text))
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	if tsv {
		r.Comma = '\t'
	}
	for len(rows) <= previewCSVRows {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			if len(rows) == 0 {
				return nil, false
			}
			more = true
			break
		}
		rows = append(rows, rec)
	}
	switch {
	case len(rows) > previewCSVRows:
		rows, more = rows[:previewCSVRows], true
	case truncated && len(rows) > 1:
		rows = rows[:len(rows)-1]
	}
	return rows, more
}

// csvWidths sizes the columns of a table from its cells: wide enough for the
// longest of the first rows, within 48 and 220.
func csvWidths(rows [][]string) []float32 {
	cols := 0
	for _, r := range rows {
		cols = max(cols, len(r))
	}
	cols = min(cols, previewCSVCols)
	w := make([]float32, cols)
	for _, r := range rows {
		for j := 0; j < cols && j < len(r); j++ {
			n := min(utf8.RuneCountInString(r[j]), 32)
			w[j] = max(w[j], float32(n)*6.8+24)
		}
	}
	for j := range w {
		w[j] = max(48, min(w[j], 220))
	}
	return w
}

// hexDump writes data as lines of an offset, 8 bytes in hex and their ASCII;
// 8 columns keep a line inside the inspector's width.
func hexDump(data []byte) string {
	const n = 8
	var sb strings.Builder
	for off := 0; off < len(data); off += n {
		line := data[off:min(off+n, len(data))]
		fmt.Fprintf(&sb, "%04x  ", off) // previews stop at 4 KiB
		for i := 0; i < n; i++ {
			if i < len(line) {
				fmt.Fprintf(&sb, "%02x ", line[i])
			} else {
				sb.WriteString("   ")
			}
		}
		sb.WriteByte(' ')
		for _, b := range line {
			if b >= 0x20 && b < 0x7f {
				sb.WriteByte(b)
			} else {
				sb.WriteByte('.')
			}
		}
		if off+n < len(data) {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// newPreviewEditor is a read-only buffer of text in mygo's code editor, with
// the language's highlighting when it has one.
func newPreviewEditor(text, lang, label string) *editor.State {
	buf := editor.NewBuffer(text)
	if l := editor.LanguageNamed(lang); l != nil {
		buf.SetLanguage(l)
	}
	return &editor.State{
		Buffer: buf, ReadOnly: true, NoFolding: true, NoActiveLine: true, NoIndentGuides: true, NoScrollPastEnd: true,
		LineNumbers: editor.LineNumbersOff, Label: label, Font: ui.Font{Family: "monospace", Size: sizeSecondary},
	}
}

// previewTab shows the object: a picture scaled to the panel, a table, or text
// in the read-only editor; an empty state says why there is nothing to show.
func (a *app) previewTab(c *ui.Context, e Entry) {
	k := tk(c)
	p := &a.prev
	switch p.state {
	case previewLoading, previewIdle:
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c).Size(14, 14)
			mutedText(c, locales.PreviewLoading())
		})
	case previewReady:
		switch p.kind {
		case "image":
			ui.Image(c, p.img).FillWidth().AspectRatio(float32(p.w) / float32(max(p.h, 1))).Fit(ui.Contain).Radius(6).Label(locales.PreviewLabelOf(e.Name))
			subtleText(c, fmt.Sprintf("%d × %d", p.w, p.h))
		case "csv":
			ui.Column(c).Gap(6).Children(func() {
				a.previewTable(c)
				if p.note != "" {
					mutedText(c, p.note)
				}
			})
		default:
			ui.Column(c).Gap(6).Children(func() {
				p.ed.Theme = codeTheme(c)
				box := ui.Column(c.Key("previewbox")).Height(360).Radius(6).Background(k.Sidebar).Clip()
				if p.ed.Focused() {
					box.Border(1, k.Focus)
				} else {
					box.Border(1, k.Border)
				}
				box.Children(func() { editor.View(c, p.ed).Grow(1).FillWidth() })
				if p.note != "" {
					mutedText(c, p.note)
				}
			})
		}
	case previewFailed:
		msg := p.msg
		if p.bad {
			msg = locales.PreviewImageFailed()
		}
		emptyBlock(c, "circle-alert", locales.PreviewFailedTitle(), msg, 200)
	default:
		msg := locales.PreviewEmpty()
		if p.none == noneLarge {
			msg = locales.PreviewTooLarge(humanBytes(p.limit))
		}
		emptyBlock(c, "eye", msg, "", 200)
	}
}

// previewTable is the CSV table: the header row in weight 600 over a hairline,
// 26px rows, scrolling both ways.
func (a *app) previewTable(c *ui.Context) {
	k := tk(c)
	p := &a.prev
	h := min(360, float32(len(p.rows))*26+12)
	ui.ScrollBoth(c.Key("csvscroll")).Height(h).Radius(6).Background(k.Sidebar).Border(1, k.Border).Children(func() {
		ui.Column(c).Padding(6, 0).Children(func() {
			for i, row := range p.rows {
				r := ui.Row(c.Key(i)).Height(26).Padding(0, 10).AlignItems(ui.Center).Shrink(0)
				if i == 0 {
					r.BorderWidth(0, 0, 1, 0).BorderColor(k.Border)
				}
				r.Children(func() {
					for j, w := range p.colW {
						cell := ""
						if j < len(row) {
							cell = row[j]
						}
						weight := 400
						if i == 0 {
							weight = 600
						}
						ui.Text(c.Key(j), cell).FontSize(sizeSecondary).FontWeight(weight).TextColor(k.Text).SingleLine().Ellipsis("…").Width(w).Shrink(0)
					}
				})
			}
		})
	})
}
