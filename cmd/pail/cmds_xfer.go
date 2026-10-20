package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/DejavuMoe/Pail/internal/app"
)

func setupCp(c *cli, fs *flag.FlagSet) runner {
	rec := fs.Bool("r", false, "recursive (folders and prefixes)")
	fs.BoolVar(rec, "recursive", false, "same as -r")
	conflict := fs.String("conflict", "", "skip|overwrite|rename|newer|different_size (default: the desktop setting)")
	decode := fs.String("decode", "", "gzip|zip|auto: decode downloaded objects")
	return func(ctx context.Context, pos []string) error {
		if len(pos) != 2 {
			return usagef("cp takes SRC and DST")
		}
		pol, err := parseConflict(*conflict)
		if err != nil {
			return err
		}
		src, dst := pos[0], pos[1]
		switch {
		case !isS3(src) && isS3(dst):
			return c.upload(ctx, src, dst, *rec, pol, *decode)
		case isS3(src) && !isS3(dst):
			return c.download(ctx, src, dst, *rec, pol, app.DecodeMode(*decode))
		case isS3(src) && isS3(dst):
			if *decode != "" {
				return usagef("--decode applies to downloads only")
			}
			return c.s3copy(ctx, src, dst, *rec, pol, false)
		}
		return usagef("at least one of SRC and DST must be an s3:// path")
	}
}

func (c *cli) upload(ctx context.Context, src, dst string, rec bool, pol app.ConflictPolicy, decode string) error {
	if decode != "" {
		return usagef("--decode applies to downloads only")
	}
	fi, err := os.Stat(src)
	if err != nil {
		return usagef("%v", err)
	}
	if fi.IsDir() && !rec {
		return usagef("%s is a directory; add -r to upload it", src)
	}
	d, err := parseS3(dst)
	if err != nil {
		return err
	}
	w := c.watch()
	defer w.close()
	id, err := c.svc.Upload(app.UploadRequest{Dest: c.loc(d.Bucket, folderPrefix(d.Key)), Paths: []string{src}, Conflict: pol})
	if err != nil {
		return err
	}
	return c.finish(w.wait(ctx, id), "cp", ctx)
}

