package main

import (
	"encoding/json"
	"strings"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// Section forms: a few settings have a small form, the rest are edited as the
// JSON the service exchanges.
const (
	secVersioning = iota
	secEncryption
	secPublicAccess
	secOwnership
	secRequester
	secAccelerate
	secJSON
)

type bsSection struct {
	name, help func() string // the label and the "?" of the section, in the language shown
	cfg        core.BucketConfig
	form       int
	deletable  bool
}

// label is the section name in the language the app shows.
func (s bsSection) label() string { return s.name() }

var bsSections = []bsSection{
	{locales.BucketSettingsVersioningName, locales.BucketSettingsVersioningHelp, core.ConfigVersioning, secVersioning, false},
	{locales.BucketSettingsEncryptionName, locales.BucketSettingsEncryptionHelp, core.ConfigEncryption, secEncryption, true},
	{locales.BucketSettingsPublicAccessName, locales.BucketSettingsPublicAccessHelp, core.ConfigPublicAccessBlock, secPublicAccess, true},
	{locales.BucketSettingsOwnershipName, locales.BucketSettingsOwnershipHelp, core.ConfigOwnership, secOwnership, true},
	{locales.BucketSettingsPolicyName, locales.BucketSettingsPolicyHelp, core.ConfigPolicy, secJSON, true},
	{locales.BucketSettingsCorsName, locales.BucketSettingsCorsHelp, core.ConfigCORS, secJSON, true},
	{locales.BucketSettingsLifecycleName, locales.BucketSettingsLifecycleHelp, core.ConfigLifecycle, secJSON, true},
	{locales.BucketSettingsLoggingName, locales.BucketSettingsLoggingHelp, core.ConfigLogging, secJSON, false},
	{locales.BucketSettingsTagsName, locales.BucketSettingsTagsHelp, core.ConfigTagging, secJSON, true},
	{locales.BucketSettingsWebsiteName, locales.BucketSettingsWebsiteHelp, core.ConfigWebsite, secJSON, true},
	{locales.BucketSettingsObjectLockName, locales.BucketSettingsObjectLockHelp, core.ConfigObjectLock, secJSON, false},
	{locales.BucketSettingsReplicationName, locales.BucketSettingsReplicationHelp, core.ConfigReplication, secJSON, true},
	{locales.BucketSettingsRequesterName, locales.BucketSettingsRequesterHelp, core.ConfigRequestPayment, secRequester, false},
	{locales.BucketSettingsAccelerateName, locales.BucketSettingsAccelerateHelp, core.ConfigAccelerate, secAccelerate, false},
}

// bucketSettings is the settings dialog of one bucket.
type bucketSettings struct {
	open    bool
	bucket  string
	section int
	secs    map[core.BucketConfig]*cfgState
	gen     int

	// navScroll and paneScroll report whether the nav list and the form
	// overflowed in the last frame, which decides whether the dividers show.
	navScroll, paneScroll ui.ScrollState
}

// cfgState is one section: what the service holds and what the form shows.
type cfgState struct {
	state  int // 0 idle, 1 loading, 2 ready, 3 failed
	server string
	draft  string
	err    string
	unsupp bool

	configuring bool // "Configure" was pressed on a section with nothing set
	msg         string
	msgOK       bool
	saving      bool

	// small forms
	verStatus int // 0 Enabled, 1 Suspended
	sse       string
	kms       string
	pab       [4]bool
	owner     string
	requester bool
	accel     bool
	loaded    cfgForm // the form values as loaded, for the dirty check

	// JSON editor: the buffer behind draft, and the text and version last synced.
	ed     *editor.State
	edText string
	edVer  uint64
}

type cfgForm struct {
	verStatus int
	sse, kms  string
	pab       [4]bool
	owner     string
	requester bool
	accel     bool
}

func (s *cfgState) formNow() cfgForm {
	return cfgForm{s.verStatus, s.sse, s.kms, s.pab, s.owner, s.requester, s.accel}
}

func (s *cfgState) formDirty() bool { return s.formNow() != s.loaded }

var pabFields = [4]string{"BlockPublicAcls", "IgnorePublicAcls", "BlockPublicPolicy", "RestrictPublicBuckets"}

// pabLabels and pabHelps are the switches of Public access, in the order of pabFields.
func pabLabels() [4]string {
	return [4]string{locales.BucketSettingsPabAcls(), locales.BucketSettingsPabIgnoreAcls(), locales.BucketSettingsPabPolicy(), locales.BucketSettingsPabRestrict()}
}

func pabHelps() [4]string {
	return [4]string{locales.BucketSettingsPabAclsHelp(), locales.BucketSettingsPabIgnoreAclsHelp(), locales.BucketSettingsPabPolicyHelp(), locales.BucketSettingsPabRestrictHelp()}
}

func sseChoices() []choice {
	return []choice{{"none", locales.CommonNone()}, {"AES256", "AES-256 (SSE-S3)"}, {"aws:kms", "AWS KMS"}}
}

func ownChoices() []choice {
	return []choice{
		{"BucketOwnerEnforced", locales.BucketSettingsOwnerEnforced()},
		{"BucketOwnerPreferred", locales.BucketSettingsOwnerPreferred()},
		{"ObjectWriter", locales.BucketSettingsOwnerWriter()},
	}
}

func (a *app) openBucketSettings(name string) {
	if name == "" {
		return
	}
	a.mg.bs = bucketSettings{open: true, bucket: name, secs: map[core.BucketConfig]*cfgState{}, gen: a.mg.bs.gen + 1}
	a.ensureVersioning(a.accountID, name)
}

func (bs *bucketSettings) cur() (bsSection, *cfgState) {
	sec := bsSections[bs.section]
	s := bs.secs[sec.cfg]
	if s == nil {
		s = &cfgState{}
		bs.secs[sec.cfg] = s
	}
	return sec, s
}

// template is what "Configure" puts in the editor of a section with nothing set.
func (bs *bucketSettings) template(cfg core.BucketConfig) string {
	switch cfg {
	case core.ConfigPolicy:
		return `{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "AllowRead",
      "Effect": "Allow",
      "Principal": "*",
      "Action": "s3:GetObject",
      "Resource": "arn:aws:s3:::` + bs.bucket + `/*"
    }
  ]
}`
	case core.ConfigCORS:
		return `{
  "CORSRules": [
    {
      "AllowedMethods": ["GET"],
      "AllowedOrigins": ["*"],
      "AllowedHeaders": ["*"],
      "MaxAgeSeconds": 3000
    }
  ]
}`
	case core.ConfigLifecycle:
		return `{
  "Rules": [
    {
      "ID": "expire-old-objects",
      "Status": "Enabled",
      "Filter": { "Prefix": "" },
      "Expiration": { "Days": 365 }
    }
  ]
}`
	case core.ConfigLogging:
		return `{
  "LoggingEnabled": {
    "TargetBucket": "my-log-bucket",
    "TargetPrefix": "logs/"
  }
}`
	case core.ConfigTagging:
		return `{
  "TagSet": [
    { "Key": "environment", "Value": "production" }
  ]
}`
	case core.ConfigWebsite:
		return `{
  "IndexDocument": { "Suffix": "index.html" },
  "ErrorDocument": { "Key": "error.html" }
}`
	case core.ConfigObjectLock:
		return `{
  "ObjectLockEnabled": "Enabled",
  "Rule": {
    "DefaultRetention": { "Mode": "GOVERNANCE", "Days": 30 }
  }
}`
	case core.ConfigReplication:
		return `{
  "Role": "arn:aws:iam::123456789012:role/replication-role",
  "Rules": [
    {
      "ID": "replicate-all",
      "Status": "Enabled",
      "Priority": 1,
      "Filter": { "Prefix": "" },
      "DeleteMarkerReplication": { "Status": "Disabled" },
      "Destination": { "Bucket": "arn:aws:s3:::my-destination-bucket" }
    }
  ]
}`
	}
	return "{\n  \n}"
}

// ---- loading -----------------------------------------------------------------------------

func (a *app) ensureCfg() {
	bs := &a.mg.bs
	sec, s := bs.cur()
	if s.state != 0 {
		return
	}
	s.state = 1
	gen, acct, bucket, cfg := bs.gen, a.accountID, bs.bucket, sec.cfg
	a.bg(func() func() {
		ctx, cancel := a.shortCtx()
		defer cancel()
		js, err := a.svc.GetBucketConfig(ctx, acct, bucket, cfg)
		return func() {
			bs := &a.mg.bs
			if bs.gen != gen || !bs.open {
				return
			}
			s := bs.secs[cfg]
			if s == nil {
				return
			}
			if err != nil {
				s.state, s.unsupp = 3, core.KindOf(err) == core.ErrUnsupported
				s.err = plainError(err, locales.BucketSettingsReadWhat())
				return
			}
			s.state, s.err, s.unsupp = 2, "", false
			s.server, s.draft, s.configuring = js, js, false
			s.parseForm(cfg, js)
		}
	})
}

// parseForm reads the values of the small forms out of the JSON the service
// returned.
func (s *cfgState) parseForm(cfg core.BucketConfig, js string) {
	switch cfg {
	case core.ConfigVersioning:
		s.verStatus = 0
		if parseVersioning(js) == "Suspended" {
			s.verStatus = 1
		}
	case core.ConfigEncryption:
		s.sse, s.kms = "none", ""
		var v struct {
			Rules []struct {
				ApplyServerSideEncryptionByDefault *struct {
					SSEAlgorithm   string
					KMSMasterKeyID *string
				}
			}
		}
		if json.Unmarshal([]byte(js), &v) == nil && len(v.Rules) > 0 && v.Rules[0].ApplyServerSideEncryptionByDefault != nil {
			d := v.Rules[0].ApplyServerSideEncryptionByDefault
			s.sse = d.SSEAlgorithm
			if d.KMSMasterKeyID != nil {
				s.kms = *d.KMSMasterKeyID
			}
		}
	case core.ConfigPublicAccessBlock:
		s.pab = [4]bool{}
		var v map[string]any
		if json.Unmarshal([]byte(js), &v) == nil {
			for i, f := range pabFields {
				if b, ok := v[f].(bool); ok {
					s.pab[i] = b
				}
			}
		}
	case core.ConfigOwnership:
		s.owner = "BucketOwnerEnforced"
		var v struct {
			Rules []struct{ ObjectOwnership string }
		}
		if json.Unmarshal([]byte(js), &v) == nil && len(v.Rules) > 0 && v.Rules[0].ObjectOwnership != "" {
			s.owner = v.Rules[0].ObjectOwnership
		}
	case core.ConfigRequestPayment:
		var v struct{ Payer string }
		_ = json.Unmarshal([]byte(js), &v)
		s.requester = v.Payer == "Requester"
	case core.ConfigAccelerate:
		var v struct{ Status string }
		_ = json.Unmarshal([]byte(js), &v)
		s.accel = v.Status == "Enabled"
	}
	s.loaded = s.formNow()
}

// ---- saving ------------------------------------------------------------------------------

// syntaxError words what is wrong with the JSON text, "" when it is fine.
func syntaxError(text string) string {
	var v any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		return locales.BucketSettingsInvalidJson(err.Error())
	}
	if _, ok := v.(map[string]any); !ok {
		return locales.BucketSettingsNotObject()
	}
	return ""
}

