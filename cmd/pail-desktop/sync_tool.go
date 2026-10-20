package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/icons"
	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/egoist/mygo/ui"
)

// Sync jobs tool: saved jobs on the left, the chosen job's editor on the
// right, and below it the preview of what a run would do. Run executes
// exactly the previewed plan; editing the job clears the preview.

type syncForm struct {
	name, dir, acct, bucket, prefix string
	up                              int // segmented: 0 upload, 1 download
	mode, compare, excludes         string
	noRules                         bool
}

type syncState struct {
	jobs     []core.SyncJob
	sel      int // index of the chosen saved job, -1 for the new draft
	hasDraft bool
	f        syncForm
	err      string
	saving   bool

	buckets map[string][]string // account id -> bucket names
	bLoad   map[string]bool

	pv       *core.SyncPreview
	pvSig    string
	pvBusy   bool
	pvErr    string
	pvGen    int
	pvCancel context.CancelFunc
	filter   int // 0 all, 1 changes, 2 deletes, 3 skipped
	shown    []core.SyncAction
	shownPV  *core.SyncPreview
	shownF   int
	list     ui.ListState
	base     string // signature of a new draft as created, to tell whether it was edited
	dsc      struct {
		open bool
		name string
		then func()
	}
}

func syncModeChoices() []choice {
	return []choice{
		{"new", locales.SyncModeNew()},
		{"new_changed", locales.SyncModeNewChanged()},
		{"mirror", locales.SyncModeMirror()},
	}
}

func syncCompareChoices() []choice {
	return []choice{
		{"size_time", locales.SyncCompareSizeTime()},
		{"size", locales.CommonSize()},
		{"checksum", locales.SyncCompareChecksum()},
	}
}

func syncColumns() []ui.TableColumn {
	return []ui.TableColumn{
		{Title: "", ID: "op", Width: 36, Fixed: true},
		{Title: locales.SyncPath(), ID: "path", MinWidth: 200},
		{Title: locales.SyncReason(), ID: "reason", Width: 180, MinWidth: 80},
		{Title: locales.CommonSize(), ID: "size", Width: 90, MinWidth: 60, Align: ui.End},
	}
}

// syncReasons are the fixed reasons the application layer gives to a sync
// action; any other reason (it may carry a detail) is shown as it came.
var syncReasons = map[string]func() string{
	"new":                  locales.SyncReasonNew,
	"exists":               locales.SyncReasonExists,
	"size differs":         locales.SyncReasonSizeDiffers,
	"newer":                locales.SyncReasonNewer,
	"time differs":         locales.SyncReasonTimeDiffers,
	"checksum differs":     locales.SyncReasonChecksumDiffers,
	"no checksum metadata": locales.SyncReasonNoChecksum,
	"no size metadata":     locales.SyncReasonNoSize,
	"unchanged":            locales.SyncReasonUnchanged,
	"not in the source":    locales.SyncReasonNotInSource,
	"excluded":             locales.SyncReasonExcluded,

	"symbolic link (not followed)":                         locales.SyncReasonSymlink,
	"reparse point or irregular file (not followed)":       locales.SyncReasonReparse,
	"Pail temporary file":                                  locales.SyncReasonTemp,
	"not a regular file (device, socket or pipe)":          locales.SyncReasonIrregular,
	"name is not valid UTF-8":                              locales.SyncReasonBadName,
	"object key is longer than 1024 bytes":                 locales.SyncReasonLongKey,
	"the remote object has no ETag to guard the overwrite": locales.SyncReasonNoETag,
}

// syncReason words the reason of a previewed action.
func syncReason(r string) string {
	if f, ok := syncReasons[r]; ok {
		return f()
	}
	return r
}

func newSyncForm(acct, bucket string) syncForm {
	return syncForm{acct: acct, bucket: bucket, mode: "new_changed", compare: "size_time"}
}

