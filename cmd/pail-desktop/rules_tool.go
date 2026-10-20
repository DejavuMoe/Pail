package main

import (
	"encoding/json"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/icons"
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	"github.com/DejavuMoe/Pail/internal/rules"
	"github.com/egoist/mygo/ui"
)

// Upload rules tool: an ordered list of rules on the left, the editor of the
// chosen rule on the right, and a strip that evaluates the draft list against
// a bucket and key. Edits stay in a draft until Save.

type ruleKV struct{ k, v string }

// ruleForm holds the text of the chosen rule's fields. It is turned back into
// a rule after every frame, so that the draft list is always current.
type ruleForm struct {
	name, bucket, key string
	headers, meta     []ruleKV
	class             string
	enc, kms, keyRef  string // enc: "", AES256, aws:kms, SSE-C
	comp, level       string // comp: "", gzip, zip, zipaes
	pwRef             string
}

type rulesState struct {
	saved, draft []rules.UploadRule
	sel          int
	form         ruleForm
	saving       bool
	err          string
	testBucket   string
	testKey      string
}

var headerNames = []string{"Cache-Control", "Content-Disposition", "Content-Encoding", "Content-Language", "Content-Type", "Expires"}

var headerChoices = func() []choice {
	var out []choice
	for _, h := range headerNames {
		out = append(out, choice{h, h})
	}
	return out
}()

func storageClassChoices() []choice {
	return append([]choice{{"", locales.RulesDefault()}}, classChoices(s3Classes...)...)
}

func encChoices() []choice {
	return []choice{{"", locales.CommonNone()}, {"AES256", "AES256"}, {"aws:kms", "aws:kms"}, {"SSE-C", "SSE-C"}}
}

func compChoices() []choice {
	return []choice{{"", locales.CommonNone()}, {"gzip", "GZIP"}, {"zip", "ZIP"}, {"zipaes", "AES-256 ZIP"}}
}

func levelChoices() []choice {
	out := []choice{{"-1", locales.RulesDefault()}, {"-2", locales.RulesLevelHuffman()}, {"0", locales.RulesLevelStore()}}
	for i := 1; i <= 9; i++ {
		out = append(out, choice{strconv.Itoa(i), strconv.Itoa(i)})
	}
	return out
}

// ---- model ------------------------------------------------------------------------------------

func cloneRuleList(in []rules.UploadRule) []rules.UploadRule {
	b, _ := json.Marshal(in)
	var out []rules.UploadRule
	_ = json.Unmarshal(b, &out)
	return out
}

func rulesJSON(in []rules.UploadRule) string {
	b, _ := json.Marshal(in)
	if string(b) == "null" {
		return "[]"
	}
	return string(b)
}

func formFromRule(r rules.UploadRule) ruleForm {
	f := ruleForm{name: r.Name, bucket: r.BucketPattern, key: r.KeyPattern, class: r.StorageClass, level: "-1"}
	keys := make([]string, 0, len(r.Headers))
	for k := range r.Headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		f.headers = append(f.headers, ruleKV{k, r.Headers[k]})
	}
	keys = keys[:0]
	for k := range r.Metadata {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		f.meta = append(f.meta, ruleKV{k, r.Metadata[k]})
	}
	if s := r.SSE; s != nil {
		f.enc, f.kms, f.keyRef = s.Type, s.KMSKeyID, s.CustomerKeyRef
	}
	if z := r.Compression; z != nil {
		f.comp, f.level, f.pwRef = z.Method, strconv.Itoa(z.Level), z.PasswordRef
		if z.Method == "zip" && z.PasswordRef != "" {
			f.comp = "zipaes"
		}
	}
	return f
}

