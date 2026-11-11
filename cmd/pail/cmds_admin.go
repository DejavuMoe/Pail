package main

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/DejavuMoe/Pail/internal/app"
)

// ---- bucket alias --------------------------------------------------------------------

func setupBucket(c *cli, fs *flag.FlagSet) runner {
	clearAlias := fs.Bool("clear", false, "alias: remove the display name")
	return func(ctx context.Context, pos []string) error {
		if len(pos) < 2 || pos[0] != "alias" || len(pos) > 3 {
			return usagef("usage: pail bucket alias s3://bucket [NAME | --clear]")
		}
		p, err := parseS3(pos[1])
		if err != nil {
			return err
		}
		if p.Key != "" {
			return usagef("bucket alias takes a bucket only, not a key")
		}
		if *clearAlias && len(pos) == 3 {
			return usagef("--clear takes no NAME")
		}
		if len(pos) == 2 && !*clearAlias {
			a := c.svc.BucketAlias(c.acct.ID, p.Bucket)
			if c.g.json {
				c.emit(map[string]any{"type": "alias", "bucket": p.Bucket, "alias": a})
				c.emit(map[string]any{"type": "summary", "command": "bucket alias", "ok": true})
				return nil
			}
			fmt.Fprintln(c.io.Stdout, orDash(a))
			return nil
		}
		name := ""
		if len(pos) == 3 {
			name = pos[2]
		}
		if err := c.svc.SetBucketAlias(c.acct.ID, p.Bucket, name); err != nil {
			return err
		}
		got := c.svc.BucketAlias(c.acct.ID, p.Bucket)
		text := "removed the display name of s3://" + p.Bucket
		if got != "" {
			text = fmt.Sprintf("s3://%s is now shown as %q in Pail", p.Bucket, got)
		}
		return c.single("bucket alias", text, map[string]any{"bucket": p.Bucket, "alias": got})
	}
}

// ---- multipart -------------------------------------------------------------------------

// parseAge reads a duration such as 36h, 90m or 7d.
func parseAge(s string) (time.Duration, error) {
	if n, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.ParseFloat(n, 64)
		nanos := days * float64(24*time.Hour)
		// The comparison also rejects NaN, infinities and sub-nanosecond ages.
		if err != nil || !(nanos >= 1 && nanos < 1<<63) {
			return 0, usagef("--older-than must be a positive duration such as 36h or 7d, not %q", s)
		}
		return time.Duration(nanos), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, usagef("--older-than must be a positive duration such as 36h or 7d, not %q", s)
	}
	return d, nil
}

func setupMultipart(c *cli, fs *flag.FlagSet) runner {
	older := fs.String("older-than", "", "abort: only uploads started longer ago than this (for example 36h or 7d)")
	return func(ctx context.Context, pos []string) error {
		if len(pos) != 2 || (pos[0] != "ls" && pos[0] != "abort") {
			return usagef("usage: pail multipart ls s3://bucket[/prefix/] | multipart abort s3://bucket[/prefix/] [--older-than D]")
		}
		sub := pos[0]
		var age time.Duration
		if sub == "ls" {
			if err := onlyFlags(fs, "multipart ls"); err != nil {
				return err
			}
		} else if *older != "" {
			var err error
			if age, err = parseAge(*older); err != nil {
				return err
			}
		}
		p, err := parseS3(pos[1])
		if err != nil {
			return err
		}
		loc := c.loc(p.Bucket, folderPrefix(p.Key))
		ups, err := c.svc.MultipartUploads(ctx, loc)
		if err != nil {
			return err
		}
		if age > 0 {
			cutoff := time.Now().Add(-age)
			kept := ups[:0:0]
			for _, u := range ups {
				if u.Initiated.Before(cutoff) {
					kept = append(kept, u)
				}
			}
			ups = kept
		}
		sort.Slice(ups, func(i, j int) bool {
			if !ups[i].Initiated.Equal(ups[j].Initiated) {
				return ups[i].Initiated.Before(ups[j].Initiated)
			}
			return ups[i].Key < ups[j].Key
		})
		if sub == "ls" {
			return c.printUploads(ups)
		}
		if len(ups) == 0 {
			c.infof("nothing to abort: no matching unfinished multipart upload")
			if c.g.json {
				c.emit(map[string]any{"type": "summary", "command": "multipart abort", "ok": true, "succeeded": 0})
			}
			return nil
		}
		scope := []string{
			fmt.Sprintf("Abort: %d unfinished multipart upload(s) under %s", len(ups), locURL(loc)),
			"The parts already uploaded are discarded; an interrupted upload cannot be resumed afterwards.",
		}
		if age > 0 {
			scope = append(scope, "Only uploads started more than "+*older+" ago.")
		}
		if err := c.confirm(scope); err != nil {
			return err
		}
		w := c.watch()
		defer w.close()
		id, err := c.svc.AbortMultipartUploads(loc, ups)
		if err != nil {
			return err
		}
		return c.finish(w.wait(ctx, id), "multipart abort", ctx)
	}
}