func formFromJob(j core.SyncJob) syncForm {
	f := syncForm{name: j.Name, dir: j.LocalDir, acct: j.Remote.AccountID, bucket: j.Remote.Bucket, prefix: j.Remote.Prefix,
		mode: j.Mode, compare: j.Compare, excludes: strings.Join(j.Excludes, "\n"), noRules: j.NoRules}
	if j.Direction == "download" {
		f.up = 1
	}
	return f
}

func (f *syncForm) job(id string) core.SyncJob {
	j := core.SyncJob{ID: id, Name: strings.TrimSpace(f.name), LocalDir: strings.TrimSpace(f.dir),
		Remote:    Scope{AccountID: f.acct, Bucket: strings.TrimSpace(f.bucket), Prefix: strings.TrimSpace(f.prefix)},
		Direction: "upload", Mode: f.mode, Compare: f.compare, NoRules: f.noRules}
	if f.up == 1 {
		j.Direction = "download"
	}
	for _, l := range strings.Split(f.excludes, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			j.Excludes = append(j.Excludes, l)
		}
	}
	return j
}

func jobSig(j core.SyncJob) string {
	b, _ := json.Marshal(j)
	return string(b)
}

func (s *syncState) jobID() string {
	if s.sel >= 0 && s.sel < len(s.jobs) {
		return s.jobs[s.sel].ID
	}
	return ""
}

func (s *syncState) draftJob() core.SyncJob { return s.f.job(s.jobID()) }

// edited reports unsaved edits: a saved job that differs from its saved form,
// or a new draft the user has changed.
func (s *syncState) edited() bool {
	switch {
	case s.sel >= 0 && s.sel < len(s.jobs):
		return jobSig(s.draftJob()) != jobSig(s.jobs[s.sel])
	case s.hasDraft:
		return jobSig(s.draftJob()) != s.base
	}
	return false
}

// syncGuard runs then at once, or after the user decided what happens to
// unsaved edits.
func (a *app) syncGuard(then func()) {
	s := &a.tl.sync
	if !s.edited() {
		then()
		return
	}
	name := strings.TrimSpace(s.f.name)
	if name == "" && s.sel >= 0 && s.sel < len(s.jobs) {
		name = s.jobs[s.sel].Name
	}
	s.dsc.open, s.dsc.name, s.dsc.then = true, firstNonEmpty(name, locales.SyncNewJob()), then
}

func (s *syncState) dirty() bool {
	if s.sel < 0 {
		return s.hasDraft
	}
	return jobSig(s.draftJob()) != jobSig(s.jobs[s.sel])
}

// remoteText is "s3://bucket/prefix".
func remoteText(r Scope) string { return "s3://" + r.Bucket + "/" + r.Prefix }

// ---- model ------------------------------------------------------------------------------------

func (a *app) syncEnter() {
	s := &a.tl.sync
	if s.pvCancel != nil {
		s.pvCancel()
	}
	*s = syncState{sel: -1, buckets: map[string][]string{}, bLoad: map[string]bool{}, pvGen: s.pvGen + 1}
	s.jobs = a.svc.SyncJobs()
	if len(s.jobs) > 0 {
		a.syncSelect(0)
	}
}

func (a *app) syncClearPreview() {
	s := &a.tl.sync
	if s.pvCancel != nil {
		s.pvCancel()
		s.pvCancel = nil
	}
	s.pvGen++
	s.pv, s.pvBusy, s.pvErr, s.shown, s.shownPV = nil, false, "", nil, nil
}

func (a *app) syncSelect(i int) {
	s := &a.tl.sync
	if i < 0 || i >= len(s.jobs) {
		return
	}
	s.sel, s.hasDraft, s.err = i, false, ""
	s.f = formFromJob(s.jobs[i])
	a.syncClearPreview()
}

