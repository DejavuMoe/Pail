package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DejavuMoe/Pail/internal/app"
)

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func setupSync(c *cli, fs *flag.FlagSet) runner {
	mode := fs.String("mode", "", "new|new_changed|mirror (default new_changed; mirror also deletes)")
	compare := fs.String("compare", "", "size_time|size|checksum (default size_time)")
	var excludes multiFlag
	fs.Var(&excludes, "exclude", "glob on the relative path to leave out (repeatable)")
	dry := fs.Bool("dry-run", false, "print the plan and exit without changing anything")
	jobName := fs.String("job", "", "run a sync job saved in the desktop app")
	noRules := fs.Bool("no-rules", false, "do not apply upload rules")
	return func(ctx context.Context, pos []string) error {
		job, err := c.syncJob(pos, *jobName, *mode, *compare, excludes, *noRules, *dry)
		if err != nil {
			return err
		}
		pv, err := c.svc.PreviewSync(ctx, job)
		if err != nil {
			return err
		}
		c.printPreview(pv, *dry)
		if *dry {
			return nil
		}
		if pv.Uploads+pv.Downloads+pv.Deletes == 0 {
			c.infof("sync: already up to date (%d skipped)", pv.Skips)
			if c.g.json {
				c.emit(map[string]any{"type": "summary", "command": "sync", "ok": true, "succeeded": 0, "skipped": pv.Skips})
			}
			return nil
		}
		if pv.Deletes > 0 {
			where := "objects under " + locURL(job.Remote) + " are deleted"
			if job.Direction == "download" {
				where = "local files under " + job.LocalDir + " are moved to the trash"
			}
			if err := c.confirm([]string{
				fmt.Sprintf("Sync (%s, mode %s): %s <-> %s", job.Direction, job.Mode, job.LocalDir, locURL(job.Remote)),
				fmt.Sprintf("Plan:   %d uploads, %d downloads, %d deletes, %s to transfer", pv.Uploads, pv.Downloads, pv.Deletes, humanSize(pv.Bytes)),
				fmt.Sprintf("DELETES: %d %s", pv.Deletes, where),
			}); err != nil {
				return err
			}
		}
		w := c.watch()
		defer w.close()
		id, err := c.svc.RunSync(pv)
		if err != nil {
			return err
		}
		return c.finish(w.wait(ctx, id), "sync", ctx)
	}
}

func (c *cli) syncJob(pos []string, jobName, mode, compare string, excludes []string, noRules, dry bool) (app.SyncJob, error) {
	if jobName != "" {
		if len(pos) != 0 {
			return app.SyncJob{}, usagef("--job takes no paths")
		}
		if mode != "" || compare != "" || len(excludes) > 0 {
			return app.SyncJob{}, usagef("--job uses the saved settings; --mode, --compare and --exclude cannot be combined with it")
		}
		for _, j := range c.svc.SyncJobs() {
			if strings.EqualFold(j.Name, jobName) || j.ID == jobName {
				if j.Remote.AccountID != c.acct.ID && c.g.account == "" {
					// The job names its own account; follow it unless --account was given.
					for _, a := range c.svc.Accounts() {
						if a.ID == j.Remote.AccountID {
							a := a
							c.acct = &a
						}
					}
				}
				if j.Remote.AccountID != c.acct.ID {
					return app.SyncJob{}, usagef("job %q belongs to another account", j.Name)
				}
				return j, nil
			}
		}
		return app.SyncJob{}, usagef("no sync job named %q", jobName)
	}
	if len(pos) != 2 {
		return app.SyncJob{}, usagef("sync takes LOCAL s3://bucket/prefix/ (upload) or s3://bucket/prefix/ LOCAL (download), or --job NAME")
	}
	var local, remote string
	dir := "upload"
	switch {
	case isS3(pos[1]) && !isS3(pos[0]):
		local, remote = pos[0], pos[1]
	case isS3(pos[0]) && !isS3(pos[1]):
		local, remote, dir = pos[1], pos[0], "download"
	default:
		return app.SyncJob{}, usagef("sync needs exactly one local path and one s3:// path")
	}
	p, err := parseS3(remote)
	if err != nil {
		return app.SyncJob{}, err
	}
	abs, err := filepath.Abs(local)
	if err != nil {
		return app.SyncJob{}, usagef("%v", err)
	}
	if _, err := os.Stat(abs); err != nil {
		if dir == "download" && !dry && os.IsNotExist(err) {
			if err := os.MkdirAll(abs, 0o755); err != nil {
				return app.SyncJob{}, err
			}
		} else {
			return app.SyncJob{}, usagef("%v", err)
		}
	}
	return app.SyncJob{
		Name: "cli", LocalDir: abs, Remote: c.loc(p.Bucket, folderPrefix(p.Key)), Direction: dir,
		Mode: mode, Compare: compare, Excludes: excludes, NoRules: noRules,
	}, nil
}

func (c *cli) printPreview(pv app.SyncPreview, dry bool) {
	if c.g.json {
		for _, a := range pv.Actions {
			c.emit(map[string]any{"type": "action", "op": a.Op, "path": a.Path, "reason": a.Reason, "size": a.Size})
		}
		if dry {
			c.emit(map[string]any{"type": "summary", "command": "sync", "ok": true, "dryRun": true,
				"uploads": pv.Uploads, "downloads": pv.Downloads, "deletes": pv.Deletes, "skips": pv.Skips, "bytes": pv.Bytes})
		}
		return
	}
	out := c.io.Stdout
	if !dry {
		out = c.io.Stderr
		if c.g.quiet {
			return
		}
	}
	for _, a := range pv.Actions {
		if a.Op == "skip" {
			continue
		}
		fmt.Fprintf(out, "%-8s %s  (%s)  %s\n", a.Op, a.Path, humanSize(a.Size), a.Reason)
	}
	fmt.Fprintf(out, "plan: %d uploads, %d downloads, %d deletes, %d skipped, %s to transfer\n",
		pv.Uploads, pv.Downloads, pv.Deletes, pv.Skips, humanSize(pv.Bytes))
	if dry {
		fmt.Fprintln(out, "dry run: nothing was changed")
	}
}