func (c *cli) printUploads(ups []app.MultipartUpload) error {
	if c.g.json {
		for _, u := range ups {
			c.emit(map[string]any{"type": "upload", "key": u.Key, "uploadId": u.UploadID, "initiated": u.Initiated,
				"storageClass": u.StorageClass, "initiator": u.Initiator})
		}
		c.emit(map[string]any{"type": "summary", "command": "multipart ls", "ok": true, "count": len(ups)})
		return nil
	}
	tw := tabwriter.NewWriter(c.io.Stdout, 2, 4, 2, ' ', 0)
	for _, u := range ups {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", fmtTime(u.Initiated), u.StorageClass, u.UploadID, u.Key)
	}
	return tw.Flush()
}

// ---- sync jobs ---------------------------------------------------------------------------

func isSyncJobCommand(pos []string) bool {
	return len(pos) >= 2 && pos[0] == "job" && (pos[1] == "ls" || pos[1] == "add" || pos[1] == "rm")
}

// syncJobCommand runs "sync job ls|add|rm"; pos starts after "job".
func (c *cli) syncJobCommand(fs *flag.FlagSet, pos []string, mode, compare string, excludes []string, noRules bool) error {
	sub, args := pos[0], pos[1:]
	allowed := []string{}
	if sub == "add" {
		allowed = []string{"mode", "compare", "exclude", "no-rules"}
	}
	if err := onlyFlags(fs, "sync job "+sub, allowed); err != nil {
		return err
	}
	jobs := c.svc.SyncJobs()
	switch sub {
	case "ls":
		if len(args) != 0 {
			return usagef("sync job ls takes no arguments")
		}
		names := map[string]string{}
		for _, a := range c.svc.Accounts() {
			names[a.ID] = a.Name
		}
		if c.g.json {
			for _, j := range jobs {
				c.emit(map[string]any{"type": "job", "id": j.ID, "name": j.Name, "account": names[j.Remote.AccountID],
					"accountId": j.Remote.AccountID, "direction": j.Direction, "local": j.LocalDir, "remote": locURL(j.Remote),
					"mode": j.Mode, "compare": j.Compare, "excludes": j.Excludes, "noRules": j.NoRules})
			}
			c.emit(map[string]any{"type": "summary", "command": "sync job ls", "ok": true, "count": len(jobs)})
			return nil
		}
		if len(jobs) == 0 {
			c.infof("no saved sync jobs; add one with pail sync job add")
			return nil
		}
		tw := tabwriter.NewWriter(c.io.Stdout, 2, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tACCOUNT\tDIRECTION\tMODE\tLOCAL\tREMOTE")
		for _, j := range jobs {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", j.Name, orDash(names[j.Remote.AccountID]), j.Direction, j.Mode, j.LocalDir, locURL(j.Remote))
		}
		return tw.Flush()
	case "add":
		if len(args) != 3 {
			return usagef("sync job add takes NAME and LOCAL s3://bucket/prefix/ (upload) or s3://bucket/prefix/ LOCAL (download)")
		}
		name := strings.TrimSpace(args[0])
		if name == "" {
			return usagef("the job needs a name")
		}
		for _, j := range jobs {
			if strings.EqualFold(j.Name, name) {
				return usagef("a sync job named %q already exists (%s)", j.Name, j.ID)
			}
		}
		job, err := c.syncJob(args[1:], "", mode, compare, excludes, noRules, true)
		if err != nil {
			return err
		}
		job.Name = name
		saved, err := c.svc.SaveSyncJob(job)
		if err != nil {
			return err
		}
		return c.single("sync job add", fmt.Sprintf("saved sync job %s (%s): %s %s %s", saved.Name, saved.ID, saved.LocalDir,
			map[string]string{"upload": "->", "download": "<-"}[saved.Direction], locURL(saved.Remote)),
			map[string]any{"id": saved.ID, "name": saved.Name})
	case "rm":
		if len(args) != 1 {
			return usagef("sync job rm takes one job name or ID")
		}
		var hit []app.SyncJob
		for _, j := range jobs {
			if j.ID == args[0] {
				hit = []app.SyncJob{j}
				break
			}
			if strings.EqualFold(j.Name, args[0]) {
				hit = append(hit, j)
			}
		}
		switch len(hit) {
		case 0:
			return usagef("no sync job named %q", args[0])
		case 1:
		default:
			return usagef("job name %q is ambiguous; use the ID (pail sync job ls)", args[0])
		}
		j := hit[0]
		if err := c.confirm([]string{
			fmt.Sprintf("Remove saved sync job: %s (%s)", j.Name, j.ID),
			fmt.Sprintf("%s %s %s", j.LocalDir, map[string]string{"upload": "->", "download": "<-"}[j.Direction], locURL(j.Remote)),
			"Only the saved job is removed; local files and objects are not touched.",
		}); err != nil {
			return err
		}
		if err := c.svc.DeleteSyncJob(j.ID); err != nil {
			return err
		}
		return c.single("sync job rm", fmt.Sprintf("removed sync job %s (%s)", j.Name, j.ID), map[string]any{"id": j.ID, "name": j.Name})
	}
	return nil
}

// ---- cloudfront ----------------------------------------------------------------------------

func setupCloudFront(c *cli, fs *flag.FlagSet) runner {
	bucket := fs.String("bucket", "", "ls: only distributions with an origin in this bucket")
	return func(ctx context.Context, pos []string) error {
		if len(pos) == 0 || (pos[0] != "ls" && pos[0] != "invalidate") {
			return usagef("usage: pail cloudfront ls [--bucket B] | cloudfront invalidate DISTRIBUTION PATH...")
		}
		if c.acct.Provider != app.ProviderAWS {
			return &exitErr{exitFailure, fmt.Sprintf("%s: CloudFront is only available for AWS accounts; %q uses provider %q",
				kindName(app.ErrUnsupported), c.acct.Name, c.acct.Provider)}
		}
		if pos[0] == "ls" {
			if len(pos) != 1 {
				return usagef("cloudfront ls takes no arguments")
			}
			var rows []distRow
			add := func(id, domain, status string, enabled bool, aliases []string) {
				rows = append(rows, distRow{id, domain, status, enabled, aliases})
			}
			if *bucket != "" {
				ds, err := c.svc.DistributionsForBucket(ctx, c.acct.ID, *bucket)
				if err != nil {
					return err
				}
				for _, d := range ds {
					add(d.ID, d.DomainName, d.Status, d.Enabled, d.Aliases)
				}
			} else {
				ds, err := c.svc.Distributions(ctx, c.acct.ID)
				if err != nil {
					return err
				}
				for _, d := range ds {
					add(d.ID, d.DomainName, d.Status, d.Enabled, d.Aliases)
				}
			}
			return c.printDistributions(rows)
		}
		if err := onlyFlags(fs, "cloudfront invalidate"); err != nil {
			return err
		}
		if len(pos) < 3 {
			return usagef("cloudfront invalidate takes a distribution ID and at least one path such as /index.html or /img/*")
		}
		dist, paths := pos[1], pos[2:]
		scope := []string{
			"Invalidate CloudFront distribution: " + dist,
			fmt.Sprintf("Paths:  %d", len(paths)),
		}
		for i, p := range paths {
			if i == 10 {
				scope = append(scope, fmt.Sprintf("        ... and %d more", len(paths)-10))
				break
			}
			scope = append(scope, "        "+p)
		}
		scope = append(scope, "Cached copies of these paths are dropped at every edge location; the next requests fetch them from the origin.")
		if err := c.confirm(scope); err != nil {
			return err
		}
		inv, err := c.svc.Invalidate(ctx, c.acct.ID, dist, paths)
		if err != nil {
			return err
		}
		if c.g.json {
			c.emit(map[string]any{"type": "invalidation", "id": inv.ID, "distribution": inv.DistributionID, "status": inv.Status, "paths": inv.Paths})
			c.emit(map[string]any{"type": "summary", "command": "cloudfront invalidate", "ok": true})
			return nil
		}
		fmt.Fprintf(c.io.Stdout, "invalidation %s created for %s (%s, %d path(s))\n", inv.ID, inv.DistributionID, inv.Status, len(inv.Paths))
		return nil
	}
}

type distRow struct {
	id, domain, status string
	enabled            bool
	aliases            []string
}

func (c *cli) printDistributions(rows []distRow) error {
	if c.g.json {
		for _, r := range rows {
			c.emit(map[string]any{"type": "distribution", "id": r.id, "domain": r.domain, "status": r.status, "enabled": r.enabled, "aliases": r.aliases})
		}
		c.emit(map[string]any{"type": "summary", "command": "cloudfront ls", "ok": true, "count": len(rows)})
		return nil
	}
	tw := tabwriter.NewWriter(c.io.Stdout, 2, 4, 2, ' ', 0)
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.id, r.domain, r.status, map[bool]string{true: "enabled", false: "disabled"}[r.enabled], strings.Join(r.aliases, ","))
	}
	return tw.Flush()
}