func (a *app) syncNew() {
	s := &a.tl.sync
	s.sel, s.hasDraft, s.err = -1, true, ""
	s.f = newSyncForm(a.accountID, a.scope.Bucket)
	s.base = jobSig(s.draftJob())
	a.syncClearPreview()
}

// syncEnsureBuckets loads the bucket names of the form's account once.
func (a *app) syncEnsureBuckets(acct string) {
	s := &a.tl.sync
	if acct == "" || s.buckets == nil {
		return
	}
	if _, ok := s.buckets[acct]; ok || s.bLoad[acct] {
		return
	}
	if acct == a.accountID && len(a.buckets) > 0 {
		for _, b := range a.buckets {
			s.buckets[acct] = append(s.buckets[acct], b.Name)
		}
		return
	}
	s.bLoad[acct] = true
	a.bg(func() func() {
		ctx, cancel := a.timeout(30 * time.Second)
		defer cancel()
		bs, err := a.svc.Buckets(ctx, acct)
		return func() {
			delete(s.bLoad, acct)
			names := []string{}
			if err == nil {
				for _, b := range bs {
					names = append(names, b.Name)
				}
			}
			s.buckets[acct] = names
		}
	})
}

func (a *app) syncSave(then ...func()) {
	s := &a.tl.sync
	if s.saving || (s.sel < 0 && !s.hasDraft) {
		return
	}
	job := s.draftJob()
	s.saving, s.err = true, ""
	a.bg(func() func() {
		saved, err := a.svc.SaveSyncJob(job)
		return func() {
			s.saving = false
			if err != nil {
				s.err = humanError(err)
				return
			}
			s.jobs = a.svc.SyncJobs()
			for i, j := range s.jobs {
				if j.ID == saved.ID {
					s.sel = i
				}
			}
			s.hasDraft = false
			s.f = formFromJob(saved)
			a.toast(locales.SyncSaved())
			for _, f := range then {
				f()
			}
		}
	})
}

func (a *app) syncAskDelete() {
	s := &a.tl.sync
	if s.sel < 0 {
		if s.hasDraft {
			s.hasDraft = false
			a.syncClearPreview()
			if len(s.jobs) > 0 {
				a.syncSelect(0)
			}
		}
		return
	}
	j := s.jobs[s.sel]
	name := j.Name
	a.confirm = confirmState{
		open: true, title: locales.SyncDeleteTitle(),
		rows:    []KV{{locales.SyncRowJob(), name}, {locales.SyncRowLocal(), j.LocalDir}, {locales.SyncRowRemote(), remoteText(j.Remote)}},
		message: locales.SyncDeleteNote(),
		action:  locales.SyncDeleteAction(), danger: true,
		run: func() {
			a.bg(func() func() {
				err := a.svc.DeleteSyncJob(j.ID)
				return func() {
					if err != nil {
						s.err = humanError(err)
						return
					}
					s.jobs = a.svc.SyncJobs()
					s.sel, s.hasDraft = -1, false
					a.syncClearPreview()
					if len(s.jobs) > 0 {
						a.syncSelect(0)
					}
				}
			})
		},
	}
}

func (a *app) syncChoose() {
	go func() {
		ds := a.pick(true)
		if len(ds) == 0 {
			return
		}
		a.post(func() { a.tl.sync.f.dir = ds[0] })
	}()
}

func (a *app) syncPreview() {
	s := &a.tl.sync
	if s.pvBusy || (s.sel < 0 && !s.hasDraft) {
		return
	}
	job := s.draftJob()
	a.syncClearPreview()
	if job.LocalDir == "" || job.Remote.Bucket == "" {
		s.pvErr = locales.SyncChooseFirst()
		return
	}
	gen, sig := s.pvGen, jobSig(job)
	ctx, cancel := a.timeout(10 * time.Minute)
	s.pvCancel, s.pvBusy = cancel, true
	a.bg(func() func() {
		defer cancel()
		pv, err := a.svc.PreviewSync(ctx, job)
		return func() {
			if gen != s.pvGen {
				return
			}
			s.pvBusy = false
			if err != nil {
				s.pvErr = humanError(err)
				return
			}
			s.pv, s.pvSig, s.filter = &pv, sig, 0
		}
	})
}