func (a *app) validateCfg(s *cfgState) bool {
	if msg := syntaxError(s.draft); msg != "" {
		s.msg, s.msgOK = msg, false
		return false
	}
	s.msg, s.msgOK = locales.BucketSettingsValidJson(), true
	return true
}

// formJSON builds the JSON a small form stands for.
func (s *cfgState) formJSON(cfg core.BucketConfig) string {
	var v any
	switch cfg {
	case core.ConfigVersioning:
		st := "Enabled"
		if s.verStatus == 1 {
			st = "Suspended"
		}
		v = map[string]any{"Status": st}
	case core.ConfigEncryption:
		d := map[string]any{"SSEAlgorithm": s.sse}
		if s.sse == "aws:kms" && strings.TrimSpace(s.kms) != "" {
			d["KMSMasterKeyID"] = strings.TrimSpace(s.kms)
		}
		v = map[string]any{"Rules": []any{map[string]any{"ApplyServerSideEncryptionByDefault": d}}}
	case core.ConfigPublicAccessBlock:
		m := map[string]any{}
		for i, f := range pabFields {
			m[f] = s.pab[i]
		}
		v = m
	case core.ConfigOwnership:
		v = map[string]any{"Rules": []any{map[string]any{"ObjectOwnership": s.owner}}}
	case core.ConfigRequestPayment:
		p := "BucketOwner"
		if s.requester {
			p = "Requester"
		}
		v = map[string]any{"Payer": p}
	case core.ConfigAccelerate:
		st := "Suspended"
		if s.accel {
			st = "Enabled"
		}
		v = map[string]any{"Status": st}
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

// saveCfg writes the section. js "" with remove removes the configuration.
func (a *app) saveCfg(sec bsSection, s *cfgState, js string, remove bool) {
	bs := &a.mg.bs
	if s.saving {
		return
	}
	s.saving, s.msg = true, ""
	gen, acct, bucket, cfg := bs.gen, a.accountID, bs.bucket, sec.cfg
	a.bg(func() func() {
		ctx, cancel := a.shortCtx()
		defer cancel()
		var err error
		if remove {
			err = a.svc.DeleteBucketConfig(ctx, acct, bucket, cfg)
		} else {
			err = a.svc.PutBucketConfig(ctx, acct, bucket, cfg, js)
		}
		return func() {
			bs := &a.mg.bs
			if cfg == core.ConfigVersioning {
				a.invalidateVersioning(acct, bucket)
			}
			if bs.gen != gen || !bs.open {
				return
			}
			s := bs.secs[cfg]
			if s == nil {
				return
			}
			s.saving = false
			if err != nil {
				s.msg, s.msgOK = plainError(err, locales.BucketSettingsChangeWhat()), false
				return
			}
			s.state = 0 // read it back
			s.msg, s.msgOK = locales.CommonSaved(), true
			if remove {
				s.msg = locales.BucketSettingsRemoved()
			}
		}
	})
}

func (a *app) askRemoveCfg(sec bsSection, s *cfgState) {
	bs := &a.mg.bs
	rows := append(a.scopeRows(Scope{Bucket: bs.bucket}), KV{locales.BucketSettingsSettingRow(), sec.label()})
	a.confirm = confirmState{
		open: true, title: locales.BucketSettingsRemoveTitle(), rows: rows, danger: true, action: locales.CommonRemove(),
		message: locales.BucketSettingsRemoveMessage(),
		run:     func() { a.saveCfg(sec, s, "", true) },
	}
}

// ---- view --------------------------------------------------------------------------------

// ghostDanger is a ghost button whose label is Danger-colored.
func ghostDanger(c *ui.Context, label, tip string, disabled bool) ui.Element {
	k := tk(c)
	b := ui.ButtonBase(c.Key("gd:"+label)).Label(label).Tooltip(tip).Radius(6).Height(28).Padding(0, 8).Center().Disabled(disabled)
	switch {
	case disabled:
		b.Opacity(0.4)
	case b.Pressed():
		b.Background(k.SurfacePressed)
	case b.Hovered():
		b.Background(k.SurfaceHover)
	}
	return b.Children(func() { ui.Text(c, label).FontSize(sizeBody).TextColor(k.Danger).SingleLine() })
}

func (a *app) bucketSettingsDialog(c *ui.Context) {
	bs := &a.mg.bs
	k := tk(c)
	_, winH := c.Size()
	ui.DialogBase(c, &bs.open, func(backdrop, panel ui.Element) {
		backdrop.Background(k.Scrim)
		// The same card as every dialog (dialogFrame); only the height is fixed,
		// since the nav and the pane share it and scroll inside it.
		dialogFrame(c, panel, 760, winH)
		panel.Height(min(560, max(360, winH-dialogMargin)))
		dialogHeader(c, locales.BucketSettingsTitle(), func() {
			ui.Text(c, bs.bucket).FontSize(sizeBody).TextColor(k.TextMuted).SingleLine()
		})
		// As in dialogW, the dividers show only while something scrolls.
		overflow := bs.navScroll.MaxY > 0 || bs.paneScroll.MaxY > 0
		divider(c, overflow)
		ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
			a.settingsNav(c)
			a.settingsPane(c)
		})
		divider(c, overflow)
		dialogFooter(c, func() {
			buttonRow(c, func() {
				if secondaryButton(c, locales.CommonClose(), false).Clicked() {
					bs.open = false
				}
			})
		})
	})
}

