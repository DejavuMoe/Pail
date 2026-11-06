package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/DejavuMoe/Pail/internal/app"
)

// objectPath parses an s3:// argument that must name one object.
func objectPath(arg, cmd string) (s3Path, error) {
	p, err := parseS3(arg)
	if err != nil {
		return p, err
	}
	if p.Key == "" || strings.HasSuffix(p.Key, "/") {
		return p, usagef("%s needs an object key, not a prefix", cmd)
	}
	return p, nil
}

func sortedKeys(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func exactSize(n int64) string {
	if n < 1024 {
		return humanSize(n)
	}
	return fmt.Sprintf("%s (%d bytes)", humanSize(n), n)
}

// ---- stat -------------------------------------------------------------------------

func setupStat(c *cli, fs *flag.FlagSet) runner {
	vid := fs.String("version-id", "", "object version")
	return func(ctx context.Context, pos []string) error {
		if len(pos) != 1 {
			return usagef("stat takes one s3://bucket/key path")
		}
		p, err := objectPath(pos[0], "stat")
		if err != nil {
			return err
		}
		return c.stat(ctx, p, *vid, "stat")
	}
}

func (c *cli) stat(ctx context.Context, p s3Path, versionID, command string) error {
	info, err := c.svc.Head(ctx, c.loc(p.Bucket, ""), p.Key, versionID)
	if err != nil {
		return err
	}
	if info.TagsErr != nil {
		c.infof("tags are not available: %v", info.TagsErr)
	}
	if c.g.json {
		hdr := map[string]string{}
		for _, h := range info.Headers {
			if h.Value != "" {
				hdr[h.Name] = h.Value
			}
		}
		o := map[string]any{"type": "object", "key": info.Key, "versionId": info.VersionID, "size": info.Size,
			"modified": info.Modified, "etag": info.ETag, "storageClass": info.StorageClass, "contentType": info.ContentType,
			"headers": hdr, "metadata": info.Metadata, "tags": info.Tags, "sse": info.SSE, "kmsKeyId": info.KMSKeyID,
			"restore": info.Restore, "lockMode": info.LockMode, "legalHold": info.LegalHold}
		if !info.RetainUntil.IsZero() {
			o["retainUntil"] = info.RetainUntil
		}
		c.emit(o)
		c.emit(map[string]any{"type": "summary", "command": command, "ok": true})
		return nil
	}
	tw := tabwriter.NewWriter(c.io.Stdout, 2, 4, 2, ' ', 0)
	row := func(k, v string) { fmt.Fprintf(tw, "%s:\t%s\n", k, v) }
	opt := func(k, v string) {
		if v != "" {
			row(k, v)
		}
	}
	row("key", info.Key)
	opt("version", info.VersionID)
	row("size", exactSize(info.Size))
	row("modified", fmtTime(info.Modified))
	opt("etag", info.ETag)
	opt("content type", info.ContentType)
	row("storage class", orDash(info.StorageClass))
	for _, h := range info.Headers {
		if h.Name != "Content-Type" {
			opt(strings.ToLower(h.Name), h.Value)
		}
	}
	for _, k := range sortedKeys(info.Metadata) {
		row("metadata "+k, info.Metadata[k])
	}
	for _, k := range sortedKeys(info.Tags) {
		row("tag "+k, info.Tags[k])
	}
	opt("encryption", info.SSE)
	opt("KMS key", info.KMSKeyID)
	opt("restore", info.Restore)
	opt("lock mode", info.LockMode)
	if !info.RetainUntil.IsZero() {
		row("retain until", fmtTime(info.RetainUntil))
	}
	if info.LegalHold {
		row("legal hold", "on")
	}
	opt("read-only", info.ReadOnlyReason)
	return tw.Flush()
}

// ---- cat --------------------------------------------------------------------------

func setupCat(c *cli, fs *flag.FlagSet) runner {
	vid := fs.String("version-id", "", "object version")
	return func(ctx context.Context, pos []string) error {
		if len(pos) != 1 {
			return usagef("cat takes one s3://bucket/key path")
		}
		if c.g.json {
			return usagef("cat writes the raw object to stdout; --json does not apply")
		}
		p, err := objectPath(pos[0], "cat")
		if err != nil {
			return err
		}
		rc, _, err := c.svc.OpenObject(ctx, c.loc(p.Bucket, ""), p.Key, *vid)
		if err != nil {
			return err
		}
		defer rc.Close()
		if _, err := io.Copy(c.io.Stdout, rc); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("reading %s: %w", p, err)
		}
		return nil
	}
}