// rebuildShown filters the previewed actions for the table.
func (s *syncState) rebuildShown() {
	s.shown = s.shown[:0]
	if s.pv != nil {
		for _, act := range s.pv.Actions {
			ok := false
			switch s.filter {
			case 0:
				ok = true
			case 1:
				ok = act.Op == "upload" || act.Op == "download"
			case 2:
				ok = act.Op == "delete"
			case 3:
				ok = act.Op == "skip"
			}
			if ok {
				s.shown = append(s.shown, act)
			}
		}
	}
	s.shownPV, s.shownF = s.pv, s.filter
}

// trashName is where local deletes of a sync go.
func trashName() string {
	if runtime.GOOS == "windows" {
		return locales.SyncTrashRecycleBin()
	}
	return locales.SyncTrashTrash()
}

func (a *app) syncAskRun() {
	s := &a.tl.sync
	if s.pv == nil {
		return
	}
	pv := *s.pv
	job := pv.Job
	rows := []KV{{locales.SyncRowJob(), job.Name}}
	if job.Name == "" {
		rows[0].Value = locales.SyncUntitledJob()
	}
	rows = append(rows, KV{locales.SyncRowLocal(), job.LocalDir}, KV{locales.SyncRowRemote(), remoteText(job.Remote)})
	rows = append(rows, KV{locales.SyncRowUploads(), locales.CommonFiles(pv.Uploads)}, KV{locales.SyncRowDownloads(), locales.CommonFiles(pv.Downloads)})
	delText := locales.CommonFiles(pv.Deletes)
	if pv.Deletes > 0 {
		delText = locales.SyncDeleteWill(pv.Deletes)
	}
	rowDeletes := locales.SyncRowDeletes()
	rows = append(rows, KV{rowDeletes, delText}, KV{locales.CommonSize(), humanBytes(pv.Bytes)})
	msg := ""
	if pv.Deletes > 0 && job.Direction == "download" {
		msg = locales.SyncTrashNote(trashName())
	}
	if pv.Uploads+pv.Downloads+pv.Deletes == 0 {
		msg = locales.SyncNothingToDo()
	}
	a.confirm = confirmState{
		open: true, title: locales.SyncRunTitle(), rows: rows, message: msg,
		action: locales.SyncRun(), danger: pv.Deletes > 0, dangerRows: dangerIf(pv.Deletes > 0, rowDeletes),
		run: func() {
			a.bg(func() func() {
				_, err := a.svc.RunSync(pv)
				return func() {
					if err != nil {
						a.toast(locales.SyncStartFailed(humanError(err)))
						return
					}
					a.transfersOpen, a.transTab = true, 0
					a.syncClearPreview()
					a.toast(locales.SyncStarted())
				}
			})
		},
	}
}

// ---- view -------------------------------------------------------------------------------------

func (a *app) syncView(c *ui.Context) {
	s := &a.tl.sync
	// A preview of a job that has been edited since is stale.
	if s.pv != nil || s.pvBusy {
		cur := jobSig(s.draftJob())
		if s.pvSig != cur && s.pv != nil {
			a.syncClearPreview()
		}
	}
	if s.pv != nil && (s.shownPV != s.pv || s.shownF != s.filter) {
		s.rebuildShown()
	}
	if s.hasDraft || s.sel >= 0 {
		a.syncEnsureBuckets(s.f.acct)
	}
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		a.syncList(c)
		a.syncEditor(c)
	})
}