// ---- settings --------------------------------------------------------------------------------

type settingKey struct {
	name string
	get  func(app.Settings) any
	set  func(*app.Settings, string) error // nil: read-only
}

// setInt parses a whole number. Zero is refused unless zeroOK: the settings
// layer would silently replace it by the default.
func setInt(dst func(*app.Settings) *int, zeroOK ...bool) func(*app.Settings, string) error {
	return func(s *app.Settings, v string) error {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return usagef("%q is not a whole number", v)
		}
		if n == 0 && len(zeroOK) == 0 {
			return usagef("0 is not allowed here; use a positive number")
		}
		*dst(s) = n
		return nil
	}
}

func setBool(dst func(*app.Settings) *bool) func(*app.Settings, string) error {
	return func(s *app.Settings, v string) error {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "yes", "on", "1":
			*dst(s) = true
		case "false", "no", "off", "0":
			*dst(s) = false
		default:
			return usagef("%q is not on or off", v)
		}
		return nil
	}
}

var settingKeys = []settingKey{
	{"theme", func(s app.Settings) any { return s.Theme }, func(s *app.Settings, v string) error { s.Theme = v; return nil }},
	{"upload-limit", func(s app.Settings) any { return s.Limits.Upload }, setInt(func(s *app.Settings) *int { return &s.Limits.Upload })},
	{"download-limit", func(s app.Settings) any { return s.Limits.Download }, setInt(func(s *app.Settings) *int { return &s.Limits.Download })},
	{"other-limit", func(s app.Settings) any { return s.Limits.Other }, setInt(func(s *app.Settings) *int { return &s.Limits.Other })},
	{"bulk-workers", func(s app.Settings) any { return s.BulkWorkers }, setInt(func(s *app.Settings) *int { return &s.BulkWorkers })},
	{"part-size-mib", func(s app.Settings) any { return s.PartSizeMiB }, setInt(func(s *app.Settings) *int { return &s.PartSizeMiB })},
	{"upload-kibps", func(s app.Settings) any { return s.UploadKiBps }, setInt(func(s *app.Settings) *int { return &s.UploadKiBps }, true)},
	{"download-kibps", func(s app.Settings) any { return s.DownloadKiBps }, setInt(func(s *app.Settings) *int { return &s.DownloadKiBps }, true)},
	{"download-dir", func(s app.Settings) any { return s.DefaultDownloadDir }, func(s *app.Settings, v string) error {
		if v != "" {
			abs, err := filepath.Abs(v)
			if err != nil {
				return usagef("%v", err)
			}
			v = abs
		}
		s.DefaultDownloadDir = v
		return nil
	}},
	{"conflict", func(s app.Settings) any { return string(s.Conflict) }, func(s *app.Settings, v string) error {
		p, err := parseConflict(v)
		if err != nil {
			return err
		}
		if p == "" {
			return usagef("conflict must be skip, overwrite, rename, newer or different_size")
		}
		s.Conflict = p
		return nil
	}},
	{"show-versions", func(s app.Settings) any { return s.ShowVersions }, setBool(func(s *app.Settings) *bool { return &s.ShowVersions })},
	{"log-to-file", func(s app.Settings) any { return s.LogToFile }, setBool(func(s *app.Settings) *bool { return &s.LogToFile })},
	{"vault-auto-lock-minutes", func(s app.Settings) any { return s.VaultAutoLockMinutes }, setInt(func(s *app.Settings) *int { return &s.VaultAutoLockMinutes }, true)},
	{"hide-tray-icon", func(s app.Settings) any { return s.HideTrayIcon }, setBool(func(s *app.Settings) *bool { return &s.HideTrayIcon })},
	{"quit-when-idle", func(s app.Settings) any { return s.QuitWhenIdle }, setBool(func(s *app.Settings) *bool { return &s.QuitWhenIdle })},
	{"recent-ops", func(s app.Settings) any { return s.RecentOps }, setInt(func(s *app.Settings) *int { return &s.RecentOps })},
	{"keep-recent-ops", func(s app.Settings) any { return s.KeepRecentOps }, setBool(func(s *app.Settings) *bool { return &s.KeepRecentOps })},
	{"secret-backend", func(s app.Settings) any { return s.SecretBackend }, nil},
}