// ---- mkdir, du ------------------------------------------------------------------------

func setupMkdir(c *cli, fs *flag.FlagSet) runner {
	return func(ctx context.Context, pos []string) error {
		if len(pos) != 1 {
			return usagef("mkdir takes one s3://bucket/prefix/ path")
		}
		p, err := parseS3(pos[0])
		if err != nil {
			return err
		}
		if p.Key == "" || strings.Trim(p.Key, "/") == "" {
			return usagef("mkdir needs a folder name below the bucket")
		}
		key := folderPrefix(strings.TrimRight(p.Key, "/"))
		name := strings.TrimSuffix(key[len(parentPrefix(key)):], "/")
		if err := c.svc.CreateFolder(ctx, c.loc(p.Bucket, parentPrefix(key)), name); err != nil {
			return err
		}
		return c.single("mkdir", "created s3://"+p.Bucket+"/"+key, map[string]any{"bucket": p.Bucket, "key": key})
	}
}

func setupDu(c *cli, fs *flag.FlagSet) runner {
	return func(ctx context.Context, pos []string) error {
		if len(pos) != 1 {
			return usagef("du takes one s3://bucket/prefix/ path")
		}
		p, err := parseS3(pos[0])
		if err != nil {
			return err
		}
		prefix := folderPrefix(p.Key)
		live := c.io.StderrTTY && !c.g.quiet && !c.g.json
		est, err := c.svc.FolderSize(ctx, c.loc(p.Bucket, prefix), func(e app.Estimate) {
			if live {
				fmt.Fprintf(c.io.Stderr, "\r\x1b[Kcounting: %d objects, %s", e.Objects, humanSize(e.Bytes))
			}
		})
		if live {
			fmt.Fprint(c.io.Stderr, "\r\x1b[K")
		}
		if err != nil {
			return err
		}
		path := "s3://" + p.Bucket + "/" + prefix
		if c.g.json {
			c.emit(map[string]any{"type": "du", "path": path, "objects": est.Objects, "bytes": est.Bytes})
			c.emit(map[string]any{"type": "summary", "command": "du", "ok": true})
			return nil
		}
		fmt.Fprintf(c.io.Stdout, "%10s  %d objects  %s\n", humanSize(est.Bytes), est.Objects, path)
		return nil
	}
}

// ---- tags -----------------------------------------------------------------------------

func parseKV(args []string, what string) (map[string]string, error) {
	m := map[string]string{}
	for _, a := range args {
		k, v, ok := strings.Cut(a, "=")
		if !ok || k == "" {
			return nil, usagef("%s must be written KEY=VALUE, not %q", what, a)
		}
		if _, dup := m[k]; dup {
			return nil, usagef("%s %q is given twice", what, k)
		}
		m[k] = v
	}
	return m, nil
}