func (a *app) settingsNav(c *ui.Context) {
	bs := &a.mg.bs
	k := tk(c)
	ui.Scroll(c).TrackScroll(&bs.navScroll).Width(208).Shrink(0).MinHeight(0).Background(k.Sidebar).ScrollbarInsets(0, 1, 0, 0).Children(func() {
		ui.Column(c).Padding(8, scrollGutter, 8, 8).Gap(2).Children(func() {
			for i, sec := range bsSections {
				on := i == bs.section
				row := ui.ButtonBase(c.Key("sec:"+sec.label())).Label(sec.label()).Height(28).Padding(0, 8).Radius(6).AlignItems(ui.Center).Justify(ui.Start)
				switch {
				case on:
					row.Background(k.ActiveFill)
				case row.Hovered():
					row.Background(k.SurfaceHover)
				}
				row.Children(func() {
					w := 400
					if on {
						w = 600
						selectionBar(c, 6)
					}
					ui.Text(c, sec.label()).FontSize(sizeBody).FontWeight(w).TextColor(k.Text).SingleLine()
				})
				if row.Clicked() {
					bs.section = i
				}
			}
		})
	})
}

func (a *app) settingsPane(c *ui.Context) {
	a.ensureCfg()
	bs := &a.mg.bs
	sec, s := bs.cur()
	ui.Column(c.Key("pane:"+sec.label())).Grow(1).MinWidth(0).MinHeight(0).Padding(20, 0, 0, 0).Gap(16).Justify(ui.Start).Children(func() {
		ui.Row(c).Padding(0, 20).Shrink(0).Children(func() {
			labelHelp(c, sec.help(), func() ui.Element { return sectionTitle(c, sec.label()) })
		})
		// The form scrolls with its bar in the 20px right-hand gutter; the JSON
		// editor scrolls inside itself and takes the whole pane.
		scrolls := s.state == 2 && sec.form != secJSON
		body := func() {
			switch s.state {
			case 0, 1:
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c).Size(14, 14)
					mutedText(c, locales.CommonLoading())
				})
			case 3:
				if s.unsupp {
					emptyBlock(c, "circle-alert", locales.BucketSettingsUnsupported(), "", 200)
					return
				}
				errorLine(c, s.err)
				ghostButton(c, iconOf("refresh-cw"), locales.CommonRetry(), locales.CommonRetry(), false).AlignSelf(ui.Start).OnClick(func() { s.state = 0 })
			default:
				a.settingsBody(c, sec, s)
			}
		}
		if !scrolls {
			bs.paneScroll = ui.ScrollState{} // nothing scrolls here: no stale overflow
			ui.Column(c).Grow(1).MinHeight(0).Padding(0, 20, 20, 20).Gap(16).Children(body)
			return
		}
		ui.Scroll(c).TrackScroll(&bs.paneScroll).Grow(1).MinHeight(0).ScrollbarInsets(0, 2, 0, 0).Children(func() {
			ui.Column(c).Padding(0, 20, 20, 20).Gap(16).Children(body)
		})
	})
}