func findSetting(name string) (settingKey, error) {
	var names []string
	for _, k := range settingKeys {
		if k.name == name {
			return k, nil
		}
		names = append(names, k.name)
	}
	return settingKey{}, usagef("unknown setting %q; available: %s", name, strings.Join(names, ", "))
}

func (c *cli) printSetting(k settingKey, st app.Settings, valueOnly bool) {
	v := k.get(st)
	if c.g.json {
		c.emit(map[string]any{"type": "setting", "key": k.name, "value": v, "readOnly": k.set == nil})
		return
	}
	if valueOnly {
		fmt.Fprintln(c.io.Stdout, v)
		return
	}
	fmt.Fprintf(c.io.Stdout, "%-24s %v\n", k.name, v)
}

func setupSettings(c *cli, fs *flag.FlagSet) runner {
	return func(ctx context.Context, pos []string) error {
		if len(pos) == 0 || (pos[0] != "get" && pos[0] != "set") {
			return usagef("usage: pail settings get [KEY] | settings set KEY VALUE")
		}
		st := c.svc.Settings()
		if pos[0] == "get" {
			switch len(pos) {
			case 1:
				for _, k := range settingKeys {
					c.printSetting(k, st, false)
				}
			case 2:
				k, err := findSetting(pos[1])
				if err != nil {
					return err
				}
				c.printSetting(k, st, true)
			default:
				return usagef("settings get takes at most one KEY")
			}
			if c.g.json {
				c.emit(map[string]any{"type": "summary", "command": "settings get", "ok": true})
			}
			return nil
		}
		if len(pos) != 3 {
			return usagef("settings set takes KEY and VALUE")
		}
		k, err := findSetting(pos[1])
		if err != nil {
			return err
		}
		if k.set == nil {
			return usagef("%s cannot be set here; use pail vault init, vault backend or vault reset", k.name)
		}
		if err := k.set(&st, pos[2]); err != nil {
			return err
		}
		if err := c.svc.SaveSettings(st); err != nil {
			return err
		}
		now, _ := findSetting(k.name)
		return c.single("settings set", fmt.Sprintf("%s = %v", k.name, now.get(c.svc.Settings())),
			map[string]any{"key": k.name, "value": now.get(c.svc.Settings())})
	}
}