func (f *ruleForm) rule(disabled bool) rules.UploadRule {
	r := rules.UploadRule{Name: f.name, BucketPattern: f.bucket, KeyPattern: f.key, Disabled: disabled, StorageClass: f.class}
	for _, h := range f.headers {
		if h.k = strings.TrimSpace(h.k); h.k != "" {
			if r.Headers == nil {
				r.Headers = map[string]string{}
			}
			r.Headers[http.CanonicalHeaderKey(h.k)] = h.v
		}
	}
	for _, m := range f.meta {
		if m.k = strings.TrimSpace(m.k); m.k != "" {
			if r.Metadata == nil {
				r.Metadata = map[string]string{}
			}
			r.Metadata[m.k] = m.v
		}
	}
	switch f.enc {
	case "AES256":
		r.SSE = &rules.Encryption{Type: "AES256"}
	case "aws:kms":
		r.SSE = &rules.Encryption{Type: "aws:kms", KMSKeyID: strings.TrimSpace(f.kms)}
	case "SSE-C":
		r.SSE = &rules.Encryption{Type: "SSE-C", CustomerKeyRef: f.keyRef}
	}
	if f.comp != "" {
		lv, err := strconv.Atoi(f.level)
		if err != nil {
			lv = -1
		}
		z := &rules.Compression{Method: f.comp, Level: lv}
		switch f.comp {
		case "zipaes":
			z.Method, z.PasswordRef = "zip", f.pwRef
		case "gzip":
		}
		r.Compression = z
	}
	return r
}

// ruleSummary is the one-line digest in the list: "Headers · GZIP 6 · STANDARD_IA".
func ruleSummary(r rules.UploadRule) string {
	var p []string
	if len(r.Headers) > 0 || len(r.Metadata) > 0 {
		p = append(p, locales.RulesSummaryHeaders())
	}
	if z := r.Compression; z != nil {
		name := strings.ToUpper(z.Method)
		if z.Method == "zip" && z.PasswordRef != "" {
			name = "ZIP AES-256"
		}
		if z.Level >= 0 {
			name += " " + strconv.Itoa(z.Level)
		}
		p = append(p, name)
	}
	if r.StorageClass != "" {
		p = append(p, r.StorageClass)
	}
	if r.SSE != nil {
		p = append(p, "SSE "+r.SSE.Type)
	}
	if len(p) == 0 {
		return locales.RulesSummaryNone()
	}
	return strings.Join(p, " · ")
}

func maskText(r rules.UploadRule) string {
	b, k := r.BucketPattern, r.KeyPattern
	if b == "" {
		b = "*"
	}
	if k == "" {
		k = "*"
	}
	return b + " / " + k
}

// optionsSummary words the effective options of an evaluation.
func optionsSummary(o rules.UploadOptions) string {
	var p []string
	hk := make([]string, 0, len(o.Headers))
	for k := range o.Headers {
		hk = append(hk, k)
	}
	sort.Strings(hk)
	for _, k := range hk {
		p = append(p, k+": "+o.Headers[k])
	}
	mk := make([]string, 0, len(o.Metadata))
	for k := range o.Metadata {
		mk = append(mk, k)
	}
	sort.Strings(mk)
	for _, k := range mk {
		p = append(p, locales.RulesResultMeta(k, o.Metadata[k]))
	}
	if o.StorageClass != "" {
		p = append(p, o.StorageClass)
	}
	if s := o.SSE; s != nil {
		switch s.Type {
		case "aws:kms":
			p = append(p, "SSE aws:kms "+s.KMSKeyID)
		case "SSE-C":
			p = append(p, "SSE-C "+s.CustomerKeyRef)
		default:
			p = append(p, "SSE "+s.Type)
		}
	}
	if z := o.Compression; z != nil {
		name := strings.ToUpper(z.Method)
		if z.Method == "zip" && z.PasswordRef != "" {
			name = "ZIP AES-256"
		}
		if z.Level >= 0 {
			name = locales.RulesResultLevel(name, strconv.Itoa(z.Level))
		}
		p = append(p, name)
	}
	if len(p) == 0 {
		return locales.RulesResultNone()
	}
	s := strings.Join(p, " · ")
	if len(o.MatchedRules) > 0 {
		s += "  (" + strings.Join(o.MatchedRules, ", ") + ")"
	}
	return s
}

// draftResult evaluates the draft list for bucket and key.
func (rs *rulesState) draftResult() (string, bool) {
	o, err := rules.Evaluate(rs.draft, rs.testBucket, rs.testKey)
	if err != nil {
		return err.Error(), false
	}
	return optionsSummary(o), true
}