func (a *app) settingsBody(c *ui.Context, sec bsSection, s *cfgState) {
	switch sec.form {
	case secVersioning:
		a.versioningForm(c, sec, s)
	case secEncryption:
		ui.Column(c).Gap(12).Children(func() {
			formFieldHelp(c, locales.BucketSettingsDefaultEncryption(), locales.BucketSettingsDefaultEncryptionHelp(), "", func() {
				selectBox(c, locales.BucketSettingsDefaultEncryption(), &s.sse, sseChoices(), 240)
			})
			if s.sse == "aws:kms" {
				formFieldHelp(c, locales.BucketSettingsKmsKeyId(), locales.BucketSettingsKmsKeyIdHelp(), "", func() {
					inputBox(c, &s.kms, locales.BucketSettingsKmsKeyId(), locales.BucketSettingsKmsPlaceholder(), inputOpts{mono: true})
				})
			}
		})
		a.formFooter(c, sec, s, true)
	case secPublicAccess:
		ui.Column(c).Gap(4).Children(func() {
			labels, helps := pabLabels(), pabHelps()
			for i := range s.pab {
				switchRowHelp(c, labels[i], helps[i], &s.pab[i])
			}
		})
		a.formFooter(c, sec, s, true)
	case secOwnership:
		formField(c, locales.BucketSettingsOwnershipName(), "", func() { selectBox(c, locales.BucketSettingsOwnershipName(), &s.owner, ownChoices(), 260) })
		a.formFooter(c, sec, s, true)
	case secRequester:
		switchRow(c, locales.BucketSettingsRequesterName(), &s.requester)
		a.formFooter(c, sec, s, false)
	case secAccelerate:
		switchRow(c, locales.BucketSettingsAccelerateName(), &s.accel)
		a.formFooter(c, sec, s, false)
	default:
		a.jsonEditor(c, sec, s)
	}
}