func (a *app) syncList(c *ui.Context) {
	k := tk(c)
	s := &a.tl.sync
	toolPane(c, "sync-pane", 300, func() {
		paneBar(c, "sync-bar", func() {
			if ghostButton(c, icons.Get("plus"), locales.SyncNew(), locales.SyncNewTip(), false).Clicked() {
				a.syncGuard(a.syncNew)
			}
			ui.Spacer(c)
			if ghostButton(c, icons.Get("trash-2"), locales.CommonDelete(), locales.SyncDeleteJobTip(), s.sel < 0 && !s.hasDraft).Clicked() {
				a.syncAskDelete()
			}
		})
		if len(s.jobs) == 0 && !s.hasDraft {
			emptyBlock(c, "refresh-ccw", locales.SyncEmpty(), "", 0)
			return
		}
		ui.Scroll(c).Grow(1).MinHeight(0).ScrollbarInsets(0, 2, 0, 0).Children(func() {
			ui.Column(c).Gap(2).Padding(0, 0, 8, 0).Children(func() {
				for i := range s.jobs {
					j := s.jobs[i]
					if i == s.sel {
						j = s.draftJob() // the list follows the edits
						j.Name = firstNonEmpty(j.Name, s.jobs[i].Name)
					}
					a.syncRow(c, "job:"+strconv.Itoa(i), i == s.sel, j, func() {
						if i != s.sel {
							a.syncGuard(func() { a.syncSelect(i) })
						}
					})
				}
				if s.hasDraft {
					j := s.draftJob()
					a.syncRow(c, "job:new", true, j, func() {})
				}
			})
		})
		_ = k
	})
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (a *app) syncRow(c *ui.Context, key string, on bool, j core.SyncJob, click func()) {
	k := tk(c)
	row := listRow(c, key, on)
	row.Children(func() {
		icon := "arrow-up"
		if j.Direction == "download" {
			icon = "arrow-down"
		}
		ui.Icon(c, icons.Get(icon)).FontSize(14).TextColor(k.TextMuted).Shrink(0)
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			name := j.Name
			if name == "" {
				name = locales.SyncNewJob()
			}
			ui.Text(c, name).FontSize(sizeBody).FontWeight(500).TextColor(k.Text).SingleLine()
			line := filepath.Base(j.LocalDir) + " ↔ " + remoteText(j.Remote)
			if j.LocalDir == "" {
				line = remoteText(j.Remote)
			}
			ui.Text(c, line).FontSize(sizeSecondary).TextColor(k.TextMuted).SingleLine().Tooltip(line)
		})
	})
	if row.Clicked() {
		click()
	}
}

func (a *app) syncEditor(c *ui.Context) {
	k := tk(c)
	s := &a.tl.sync
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
		if s.sel < 0 && !s.hasDraft {
			ui.Column(c).Grow(1).Center().Gap(12).Children(func() {
				emptyBlock(c, "refresh-ccw", locales.SyncCreate(), "", 90)
				secondaryButton(c, locales.SyncNewJob(), false).OnClick(func() { a.syncGuard(a.syncNew) })
			})
			return
		}
		ui.Scroll(c).Grow(1).MinHeight(0).ScrollbarInsets(0, 2, 0, 0).Children(func() {
			ui.Column(c).MaxWidth(880).Padding(20, 24, 24, 24).Gap(16).Children(func() {
				a.syncFields(c)
				a.syncActions(c)
				a.syncPreviewPane(c)
			})
		})
		_ = k
	})
}