func (c *cli) download(ctx context.Context, src, dst string, rec bool, pol app.ConflictPolicy, dec app.DecodeMode) error {
	switch dec {
	case app.DecodeRaw, app.DecodeGzip, app.DecodeZip, app.DecodeAuto:
	default:
		return usagef("--decode must be gzip, zip or auto, not %q", string(dec))
	}
	p, err := parseS3(src)
	if err != nil {
		return err
	}
	sel, err := c.selectionFor(p, rec)
	if err != nil {
		return err
	}
	if fi, err := os.Stat(dst); err == nil && !fi.IsDir() {
		return usagef("%s exists and is not a directory (the destination of a download is always a directory)", dst)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	req := app.DownloadRequest{Source: sel.Location, Keys: sel.Keys, WholeBucket: sel.All, DestDir: dst, Conflict: pol, Decode: dec}
	w := c.watch()
	defer w.close()
	id, err := c.svc.Download(req)
	if err != nil {
		return err
	}
	return c.finish(w.wait(ctx, id), "cp", ctx)
}

func (c *cli) s3copy(ctx context.Context, src, dst string, rec bool, pol app.ConflictPolicy, move bool) error {
	p, err := parseS3(src)
	if err != nil {
		return err
	}
	d, err := parseS3(dst)
	if err != nil {
		return err
	}
	sel, err := c.selectionFor(p, rec)
	if err != nil {
		return err
	}
	if pol == "" {
		pol = c.svc.Settings().Conflict // same default as upload and download
	}
	dest := c.loc(d.Bucket, folderPrefix(d.Key))
	verb := "cp"
	if move {
		verb = "mv"
		est, err := c.svc.Estimate(ctx, sel)
		if err != nil {
			return err
		}
		if est.Objects == 0 {
			return &exitErr{exitFailure, "nothing to move: no object matches " + p.String()}
		}
		if err := c.confirm([]string{
			"Move:        " + p.String(),
			"Destination: " + locURL(dest),
			"Scope:       " + fmtEstimate(est),
			"The sources are deleted after each copy is verified.",
		}); err != nil {
			return err
		}
	}
	w := c.watch()
	defer w.close()
	id, err := c.svc.Copy(sel, dest, app.CopyOptions{Conflict: pol, Move: move})
	if err != nil {
		return err
	}
	return c.finish(w.wait(ctx, id), verb, ctx)
}

func setupMv(c *cli, fs *flag.FlagSet) runner {
	rec := fs.Bool("r", false, "recursive (folders and prefixes)")
	fs.BoolVar(rec, "recursive", false, "same as -r")
	conflict := fs.String("conflict", "", "skip|overwrite|rename|newer|different_size (default: the desktop setting)")
	return func(ctx context.Context, pos []string) error {
		if len(pos) != 2 {
			return usagef("mv takes SRC and DST")
		}
		if !isS3(pos[0]) || !isS3(pos[1]) {
			return usagef("mv works between s3:// paths only")
		}
		pol, err := parseConflict(*conflict)
		if err != nil {
			return err
		}
		return c.s3copy(ctx, pos[0], pos[1], *rec, pol, true)
	}
}

func setupRm(c *cli, fs *flag.FlagSet) runner {
	rec := fs.Bool("r", false, "recursive: delete a prefix and everything below it")
	fs.BoolVar(rec, "recursive", false, "same as -r")
	versions := fs.Bool("versions", false, "permanently delete all versions (and delete markers) of one key")
	return func(ctx context.Context, pos []string) error {
		if len(pos) != 1 {
			return usagef("rm takes one s3:// path")
		}
		p, err := parseS3(pos[0])
		if err != nil {
			return err
		}
		if *versions {
			return c.rmVersions(ctx, p, *rec)
		}
		sel, err := c.selectionFor(p, *rec)
		if err != nil {
			return err
		}
		est, err := c.svc.Estimate(ctx, sel)
		if err != nil {
			return err
		}
		if est.Objects == 0 && !est.Truncated {
			c.infof("nothing to delete: no object matches %s", p)
			if c.g.json {
				c.emit(map[string]any{"type": "summary", "command": "rm", "ok": true, "succeeded": 0})
			}
			return nil
		}
		if err := c.confirm([]string{
			"Delete:  " + p.String() + map[bool]string{true: " (recursive)", false: ""}[*rec],
			"Scope:   " + fmtEstimate(est),
		}); err != nil {
			return err
		}
		w := c.watch()
		defer w.close()
		id, err := c.svc.Delete(sel)
		if err != nil {
			return err
		}
		return c.finish(w.wait(ctx, id), "rm", ctx)
	}
}

func (c *cli) rmVersions(ctx context.Context, p s3Path, rec bool) error {
	if rec || p.Key == "" || strings.HasSuffix(p.Key, "/") {
		return usagef("--versions works on a single object key (not with -r or a prefix)")
	}
	vs, err := c.listVersions(ctx, c.loc(p.Bucket, parentPrefix(p.Key)), p.Key)
	if err != nil {
		return err
	}
	if len(vs) == 0 {
		c.infof("nothing to delete: no versions of %s", p)
		return nil
	}
	refs := make([]app.ObjectRef, len(vs))
	markers := 0
	for i, v := range vs {
		refs[i] = app.ObjectRef{Key: v.Key, VersionID: v.VersionID}
		if v.DeleteMarker {
			markers++
		}
	}
	if err := c.confirm([]string{
		"Delete ALL versions of: " + p.String(),
		fmt.Sprintf("Scope:   %d versions (%d delete markers)", len(vs), markers),
		"This is permanent: the history of the key is destroyed.",
	}); err != nil {
		return err
	}
	w := c.watch()
	defer w.close()
	id, err := c.svc.DeleteVersions(c.loc(p.Bucket, parentPrefix(p.Key)), refs)
	if err != nil {
		return err
	}
	return c.finish(w.wait(ctx, id), "rm", ctx)
}

func (c *cli) single(what, text string, kv map[string]any) error {
	if c.g.json {
		m := map[string]any{"type": "summary", "command": what, "ok": true}
		for k, v := range kv {
			m[k] = v
		}
		c.emit(m)
		return nil
	}
	fmt.Fprintln(c.io.Stdout, text)
	return nil
}

func setupMb(c *cli, fs *flag.FlagSet) runner {
	region := fs.String("region", "", "bucket region (default: the account's)")
	lock := fs.Bool("object-lock", false, "enable object lock (implies versioning)")
	return func(ctx context.Context, pos []string) error {
		if len(pos) != 1 {
			return usagef("mb takes one s3://bucket")
		}
		p, err := parseS3(pos[0])
		if err != nil {
			return err
		}
		if p.Key != "" {
			return usagef("mb takes a bucket only, not a key")
		}
		if err := c.svc.CreateBucket(ctx, c.acct.ID, p.Bucket, *region, *lock); err != nil {
			return err
		}
		return c.single("mb", "made bucket s3://"+p.Bucket, map[string]any{"bucket": p.Bucket})
	}
}

func setupRb(c *cli, fs *flag.FlagSet) runner {
	return func(ctx context.Context, pos []string) error {
		if len(pos) != 1 {
			return usagef("rb takes one s3://bucket")
		}
		p, err := parseS3(pos[0])
		if err != nil {
			return err
		}
		if p.Key != "" {
			return usagef("rb takes a bucket only, not a key")
		}
		est, err := c.svc.Estimate(ctx, app.Selection{Location: c.loc(p.Bucket, ""), All: true})
		if err != nil {
			return err
		}
		if err := c.confirm([]string{
			"Delete bucket: s3://" + p.Bucket,
			"Contents:      " + fmtEstimate(est) + " (the bucket must be empty or the request is refused)",
		}); err != nil {
			return err
		}
		if err := c.svc.DeleteBucket(ctx, c.acct.ID, p.Bucket); err != nil {
			return err
		}
		return c.single("rb", "removed bucket s3://"+p.Bucket, map[string]any{"bucket": p.Bucket})
	}
}

func setupRestore(c *cli, fs *flag.FlagSet) runner {
	vid := fs.String("version-id", "", "restore this version of the key as the current one")
	deleted := fs.Bool("deleted", false, "restore keys whose latest entry is a delete marker")
	rec := fs.Bool("r", false, "recursive (with --deleted on a prefix)")
	fs.BoolVar(rec, "recursive", false, "same as -r")
	return func(ctx context.Context, pos []string) error {
		if len(pos) != 1 {
			return usagef("restore takes one s3:// path")
		}
		p, err := parseS3(pos[0])
		if err != nil {
			return err
		}
		switch {
		case *vid != "" && *deleted:
			return usagef("--version-id and --deleted are mutually exclusive")
		case *vid != "":
			if p.Key == "" || strings.HasSuffix(p.Key, "/") {
				return usagef("--version-id needs an object key")
			}
			if err := c.svc.RestoreVersion(ctx, c.loc(p.Bucket, ""), app.ObjectRef{Key: p.Key, VersionID: *vid}); err != nil {
				return err
			}
			return c.single("restore", "restored "+p.String()+" from version "+*vid, map[string]any{"key": p.Key, "versionId": *vid})
		case *deleted:
			sel, err := c.selectionFor(p, *rec)
			if err != nil {
				return err
			}
			w := c.watch()
			defer w.close()
			id, err := c.svc.RestoreDeleted(sel)
			if err != nil {
				return err
			}
			return c.finish(w.wait(ctx, id), "restore", ctx)
		}
		return usagef("restore needs --version-id V or --deleted")
	}
}

func locURL(l app.Location) string { return "s3://" + l.Bucket + "/" + l.Prefix }