// resultLine under a form: the last validate or save outcome.
func (a *app) cfgMessage(c *ui.Context, s *cfgState) {
	if s.msg == "" {
		return
	}
	if s.msgOK {
		resultLine(c, "check", tk(c).Success, s.msg)
	} else {
		resultLine(c, "circle-alert", tk(c).Danger, s.msg)
	}
}

func (a *app) versioningForm(c *ui.Context, sec bsSection, s *cfgState) {
	k := tk(c)
	if s.server == "" {
		ui.Column(c).Gap(12).Children(func() {
			mutedText(c, locales.BucketSettingsNeverVersioned())
			a.cfgMessage(c, s)
			ui.Row(c).Children(func() {
				if primaryButton(c, locales.BucketSettingsEnable(), s.saving).Clicked() {
					s.verStatus = 0
					a.saveCfg(sec, s, s.formJSON(sec.cfg), false)
				}
			})
		})
		return
	}
	ui.Column(c).Gap(12).Children(func() {
		formField(c, locales.CommonStatus(), "", func() {
			segmented(c, &s.verStatus, locales.BucketSettingsVersioningName(), locales.CommonEnabled(), locales.CommonSuspended())
		})
		a.cfgMessage(c, s)
		ui.Row(c).Justify(ui.End).Children(func() {
			if primaryButton(c, locales.CommonSave(), s.saving || !s.formDirty()).Clicked() {
				a.saveCfg(sec, s, s.formJSON(sec.cfg), false)
			}
		})
	})
	_ = k
}

