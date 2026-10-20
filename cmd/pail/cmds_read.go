package main

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/DejavuMoe/Pail/internal/app"
)

func setupAccounts(c *cli, fs *flag.FlagSet) runner {
	return func(ctx context.Context, pos []string) error {
		if len(pos) != 0 {
			return usagef("accounts takes no arguments")
		}
		accts := c.svc.Accounts()
		if c.g.json {
			for _, a := range accts {
				c.emit(map[string]any{"type": "account", "id": a.ID, "name": a.Name, "provider": a.Provider,
					"endpoint": a.Endpoint, "region": a.Region, "credentials": a.Credentials})
			}
			c.emit(map[string]any{"type": "summary", "command": "accounts", "ok": true, "count": len(accts)})
			return nil
		}
		if len(accts) == 0 {
			c.infof("no accounts configured; add one in the Pail desktop app")
			return nil
		}
		tw := tabwriter.NewWriter(c.io.Stdout, 2, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tID\tPROVIDER\tENDPOINT\tREGION")
		for _, a := range accts {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", a.Name, a.ID, a.Provider, orDash(a.Endpoint), orDash(a.Region))
		}
		return tw.Flush()
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func setupBuckets(c *cli, fs *flag.FlagSet) runner {
	return func(ctx context.Context, pos []string) error {
		if len(pos) != 0 {
			return usagef("buckets takes no arguments")
		}
		bs, err := c.svc.Buckets(ctx, c.acct.ID)
		if err != nil {
			return err
		}
		c.printBuckets(bs)
		return nil
	}
}

func (c *cli) printBuckets(bs []app.Bucket) {
	if c.g.json {
		for _, b := range bs {
			c.emit(map[string]any{"type": "bucket", "name": b.Name, "region": b.Region, "created": b.Created})
		}
		c.emit(map[string]any{"type": "summary", "command": "buckets", "ok": true, "count": len(bs)})
		return
	}
	for _, b := range bs {
		fmt.Fprintf(c.io.Stdout, "%s  %-14s %s\n", fmtTime(b.Created), orDash(b.Region), b.Name)
	}
}

func setupLs(c *cli, fs *flag.FlagSet) runner {
	rec := fs.Bool("r", false, "recursive: list every key below the prefix")
	fs.BoolVar(rec, "recursive", false, "same as -r")
	return func(ctx context.Context, pos []string) error {
		if len(pos) == 0 {
			bs, err := c.svc.Buckets(ctx, c.acct.ID)
			if err != nil {
				return err
			}
			c.printBuckets(bs)
			return nil
		}
		if len(pos) != 1 {
			return usagef("ls takes one s3:// path")
		}
		p, err := parseS3(pos[0])
		if err != nil {
			return err
		}
		prefix := folderPrefix(p.Key)
		var objs, folders int
		var bytes int64
		list := func(prefix string) error {
			tok := ""
			for {
				page, err := c.svc.List(ctx, c.loc(p.Bucket, prefix), app.ListOptions{Flat: *rec, Token: tok})
				if err != nil {
					return err
				}
				for _, e := range page.Entries {
					rel := strings.TrimPrefix(e.Key, prefix)
					if e.Folder {
						folders++
						if c.g.json {
							c.emit(map[string]any{"type": "folder", "key": e.Key})
						} else {
							fmt.Fprintf(c.io.Stdout, "%s  PRE %s\n", strings.Repeat(" ", 30), rel)
						}
						continue
					}
					objs++
					bytes += e.Size
					if c.g.json {
						c.emit(map[string]any{"type": "object", "key": e.Key, "size": e.Size, "modified": e.Modified,
							"storageClass": e.StorageClass, "etag": e.ETag})
					} else {
						fmt.Fprintf(c.io.Stdout, "%s %12s  %s\n", fmtTime(e.Modified), humanSize(e.Size), rel)
					}
				}
				if page.NextToken == "" {
					return nil
				}
				tok = page.NextToken
			}
		}
		if err := list(prefix); err != nil {
			return err
		}
		if objs+folders == 0 && p.Key != "" && !strings.HasSuffix(p.Key, "/") {
			// Not a folder: maybe a single object.
			info, err := c.svc.Head(ctx, c.loc(p.Bucket, ""), p.Key, "")
			if err == nil {
				objs++
				bytes += info.Size
				if c.g.json {
					c.emit(map[string]any{"type": "object", "key": info.Key, "size": info.Size, "modified": info.Modified,
						"storageClass": info.StorageClass, "etag": info.ETag})
				} else {
					fmt.Fprintf(c.io.Stdout, "%s %12s  %s\n", fmtTime(info.Modified), humanSize(info.Size), info.Key)
				}
			} else if !app.IsKind(err, app.ErrNotFound) {
				return err
			}
		}
		if c.g.json {
			c.emit(map[string]any{"type": "summary", "command": "ls", "ok": true, "objects": objs, "folders": folders, "bytes": bytes})
		} else {
			c.infof("%d objects, %d folders, %s", objs, folders, humanSize(bytes))
		}
		return nil
	}
}

func setupVersions(c *cli, fs *flag.FlagSet) runner {
	return func(ctx context.Context, pos []string) error {
		if len(pos) != 1 {
			return usagef("versions takes one s3://bucket/prefix/ path")
		}
		p, err := parseS3(pos[0])
		if err != nil {
			return err
		}
		vs, err := c.listVersions(ctx, c.loc(p.Bucket, folderPrefix(p.Key)), "")
		if err != nil {
			return err
		}
		markers := 0
		for _, v := range vs {
			if v.DeleteMarker {
				markers++
			}
			if c.g.json {
				c.emit(map[string]any{"type": "version", "key": v.Key, "versionId": v.VersionID, "latest": v.Latest,
					"deleteMarker": v.DeleteMarker, "size": v.Size, "modified": v.Modified, "etag": v.ETag, "storageClass": v.StorageClass})
				continue
			}
			kind := humanSize(v.Size)
			if v.DeleteMarker {
				kind = "DELETE-MARKER"
			}
			latest := ""
			if v.Latest {
				latest = " (latest)"
			}
			fmt.Fprintf(c.io.Stdout, "%s %13s  %s  %s%s\n", fmtTime(v.Modified), kind, v.VersionID, v.Key, latest)
		}
		if c.g.json {
			c.emit(map[string]any{"type": "summary", "command": "versions", "ok": true, "count": len(vs), "deleteMarkers": markers})
		}
		return nil
	}
}

// listVersions pages through ListVersions below loc.Prefix. With onlyKey set it
// returns the versions of that key only and stops once the keys sort past it.
func (c *cli) listVersions(ctx context.Context, loc app.Location, onlyKey string) ([]app.Version, error) {
	var out []app.Version
	km, vm := "", ""
	for {
		page, err := c.svc.ListVersions(ctx, loc, km, vm)
		if err != nil {
			return nil, err
		}
		past := false
		for _, v := range page.Versions {
			if onlyKey != "" {
				if v.Key == onlyKey {
					out = append(out, v)
				}
				if v.Key > onlyKey {
					past = true
				}
				continue
			}
			out = append(out, v)
		}
		if past || page.NextKeyMarker == "" && page.NextVersionMarker == "" {
			return out, nil
		}
		km, vm = page.NextKeyMarker, page.NextVersionMarker
	}
}

// setupURL prints the address of an object: its CDN link when the account has
// one for the bucket, else the public S3 address. No request is made.
func setupURL(c *cli, fs *flag.FlagSet) runner {
	kind := fs.String("kind", "", "cdn or public (default: cdn when configured, else public)")
	return func(ctx context.Context, pos []string) error {
		if len(pos) != 1 {
			return usagef("url takes one s3://bucket/key path")
		}
		p, err := parseS3(pos[0])
		if err != nil {
			return err
		}
		if p.Key == "" || strings.HasSuffix(p.Key, "/") {
			return usagef("url needs an object key, not a prefix")
		}
		k := strings.ToLower(*kind)
		switch k {
		case "", "cdn", "public":
		default:
			return usagef("--kind must be cdn or public, not %q", *kind)
		}
		loc := c.loc(p.Bucket, "")
		u := ""
		if k != "public" {
			if u = c.svc.CDNURL(loc, p.Key); u != "" {
				k = "cdn"
			}
		}
		if u == "" {
			if k == "cdn" {
				return &exitErr{exitFailure, fmt.Sprintf("no CDN URL is configured for bucket %s on account %s; set one in the Pail desktop app (Edit account)", p.Bucket, c.acct.Name)}
			}
			k, u = "public", c.svc.PublicURL(loc, p.Key)
		}
		if c.g.json {
			c.emit(map[string]any{"type": "url", "kind": k, "url": u})
			c.emit(map[string]any{"type": "summary", "command": "url", "ok": true})
			return nil
		}
		fmt.Fprintln(c.io.Stdout, u)
		return nil
	}
}

func setupPresign(c *cli, fs *flag.FlagSet) runner {
	expires := fs.Duration("expires", time.Hour, "URL lifetime")
	method := fs.String("method", "GET", "GET or PUT")
	vid := fs.String("version-id", "", "object version")
	return func(ctx context.Context, pos []string) error {
		if len(pos) != 1 {
			return usagef("presign takes one s3://bucket/key path")
		}
		p, err := parseS3(pos[0])
		if err != nil {
			return err
		}
		if p.Key == "" || strings.HasSuffix(p.Key, "/") {
			return usagef("presign needs an object key, not a prefix")
		}
		m := strings.ToUpper(*method)
		if m != "GET" && m != "PUT" {
			return usagef("--method must be GET or PUT")
		}
		if *expires <= 0 {
			return usagef("--expires must be positive")
		}
		u, err := c.svc.PresignURL(ctx, c.loc(p.Bucket, ""), p.Key, *vid, m, *expires)
		if err != nil {
			return err
		}
		if c.g.json {
			c.emit(map[string]any{"type": "presign", "url": u, "method": m, "key": p.Key, "versionId": *vid, "expiresInSeconds": int64(expires.Seconds())})
			c.emit(map[string]any{"type": "summary", "command": "presign", "ok": true})
			return nil
		}
		fmt.Fprintln(c.io.Stdout, u)
		return nil
	}
}