func (a *app) syncFields(c *ui.Context) {
	k := tk(c)
	s := &a.tl.sync
	f := &s.f
	ui.Row(c).Gap(12).Children(func() {
		cell(c, "sn", func() {
			formField(c, locales.CommonName(), "", func() {
				inputBox(c, &f.name, locales.SyncJobName(), locales.SyncNamePh(), inputOpts{})
			})
		})
		ui.Box(c.Key("sd")).Shrink(0).Children(func() {
			formFieldHelp(c, locales.SyncDirection(), locales.SyncDirectionHelp(), "", func() {
				segmented(c, &f.up, locales.SyncDirection(), locales.SyncUpload(), locales.SyncDownload())
			})
		})
	})
	formField(c, locales.SyncLocalFolder(), "", func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Grow(1).MinWidth(0).Children(func() {
				inputBox(c, &f.dir, locales.SyncLocalFolder(), locales.SyncFolderPath(), inputOpts{mono: true})
			})
			if secondaryButton(c, locales.CommonChoose(), false).Clicked() {
				a.syncChoose()
			}
		})
	})
	ui.Row(c).Gap(12).Children(func() {
		cell(c, "sa", func() {
			formField(c, locales.CommonAccount(), "", func() {
				var opts []choice
				for _, ac := range a.accounts {
					opts = append(opts, choice{ac.ID, ac.Name})
				}
				if selectBox(c, locales.SyncAccountLabel(), &f.acct, opts, 0) {
					f.bucket = ""
				}
			})
		})
		cell(c, "sb", func() {
			formField(c, locales.CommonBucket(), "", func() {
				names := s.buckets[f.acct]
				if len(names) == 0 {
					inputBox(c, &f.bucket, locales.SyncBucketLabel(), locales.SyncBucketName(), inputOpts{mono: true})
					return
				}
				if f.bucket != "" && !slices.Contains(names, f.bucket) {
					names = append(slices.Clone(names), f.bucket)
				}
				var opts []choice
				for _, n := range names {
					opts = append(opts, choice{n, n})
				}
				selectBox(c, locales.SyncBucketLabel(), &f.bucket, opts, 0)
			})
		})
		cell(c, "sp", func() {
			formField(c, locales.CommonPrefix(), "", func() {
				inputBox(c, &f.prefix, locales.SyncPrefixLabel(), locales.SyncPrefixPh(), inputOpts{mono: true})
			})
		})
	})
	ui.Row(c).Gap(12).AlignItems(ui.Start).Children(func() {
		cell(c, "sm", func() {
			ui.Column(c).Gap(6).Children(func() {
				formFieldHelp(c, locales.SyncMode(), locales.SyncModeHelp(trashName()), "", func() {
					selectBox(c, locales.SyncModeLabel(), &f.mode, syncModeChoices(), 0)
				})
				if f.mode == "mirror" {
					ui.Row(c).Gap(6).AlignItems(ui.Start).Children(func() {
						ui.Icon(c, icons.Get("circle-alert")).FontSize(14).TextColor(k.Danger).Shrink(0).MarginY(1)
						ui.Text(c, locales.SyncMirrorWarning()).FontSize(sizeSecondary).TextColor(k.Danger)
					})
				}
			})
		})
		cell(c, "sc", func() {
			formFieldHelp(c, locales.SyncCompare(), locales.SyncCompareHelp(), "", func() {
				selectBox(c, locales.SyncCompareLabel(), &f.compare, syncCompareChoices(), 0)
			})
		})
	})
	formFieldHelp(c, locales.SyncExcludes(), locales.SyncExcludesHelp(), "", func() {
		textAreaBox(c, &f.excludes, locales.SyncExcludes(), locales.SyncExcludesPh(), 72, inputOpts{mono: true})
	})
}

func (a *app) syncActions(c *ui.Context) {
	k := tk(c)
	s := &a.tl.sync
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		if secondaryButton(c, locales.CommonSave(), s.saving || !s.dirty()).Clicked() {
			a.syncSave()
		}
		if secondaryButton(c, locales.SyncPreview(), s.pvBusy).Clicked() {
			a.syncPreview()
		}
		helpIcon(c, locales.SyncPreviewHelp())
		if s.pvBusy {
			ui.Spinner(c).Size(14, 14)
			mutedText(c, locales.SyncComparing())
		}
		if s.err != "" {
			ui.Row(c).Shrink(1).MinWidth(0).Children(func() { resultLine(c, "circle-alert", k.Danger, s.err) })
		}
		ui.Spacer(c)
		if primaryButton(c, locales.SyncRun(), s.pv == nil || s.pvBusy).Clicked() {
			a.syncAskRun()
		}
	})
}