// formFooter is the message line and the buttons under a small form.
func (a *app) formFooter(c *ui.Context, sec bsSection, s *cfgState, removable bool) {
	a.cfgMessage(c, s)
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		if removable && s.server != "" {
			if ghostDanger(c, locales.BucketSettingsRemoveButton(), locales.BucketSettingsRemoveTip(), s.saving).Clicked() {
				a.askRemoveCfg(sec, s)
			}
		}
		ui.Spacer(c)
		if primaryButton(c, locales.CommonSave(), s.saving || !s.formDirty()).Clicked() {
			switch {
			case sec.cfg == core.ConfigEncryption && s.sse == "none":
				if s.server == "" {
					return
				}
				a.saveCfg(sec, s, "", true)
			default:
				a.saveCfg(sec, s, s.formJSON(sec.cfg), false)
			}
		}
	})
}

func (a *app) jsonEditor(c *ui.Context, sec bsSection, s *cfgState) {
	k := tk(c)
	bs := &a.mg.bs
	if s.server == "" && !s.configuring {
		ui.Column(c).Grow(1).Center().Gap(4).Children(func() {
			ui.Text(c, locales.BucketSettingsNotConfigured()).FontSize(sizeBody).FontWeight(500).TextColor(k.Text)
			ui.Box(c).Height(8)
			if secondaryButton(c, locales.BucketSettingsConfigure(), false).Clicked() {
				s.draft, s.configuring, s.msg = bs.template(sec.cfg), true, ""
			}
		})
		return
	}
	a.codeEditor(c, sec, s)
	a.cfgMessage(c, s)
	changed := strings.TrimSpace(s.draft) != "" && s.draft != s.server
	ui.Row(c).Gap(8).AlignItems(ui.Center).Shrink(0).Children(func() {
		if sec.deletable && s.server != "" {
			if ghostDanger(c, locales.BucketSettingsRemoveButton(), locales.BucketSettingsRemoveTip(), s.saving).Clicked() {
				a.askRemoveCfg(sec, s)
			}
		}
		ui.Spacer(c)
		if secondaryButton(c, locales.BucketSettingsValidate(), s.saving).Clicked() {
			a.validateCfg(s)
		}
		if primaryButton(c, locales.CommonSave(), s.saving || !changed).Clicked() && a.validateCfg(s) {
			s.msg = ""
			a.saveCfg(sec, s, s.draft, false)
		}
	})
}