func (rs *rulesState) dirty() bool { return rulesJSON(rs.draft) != rulesJSON(rs.saved) }

// problem is the first thing wrong with the draft, "" when it is sound.
func (rs *rulesState) problem() string {
	if err := rules.Validate(rs.draft); err != nil {
		return err.Error()
	}
	for i, r := range rs.draft {
		if v, ok := r.Headers["Expires"]; ok {
			if _, err := http.ParseTime(v); err != nil {
				return locales.RulesProblemExpires(strconv.Itoa(i), r.Name)
			}
		}
	}
	if rs.sel >= 0 && rs.sel < len(rs.draft) && rs.form.comp == "zipaes" && rs.form.pwRef == "" {
		return locales.RulesProblemPassword()
	}
	return ""
}

// ---- actions ---------------------------------------------------------------------------------

func (a *app) rulesEnter() {
	rs := &a.tl.rules
	*rs = rulesState{saved: a.svc.UploadRules()}
	rs.draft = cloneRuleList(rs.saved)
	rs.sel = -1
	if len(rs.draft) > 0 {
		a.rulesSelect(0)
	}
}

func (a *app) rulesSelect(i int) {
	rs := &a.tl.rules
	a.rulesCommit()
	rs.sel = i
	if i >= 0 && i < len(rs.draft) {
		rs.form = formFromRule(rs.draft[i])
	}
}

// rulesCommit copies the form into the chosen rule of the draft.
func (a *app) rulesCommit() {
	rs := &a.tl.rules
	if rs.sel >= 0 && rs.sel < len(rs.draft) {
		rs.draft[rs.sel] = rs.form.rule(rs.draft[rs.sel].Disabled)
	}
}

func (a *app) rulesAdd() {
	rs := &a.tl.rules
	a.rulesCommit()
	rs.draft = append(rs.draft, rules.UploadRule{Name: locales.RulesNewRule()})
	a.rulesSelect(len(rs.draft) - 1)
}

func (a *app) rulesDuplicate() {
	rs := &a.tl.rules
	a.rulesCommit()
	if rs.sel < 0 {
		return
	}
	cp := cloneRuleList([]rules.UploadRule{rs.draft[rs.sel]})[0]
	cp.Name = locales.RulesCopyName(cp.Name)
	rs.draft = slices.Insert(rs.draft, rs.sel+1, cp)
	a.rulesSelect(rs.sel + 1)
}

func (a *app) rulesDelete() {
	rs := &a.tl.rules
	if rs.sel < 0 || rs.sel >= len(rs.draft) {
		return
	}
	rs.draft = slices.Delete(rs.draft, rs.sel, rs.sel+1)
	i := min(rs.sel, len(rs.draft)-1)
	rs.sel = -2 // force the form to reload
	a.rulesSelect(i)
}

func (a *app) rulesMove(d int) {
	rs := &a.tl.rules
	a.rulesCommit()
	j := rs.sel + d
	if rs.sel < 0 || j < 0 || j >= len(rs.draft) {
		return
	}
	rs.draft[rs.sel], rs.draft[j] = rs.draft[j], rs.draft[rs.sel]
	rs.sel = j
}

func (a *app) rulesRevert() {
	rs := &a.tl.rules
	rs.draft = cloneRuleList(rs.saved)
	rs.err = ""
	i := min(rs.sel, len(rs.draft)-1)
	rs.sel = -2
	a.rulesSelect(i)
}

func (a *app) rulesSave() {
	rs := &a.tl.rules
	if rs.saving {
		return
	}
	a.rulesCommit()
	list := cloneRuleList(rs.draft)
	rs.saving, rs.err = true, ""
	a.bg(func() func() {
		err := a.svc.SaveUploadRules(list)
		return func() {
			rs.saving = false
			if err != nil {
				rs.err = humanError(err)
				return
			}
			rs.saved = a.svc.UploadRules()
			a.toast(locales.RulesSaved())
		}
	})
}

// ---- view -------------------------------------------------------------------------------------

func (a *app) rulesView(c *ui.Context) {
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
			a.rulesList(c)
			a.ruleEditor(c)
		})
		a.rulesTestStrip(c)
	})
}