func setupTags(c *cli, fs *flag.FlagSet) runner {
	vid := fs.String("version-id", "", "object version")
	merge := fs.Bool("merge", false, "set: keep the existing tags and add or replace the given ones")
	clear := fs.Bool("clear", false, "set: remove all tags")
	return func(ctx context.Context, pos []string) error {
		if len(pos) < 2 {
			return usagef("usage: pail tags get s3://bucket/key | tags set s3://bucket/key K=V... | tags set s3://bucket/key --clear")
		}
		sub := pos[0]
		p, err := objectPath(pos[1], "tags")
		if err != nil {
			return err
		}
		loc := c.loc(p.Bucket, "")
		switch sub {
		case "get":
			if len(pos) != 2 {
				return usagef("tags get takes one s3://bucket/key path")
			}
			if err := onlyFlags(fs, "tags get", []string{"version-id"}); err != nil {
				return err
			}
			info, err := c.svc.Head(ctx, loc, p.Key, *vid)
			if err != nil {
				return err
			}
			if info.TagsErr != nil {
				return info.TagsErr
			}
			for _, k := range sortedKeys(info.Tags) {
				if c.g.json {
					c.emit(map[string]any{"type": "tag", "key": k, "value": info.Tags[k]})
				} else {
					fmt.Fprintf(c.io.Stdout, "%s=%s\n", k, info.Tags[k])
				}
			}
			if c.g.json {
				c.emit(map[string]any{"type": "summary", "command": "tags get", "ok": true, "count": len(info.Tags)})
			} else if len(info.Tags) == 0 {
				c.infof("no tags")
			}
			return nil
		case "set":
			if err := onlyFlags(fs, "tags set", []string{"version-id", "merge", "clear"}); err != nil {
				return err
			}
			tags, err := parseKV(pos[2:], "a tag")
			if err != nil {
				return err
			}
			switch {
			case *clear && (len(tags) > 0 || *merge):
				return usagef("--clear takes no tags and cannot be combined with --merge")
			case !*clear && len(tags) == 0:
				return usagef("tags set needs at least one KEY=VALUE, or --clear to remove all tags")
			}
			if *clear {
				if err := c.confirm([]string{"Remove all tags of " + p.String()}); err != nil {
					return err
				}
			}
			if *merge {
				info, err := c.svc.Head(ctx, loc, p.Key, *vid)
				if err != nil {
					return err
				}
				if info.TagsErr != nil {
					return info.TagsErr
				}
				for k, v := range tags {
					info.Tags[k] = v
				}
				tags = info.Tags
			}
			if err := c.svc.SetTags(ctx, loc, app.ObjectRef{Key: p.Key, VersionID: *vid}, tags); err != nil {
				return err
			}
			return c.single("tags set", fmt.Sprintf("%s now has %d tag(s)", p, len(tags)), map[string]any{"key": p.Key, "tags": len(tags)})
		}
		return usagef("unknown tags subcommand %q (get, set)", sub)
	}
}

// ---- acl ------------------------------------------------------------------------------

func setupACL(c *cli, fs *flag.FlagSet) runner {
	vid := fs.String("version-id", "", "object version")
	return func(ctx context.Context, pos []string) error {
		if len(pos) < 2 {
			return usagef("usage: pail acl get s3://bucket/key | acl set s3://bucket/key CANNED")
		}
		sub := pos[0]
		p, err := objectPath(pos[1], "acl")
		if err != nil {
			return err
		}
		loc := c.loc(p.Bucket, "")
		ref := app.ObjectRef{Key: p.Key, VersionID: *vid}
		switch sub {
		case "get":
			if len(pos) != 2 {
				return usagef("acl get takes one s3://bucket/key path")
			}
			acl, err := c.svc.ACL(ctx, loc, ref)
			if err != nil {
				return err
			}
			if c.g.json {
				grants := make([]map[string]string, 0, len(acl.Grants))
				for _, g := range acl.Grants {
					grants = append(grants, map[string]string{"type": g.Type, "grantee": g.Grantee, "permission": g.Permission})
				}
				c.emit(map[string]any{"type": "acl", "owner": acl.Owner, "grants": grants})
				c.emit(map[string]any{"type": "summary", "command": "acl get", "ok": true})
				return nil
			}
			fmt.Fprintf(c.io.Stdout, "owner: %s\n", orDash(acl.Owner))
			tw := tabwriter.NewWriter(c.io.Stdout, 2, 4, 2, ' ', 0)
			for _, g := range acl.Grants {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", g.Permission, g.Type, g.Grantee)
			}
			return tw.Flush()
		case "set":
			if len(pos) != 3 {
				return usagef("acl set takes s3://bucket/key and a canned ACL (private, public-read, public-read-write, authenticated-read, aws-exec-read, bucket-owner-read, bucket-owner-full-control)")
			}
			if err := c.svc.SetACL(ctx, loc, ref, app.ACL{Canned: pos[2]}); err != nil {
				return err
			}
			return c.single("acl set", fmt.Sprintf("%s is now %s", p, pos[2]), map[string]any{"key": p.Key, "acl": pos[2]})
		}
		return usagef("unknown acl subcommand %q (get, set)", sub)
	}
}

// ---- props, storage-class ------------------------------------------------------------------