// codeTheme colors the JSON editor from the design tokens alone: neutral
// text, muted strings, subtle punctuation, Danger for invalid input.
func codeTheme(c *ui.Context) *editor.Theme {
	k := tk(c)
	th := editor.DefaultTheme(c.Theme())
	th.Background, th.Text = k.Sidebar, k.Text
	th.LineNumber, th.ActiveLineNumber = k.TextSubtle, k.TextMuted
	th.ActiveLine = k.ActiveFill.Alpha(0.5)
	th.Selection, th.InactiveSelection = k.Selection, k.Selection.Alpha(0.6)
	th.Cursor = k.Text
	th.IndentGuide, th.ActiveIndentGuide = k.Border, k.BorderStrong
	th.MatchingBracket = k.TextSubtle.Alpha(0.5)
	th.Error = k.Danger
	for i := range th.Tokens {
		th.Tokens[i] = editor.TokenStyle{Color: k.Text}
	}
	th.Tokens[editor.TokenString] = editor.TokenStyle{Color: k.TextMuted}
	th.Tokens[editor.TokenPunctuation] = editor.TokenStyle{Color: k.TextSubtle}
	th.Tokens[editor.TokenOperator] = editor.TokenStyle{Color: k.TextSubtle}
	th.Tokens[editor.TokenComment] = editor.TokenStyle{Color: k.TextSubtle}
	th.Tokens[editor.TokenInvalid] = editor.TokenStyle{Color: k.Danger}
	return th
}

// codeEditor is the JSON text of a section in mygo's code editor, kept in
// step with s.draft (which the rest of the dialog reads and writes).
func (a *app) codeEditor(c *ui.Context, sec bsSection, s *cfgState) {
	k := tk(c)
	if s.ed == nil {
		buf := editor.NewBuffer(s.draft)
		buf.SetLanguage(editor.LanguageNamed("json"))
		s.ed = &editor.State{Buffer: buf, NoFolding: true, Label: locales.BucketSettingsEditorLabel(sec.label())}
		s.edText, s.edVer = s.draft, buf.Version()
	} else if s.draft != s.edText { // set from outside: loaded, templated, reloaded
		s.ed.Buffer.Reset(s.draft)
		s.edText, s.edVer = s.draft, s.ed.Buffer.Version()
	}
	s.ed.Label = locales.BucketSettingsEditorLabel(sec.label())
	s.ed.Theme = codeTheme(c)
	box := ui.Column(c.Key("jsonbox:" + string(sec.cfg))).Grow(1).MinHeight(120).Radius(6).Background(k.Sidebar).Clip()
	if s.ed.Focused() {
		box.Border(1, k.Focus)
	} else {
		box.Border(1, k.BorderStrong)
	}
	box.Children(func() {
		editor.View(c, s.ed).Grow(1).FillWidth()
	})
	if v := s.ed.Buffer.Version(); v != s.edVer {
		s.draft = s.ed.Buffer.String()
		s.edText, s.edVer = s.draft, v
		s.msg = ""
	}
}