func (a *app) rulesList(c *ui.Context) {
	k := tk(c)
	rs := &a.tl.rules
	toolPane(c, "rules-pane", 300, func() {
		paneBar(c, "rules-bar", func() {
			if ghostButton(c, icons.Get("plus"), "", locales.RulesAddRule(), false).Label(locales.RulesAddRule()).Clicked() {
				a.rulesAdd()
			}
			has := rs.sel >= 0 && rs.sel < len(rs.draft)
			if ghostButton(c, icons.Get("copy"), "", locales.RulesDuplicateRule(), !has).Label(locales.RulesDuplicateRule()).Clicked() {
				a.rulesDuplicate()
			}
			if ghostButton(c, icons.Get("trash-2"), "", locales.RulesDeleteRule(), !has).Label(locales.RulesDeleteRule()).Clicked() {
				a.rulesDelete()
			}
			ui.Spacer(c)
			helpIcon(c, locales.RulesOrderHelp())
			if ghostButton(c, icons.Get("arrow-up"), "", locales.RulesMoveUp(), !has || rs.sel == 0).Label(locales.RulesMoveRuleUp()).Clicked() {
				a.rulesMove(-1)
			}
			if ghostButton(c, icons.Get("arrow-down"), "", locales.RulesMoveDown(), !has || rs.sel == len(rs.draft)-1).Label(locales.RulesMoveRuleDown()).Clicked() {
				a.rulesMove(1)
			}
		})
		if len(rs.draft) == 0 {
			emptyBlock(c, "list-filter", locales.RulesNoRules(), "", 0)
			return
		}
		ui.Scroll(c).Grow(1).MinHeight(0).ScrollbarInsets(0, 2, 0, 0).Children(func() {
			ui.Column(c).Gap(2).Padding(0, 0, 8, 0).Children(func() {
				for i := range rs.draft {
					r := rs.draft[i]
					row := listRow(c, "rule:"+strconv.Itoa(i), i == rs.sel)
					row.Children(func() {
						ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
							name := r.Name
							if strings.TrimSpace(name) == "" {
								name = locales.RulesUntitled()
							}
							col := k.Text
							if r.Disabled {
								col = k.TextMuted
							}
							ui.Text(c, name).FontSize(sizeBody).FontWeight(500).TextColor(col).SingleLine()
							mono(ui.Text(c, maskText(r)).FontSize(sizeSecondary).TextColor(k.TextMuted).SingleLine())
							ui.Text(c, ruleSummary(r)).FontSize(sizeSecondary).TextColor(k.TextMuted).SingleLine()
						})
						on := !r.Disabled
						if switchControl(c, &on, locales.RulesEnableRule(r.Name)).Changed() {
							rs.draft[i].Disabled = !on
						}
					})
					if row.Clicked() && i != rs.sel {
						a.rulesSelect(i)
					}
				}
			})
		})
	})
}

func (a *app) ruleEditor(c *ui.Context) {
	k := tk(c)
	rs := &a.tl.rules
	f := &rs.form
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
		if rs.sel < 0 || rs.sel >= len(rs.draft) {
			emptyBlock(c, "list-filter", locales.RulesNoneSelected(), "", 0)
		} else {
			ui.Scroll(c).Grow(1).MinHeight(0).ScrollbarInsets(0, 2, 0, 0).Children(func() {
				ui.Column(c).MaxWidth(640).AlignSelf(ui.Start).Padding(20, 24, 24, 24).Gap(16).Children(func() {
					a.ruleFields(c, f)
				})
			})
			a.rulesCommit()
		}
		footerBar(c, "rules-foot", func() {
			if secondaryButton(c, locales.RulesManageSecrets(), false).Clicked() {
				a.openSecrets()
			}
			ui.Spacer(c)
			msg := rs.err
			if msg == "" {
				msg = rs.problem()
			}
			if msg != "" {
				ui.Row(c).Shrink(1).MinWidth(0).MaxWidth(420).Children(func() {
					resultLine(c, "circle-alert", k.Danger, msg)
				})
			}
			dirty := rs.dirty()
			if dirty && ghostButton(c, nil, locales.CommonRevert(), locales.RulesDiscardTip(), rs.saving).Clicked() {
				a.rulesRevert()
			}
			if primaryButton(c, locales.CommonSave(), !dirty || rs.saving).Clicked() {
				a.rulesSave()
			}
		})
	})
}