func setupProps(c *cli, fs *flag.FlagSet) runner {
	hdr := map[string]*string{}
	for _, h := range []struct{ flag, desc string }{
		{"content-type", "Content-Type"}, {"content-encoding", "Content-Encoding"}, {"content-language", "Content-Language"},
		{"content-disposition", "Content-Disposition"}, {"cache-control", "Cache-Control"}, {"expires", "Expires (an HTTP date)"},
	} {
		hdr[h.flag] = fs.String(h.flag, "", "set "+h.desc+" (an empty value removes it)")
	}
	var meta, delHeaders, delMeta multiFlag
	fs.Var(&meta, "metadata", "set user metadata KEY=VALUE, keys in lower case (repeatable)")
	fs.Var(&delHeaders, "delete-header", "remove a standard header by name (repeatable)")
	fs.Var(&delMeta, "delete-metadata", "remove user metadata KEY (repeatable)")
	rec := fs.Bool("r", false, "recursive: change every object below the prefix")
	fs.BoolVar(rec, "recursive", false, "same as -r")
	propFlags := []string{"content-type", "content-encoding", "content-language", "content-disposition", "cache-control",
		"expires", "metadata", "delete-header", "delete-metadata", "r", "recursive"}
	return func(ctx context.Context, pos []string) error {
		if len(pos) != 2 {
			return usagef("usage: pail props get s3://bucket/key | props set s3://bucket/key [flags]")
		}
		switch pos[0] {
		case "get":
			if err := onlyFlags(fs, "props get"); err != nil {
				return err
			}
			p, err := objectPath(pos[1], "props")
			if err != nil {
				return err
			}
			return c.stat(ctx, p, "", "props get")
		case "set":
		default:
			return usagef("unknown props subcommand %q (get, set)", pos[0])
		}
		if err := onlyFlags(fs, "props set", propFlags); err != nil {
			return err
		}
		p, err := parseS3(pos[1])
		if err != nil {
			return err
		}
		patch := app.PropertiesPatch{SetHeaders: map[string]string{}, DelHeaders: append([]string(nil), delHeaders...),
			DelMetadata: append([]string(nil), delMeta...)}
		fs.Visit(func(f *flag.Flag) {
			if v, ok := hdr[f.Name]; ok {
				if *v == "" {
					patch.DelHeaders = append(patch.DelHeaders, f.Name)
				} else {
					patch.SetHeaders[f.Name] = *v
				}
			}
		})
		if patch.SetMetadata, err = parseKV(meta, "metadata"); err != nil {
			return err
		}
		if len(patch.SetHeaders)+len(patch.DelHeaders)+len(patch.SetMetadata)+len(patch.DelMetadata) == 0 {
			return usagef("props set needs at least one property flag")
		}
		return c.rewrite(ctx, p, *rec, "props", "Change properties of", func(sel app.Selection) (string, error) {
			return c.svc.SetProperties(sel, patch)
		})
	}
}

func setupStorageClass(c *cli, fs *flag.FlagSet) runner {
	rec := fs.Bool("r", false, "recursive: change every object below the prefix")
	fs.BoolVar(rec, "recursive", false, "same as -r")
	return func(ctx context.Context, pos []string) error {
		if len(pos) != 2 {
			return usagef("storage-class takes s3://bucket/key and a class such as STANDARD_IA, GLACIER_IR or INTELLIGENT_TIERING")
		}
		p, err := parseS3(pos[0])
		if err != nil {
			return err
		}
		class := strings.ToUpper(strings.TrimSpace(pos[1]))
		return c.rewrite(ctx, p, *rec, "storage-class", "Set storage class "+class+" on", func(sel app.Selection) (string, error) {
			return c.svc.SetStorageClass(sel, class)
		})
	}
}

// rewrite runs an in-place rewrite of one object, or with rec of a prefix
// after confirmation, and reports the per-object results.
func (c *cli) rewrite(ctx context.Context, p s3Path, rec bool, command, verb string, start func(app.Selection) (string, error)) error {
	sel, err := c.selectionFor(p, rec)
	if err != nil {
		return err
	}
	if rec {
		est, err := c.svc.Estimate(ctx, sel)
		if err != nil {
			return err
		}
		if err := c.confirm([]string{
			verb + ": " + p.String() + " (recursive)",
			"Scope:   " + fmtEstimate(est),
			"Objects are rewritten in place; a versioned bucket keeps the old versions.",
		}); err != nil {
			return err
		}
	}
	w := c.watch()
	defer w.close()
	id, err := start(sel)
	if err != nil {
		return err
	}
	return c.finish(w.wait(ctx, id), command, ctx)
}