func (a *app) syncPreviewPane(c *ui.Context) {
	k := tk(c)
	s := &a.tl.sync
	if s.pvErr != "" {
		resultLine(c, "circle-alert", k.Danger, s.pvErr)
	}
	pv := s.pv
	if pv == nil {
		return
	}
	hairline(c)
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		chip(c, "arrow-up", locales.SyncUploads(pv.Uploads), k.Text, false)
		chip(c, "arrow-down", locales.SyncDownloads(pv.Downloads), k.Text, false)
		if pv.Deletes > 0 {
			chip(c, "trash-2", locales.SyncDeletes(pv.Deletes), k.Danger, true)
		} else {
			chip(c, "trash-2", locales.SyncDeletes(0), k.TextMuted, false)
		}
		chip(c, "minus", locales.SyncSkipped(pv.Skips), k.TextMuted, false)
		if pv.Bytes > 0 {
			ui.Text(c, humanBytes(pv.Bytes)).FontSize(sizeSecondary).TextColor(k.TextMuted).SingleLine().Shrink(0)
		}
		ui.Spacer(c)
		segmented(c, &s.filter, locales.SyncFilter(), locales.SyncFilterAll(), locales.SyncFilterChanges(), locales.SyncFilterDeletes(), locales.SyncFilterSkipped())
	})
	switch {
	case len(pv.Actions) == 0:
		emptyBlock(c, "circle-check", locales.SyncNothing(), "", 120)
	case len(s.shown) == 0:
		emptyBlock(c, "list-filter", locales.SyncNoActionsFilter(), "", 120)
	default:
		shown := s.shown
		s.list.Key = func(i int) any { return i }
		toolTable(c, &s.list, syncColumns(), len(shown), func(row, col int) {
			act := &shown[row]
			switch col {
			case 0:
				icon, col := "minus", k.TextSubtle
				switch act.Op {
				case "upload":
					icon, col = "arrow-up", k.TextMuted
				case "download":
					icon, col = "arrow-down", k.TextMuted
				case "delete":
					icon, col = "trash-2", k.Danger
				}
				ui.Icon(c, icons.Get(icon)).FontSize(14).TextColor(col)
			case 1:
				mono(ui.Text(c, act.Path).FontSize(sizeSecondary).TextColor(k.Text).SingleLine().Grow(1).MinWidth(0).Tooltip(act.Path))
			case 2:
				secondaryCell(c, syncReason(act.Reason), true, ui.Start)
			case 3:
				secondaryCell(c, humanBytes(act.Size), act.Size > 0, ui.End)
			}
		}).Height(300).Label(locales.SyncActionsLabel())
	}
}

// syncDiscardDialog asks what to do with unsaved edits before the editor
// moves to another job, a new draft, or out of the tool.
func (a *app) syncDiscardDialog(c *ui.Context) {
	s := &a.tl.sync
	if !s.dsc.open {
		return
	}
	dialog(c, &s.dsc.open, locales.SyncDiscardTitle(s.dsc.name), nil, func() {
		buttonRow(c, func() {
			if secondaryButton(c, locales.CommonCancel(), false).Clicked() {
				s.dsc.open, s.dsc.then = false, nil
			}
			if ghostDanger(c, locales.SyncDiscard(), locales.SyncDiscardTip(), false).Clicked() {
				then := s.dsc.then
				s.dsc.open, s.dsc.then = false, nil
				if then != nil {
					then()
				}
			}
			if primaryButton(c, locales.CommonSave(), s.saving).Clicked() {
				then := s.dsc.then
				s.dsc.open, s.dsc.then = false, nil
				a.syncSave(then)
			}
		})
	})
}