// ruleSection is formSection with an optional "?" after the title.
func ruleSection(c *ui.Context, title, help string) {
	ui.Column(c.Key("fsec:"+title)).Gap(6).Margin(4, 0, 0, 0).Children(func() {
		labelHelp(c, help, func() ui.Element { return sectionLabel(c, title) })
		hairline(c)
	})
}

func (a *app) ruleFields(c *ui.Context, f *ruleForm) {
	formField(c, locales.CommonName(), "", func() { inputBox(c, &f.name, locales.CommonName(), "", inputOpts{}) })
	ui.Row(c).Gap(12).Children(func() {
		cell(c, "bm", func() {
			formFieldHelp(c, locales.RulesBucketMask(), locales.RulesBucketMaskHelp(), "", func() {
				inputBox(c, &f.bucket, locales.RulesBucketMask(), locales.RulesBucketMaskPh(), inputOpts{mono: true})
			})
		})
		cell(c, "km", func() {
			formFieldHelp(c, locales.RulesKeyMask(), locales.RulesKeyMaskHelp(), "", func() {
				inputBox(c, &f.key, locales.RulesKeyMask(), locales.RulesKeyMaskPh(), inputOpts{mono: true})
			})
		})
	})

	ruleSection(c, locales.RulesSectionHeaders(), locales.RulesHeadersHelp())
	del := -1
	for i := range f.headers {
		h := &f.headers[i]
		ui.Row(c.Key("hrow:" + strconv.Itoa(i))).Gap(8).AlignItems(ui.Center).Children(func() {
			selectBox(c, locales.RulesHeaderName(strconv.Itoa(i+1)), &h.k, headerChoices, 200)
			ui.Box(c).Grow(1).MinWidth(0).Children(func() {
				inputBox(c, &h.v, locales.RulesHeaderValue(strconv.Itoa(i+1)), locales.CommonValue(), inputOpts{mono: true})
			})
			if iconButton(c, icons.Get("x"), locales.RulesRemoveHeader()).Clicked() {
				del = i
			}
		})
	}
	if del >= 0 {
		f.headers = slices.Delete(f.headers, del, del+1)
	}
	ui.Row(c.Key("hadd")).Children(func() {
		if ghostButton(c, icons.Get("plus"), locales.RulesAddHeader(), locales.RulesAddHeaderTip(), len(f.headers) >= len(headerNames)).Clicked() {
			f.headers = append(f.headers, ruleKV{k: nextHeader(f.headers)})
		}
	})

	ruleSection(c, locales.RulesSectionMetadata(), locales.RulesMetadataHelp())
	del = -1
	for i := range f.meta {
		m := &f.meta[i]
		ui.Row(c.Key("mrow:" + strconv.Itoa(i))).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Width(200).Shrink(0).Children(func() {
				inputBox(c, &m.k, locales.RulesMetadataKey(strconv.Itoa(i+1)), locales.CommonKey(), inputOpts{mono: true})
			})
			ui.Box(c).Grow(1).MinWidth(0).Children(func() {
				inputBox(c, &m.v, locales.RulesMetadataValue(strconv.Itoa(i+1)), locales.CommonValue(), inputOpts{mono: true})
			})
			if iconButton(c, icons.Get("x"), locales.RulesRemoveMetadata()).Clicked() {
				del = i
			}
		})
	}
	if del >= 0 {
		f.meta = slices.Delete(f.meta, del, del+1)
	}
	ui.Row(c.Key("madd")).Children(func() {
		if ghostButton(c, icons.Get("plus"), locales.RulesAddMetadata(), locales.RulesAddMetadataTip(), false).Clicked() {
			f.meta = append(f.meta, ruleKV{})
		}
	})

	ruleSection(c, locales.RulesSectionStorage(), "")
	classes := storageClassChoices()
	if f.class != "" && !slices.ContainsFunc(classes, func(o choice) bool { return o.Value == f.class }) {
		classes = append(slices.Clone(classes), choice{f.class, f.class})
	}
	formFieldHelp(c, locales.RulesStorageClass(), locales.RulesStorageClassHelp(), "", func() {
		selectNone(c, locales.RulesStorageClass(), &f.class, classes, 240)
	})

	ruleSection(c, locales.RulesSectionEncryption(), "")
	ui.Row(c).Gap(12).Children(func() {
		cell(c, "enc", func() {
			formFieldHelp(c, locales.RulesEncryption(), locales.RulesEncryptionHelp(), "", func() {
				selectNone(c, locales.RulesEncryption(), &f.enc, encChoices(), 0)
			})
		})
		cell(c, "enc2", func() {
			switch f.enc {
			case "aws:kms":
				formFieldHelp(c, locales.RulesKmsKeyId(), locales.RulesKmsKeyIdHelp(), "", func() {
					inputBox(c, &f.kms, locales.RulesKmsKeyId(), locales.RulesKmsKeyIdPh(), inputOpts{mono: true})
				})
			case "SSE-C":
				formFieldHelp(c, locales.RulesKeyRef(), locales.RulesKeyRefHelp(), "", func() {
					selectNone(c, locales.RulesSseCKeyRef(), &f.keyRef, a.refChoices(locales.CommonChoose(), f.keyRef), 0)
				})
			}
		})
	})

	ruleSection(c, locales.RulesSectionCompression(), "")
	ui.Row(c).Gap(12).Children(func() {
		cell(c, "cmp", func() {
			formFieldHelp(c, locales.RulesCompression(), locales.RulesCompressionHelp(), "", func() {
				selectNone(c, locales.RulesCompression(), &f.comp, compChoices(), 0)
			})
		})
		cell(c, "cmp2", func() {
			if f.comp != "" {
				formFieldHelp(c, locales.RulesLevel(), locales.RulesLevelHelp(), "", func() {
					selectBox(c, locales.RulesCompressionLevel(), &f.level, levelChoices(), 0)
				})
			}
		})
		cell(c, "cmp3", func() {
			if f.comp == "zipaes" {
				formFieldHelp(c, locales.RulesPassword(), locales.RulesPasswordHelp(), "", func() {
					selectNone(c, locales.RulesZipPasswordRef(), &f.pwRef, a.refChoices(locales.CommonChoose(), f.pwRef), 0)
				})
			}
		})
	})
}

