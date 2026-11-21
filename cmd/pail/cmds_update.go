package main

import (
	"context"
	"flag"
	"fmt"
	"runtime"
	"time"

	"github.com/DejavuMoe/Pail/internal/app"
	"github.com/DejavuMoe/Pail/internal/selfupdate"
)

// Updates of the installation (internal/selfupdate), which hold the desktop
// app and the command line together. The source and the proxy are the
// settings shared with the desktop app; the command line never checks by
// itself.

// checkForUpdate checks with the saved source and proxy; tests replace it.
var checkForUpdate = func(c *cli, ctx context.Context) (*selfupdate.Update, error) {
	proxy, err := c.svc.ProxyURL(ctx)
	if app.IsKind(err, app.ErrLocked) {
		// The proxy's password is in the locked vault.
		if err := c.unlockVault(); err != nil {
			return nil, err
		}
		proxy, err = c.svc.ProxyURL(ctx)
	}
	if err != nil {
		return nil, err
	}
	u := &selfupdate.Updater{Current: version, Source: c.svc.Settings().Updates.Source, Proxy: proxy}
	return u.Check(ctx)
}

// installUpdate is Update.Install; tests replace it.
var installUpdate = func(ctx context.Context, up *selfupdate.Update, progress func(done, total int64)) error {
	return up.Install(ctx, progress)
}

func setupUpdate(c *cli, fs *flag.FlagSet) runner {
	check := fs.Bool("check", false, "only report whether a newer version is available")
	return func(ctx context.Context, pos []string) error {
		if len(pos) > 0 {
			return usagef("update takes no arguments")
		}
		up, err := checkForUpdate(c, ctx)
		if err != nil {
			return err
		}
		if up == nil {
			if c.g.json {
				c.emit(map[string]any{"type": "update", "current": version, "available": false})
			} else {
				fmt.Fprintf(c.io.Stdout, "Pail %s is the latest version\n", version)
			}
			return nil
		}
		size := humanSize(up.Size)
		if up.Delta {
			size += ", delta update"
		}
		if c.g.json {
			c.emit(map[string]any{"type": "update", "current": version, "available": true, "version": up.Version,
				"size": up.Size, "delta": up.Delta, "source": string(up.From)})
		} else {
			fmt.Fprintf(c.io.Stdout, "Pail %s is available (you have %s; download %s from %s)\n", up.Version, version, size, sourceName(up.From))
		}
		if *check {
			return nil
		}
		progress := func(int64, int64) {}
		if c.io.StderrTTY && !c.g.quiet && !c.g.json {
			var last time.Time
			progress = func(done, total int64) {
				if now := time.Now(); now.Sub(last) >= 100*time.Millisecond || done == total {
					last = now
					fmt.Fprintf(c.io.Stderr, "\rdownloading %s of %s   ", humanSize(done), humanSize(total))
				}
			}
		}
		err = installUpdate(ctx, up, progress)
		if c.io.StderrTTY && !c.g.quiet && !c.g.json {
			fmt.Fprint(c.io.Stderr, "\r\033[K")
		}
		if err != nil {
			return err
		}
		if c.g.json {
			c.emit(map[string]any{"type": "summary", "command": "update", "ok": true, "version": up.Version})
			return nil
		}
		if runtime.GOOS == "darwin" {
			fmt.Fprintf(c.io.Stdout, "Pail %s replaces Pail.app as soon as this command exits. If the desktop app is open, quit and open it again.\n", up.Version)
		} else {
			fmt.Fprintf(c.io.Stdout, "Pail %s is installed. If the desktop app is open, it keeps the old version until it starts again.\n", up.Version)
		}
		return nil
	}
}

// sourceName is how a source is written for people.
func sourceName(s selfupdate.Source) string {
	if s == selfupdate.SourceMirror {
		return "pail.download"
	}
	return "GitHub"
}