// nextHeader is the first standard header that has no row yet.
func nextHeader(rows []ruleKV) string {
	for _, n := range headerNames {
		if !slices.ContainsFunc(rows, func(r ruleKV) bool { return strings.EqualFold(r.k, n) }) {
			return n
		}
	}
	return headerNames[0]
}

func (a *app) rulesTestStrip(c *ui.Context) {
	k := tk(c)
	rs := &a.tl.rules
	ui.Row(c).Height(48).Padding(0, 16).Gap(12).AlignItems(ui.Center).Shrink(0).
		BorderWidth(1, 0, 0, 0).BorderColor(k.Border).Children(func() {
		labelHelp(c, locales.RulesTestHelp(), func() ui.Element {
			return ui.Text(c, locales.RulesTest()).FontSize(sizeSecondary).FontWeight(600).TextColor(k.Text)
		})
		inputBox(c, &rs.testBucket, locales.RulesTestBucket(), locales.CommonBucket(), inputOpts{width: 180, mono: true})
		inputBox(c, &rs.testKey, locales.RulesTestKey(), locales.RulesObjectKey(), inputOpts{width: 280, mono: true})
		if rs.testBucket == "" && rs.testKey == "" {
			return
		}
		res, ok := rs.draftResult()
		col := k.Text
		if !ok {
			col = k.Danger
		}
		ui.Text(c, locales.RulesDraftResult()).FontSize(sizeSecondary).TextColor(k.TextMuted).SingleLine().Shrink(0)
		ui.Text(c, res).FontSize(sizeSecondary).TextColor(col).SingleLine().Grow(1).MinWidth(0).Tooltip(res)
	})
}
