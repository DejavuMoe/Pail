package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/DejavuMoe/Pail/internal/app"
)

// version is set at release time with -ldflags "-X main.version=...".
var version = "dev"

// Exit codes.
const (
	exitOK        = 0
	exitFailure   = 1
	exitUsage     = 2
	exitPartial   = 3
	exitInterrupt = 130
)

// IO is the process environment of one CLI run; tests inject their own.
type IO struct {
	Stdin     io.Reader
	Stdout    io.Writer
	Stderr    io.Writer
	StdinTTY  bool
	StderrTTY bool
	Keyring   app.Keyring // nil = OS keyring
	// ReadPassword prompts on the terminal without echo; nil when there is no
	// terminal on stdin (then --vault-password-stdin is the only way).
	ReadPassword func(prompt string) (string, error)
}

// usageError is invalid input; it exits with code 2.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func usagef(format string, a ...any) error { return &usageError{fmt.Sprintf(format, a...)} }

// exitErr ends the run with code. msg may be empty when everything was already printed.
type exitErr struct {
	code int
	msg  string
}

func (e *exitErr) Error() string { return e.msg }

type globals struct {
	account, configDir string
	json, quiet, yes   bool
	timeout            time.Duration
	// vaultPasswordStdin reads the vault master password from the first line
	// of stdin. There is deliberately no flag or variable that carries the
	// password itself.
	vaultPasswordStdin bool
}

func (g *globals) register(fs *flag.FlagSet) {
	fs.StringVar(&g.account, "account", g.account, "account name or ID (default: the only account)")
	fs.StringVar(&g.configDir, "config-dir", g.configDir, "configuration directory (default: the desktop app's)")
	fs.BoolVar(&g.json, "json", g.json, "machine-readable output (newline-delimited JSON)")
	fs.BoolVar(&g.quiet, "quiet", g.quiet, "no progress or per-item lines on stderr")
	fs.BoolVar(&g.yes, "yes", g.yes, "assume yes for confirmations")
	fs.DurationVar(&g.timeout, "timeout", g.timeout, "abort the command after this long (0 = no limit)")
	fs.BoolVar(&g.vaultPasswordStdin, "vault-password-stdin", g.vaultPasswordStdin, "read the encrypted vault's master password from the first line of stdin")
}

type cli struct {
	io   IO
	g    globals
	svc  *app.Service
	acct *app.Account
	in   *bufio.Reader
	enc  *json.Encoder
}

type runner func(ctx context.Context, pos []string) error

type command struct {
	name, synopsis, desc string
	needAccount          bool
	setup                func(c *cli, fs *flag.FlagSet) runner
}

var commands []command

func init() {
	commands = []command{
		{"accounts", "accounts", "list configured accounts", false, setupAccounts},
		{"vault", "vault status [--vault-password-stdin]", "show the secret backend and encrypted vault state", false, setupVault},
		{"buckets", "buckets", "list buckets with their region", true, setupBuckets},
		{"ls", "ls [s3://bucket/prefix/] [-r]", "list objects", true, setupLs},
		{"cp", "cp SRC DST [-r] [--conflict P] [--decode M]", "copy local<->S3 or within S3", true, setupCp},
		{"mv", "mv s3://SRC s3://DST [-r] [--conflict P]", "move objects within S3", true, setupMv},
		{"rm", "rm s3://bucket/key [-r] [--versions]", "delete objects", true, setupRm},
		{"mb", "mb s3://bucket [--region R] [--object-lock]", "create a bucket", true, setupMb},
		{"rb", "rb s3://bucket", "delete an empty bucket", true, setupRb},
		{"presign", "presign s3://bucket/key [--expires 1h] [--method GET|PUT] [--version-id V]", "create a presigned URL", true, setupPresign},
		{"url", "url s3://bucket/key [--kind cdn|public]", "print the CDN or public URL of an object", true, setupURL},
		{"versions", "versions s3://bucket/prefix/", "list versions and delete markers", true, setupVersions},
		{"restore", "restore s3://bucket/key --version-id V | s3://bucket/prefix/ --deleted [-r]", "restore a version or deleted objects", true, setupRestore},
		{"sync", "sync LOCAL s3://b/prefix/ | s3://b/prefix/ LOCAL | --job NAME", "one-way synchronisation", false, setupSync},
	}
}

func (c *cli) usage(w io.Writer) {
	fmt.Fprintln(w, "usage: pail [global flags] COMMAND [flags] [args]")
	fmt.Fprintln(w, "\ncommands:")
	for _, cm := range commands {
		fmt.Fprintf(w, "  %-14s %s\n", cm.name, cm.desc)
	}
	fmt.Fprintf(w, "  %-14s %s\n", "version", "print the version")
	fmt.Fprintln(w, "\nglobal flags (accepted before or after the command):")
	fs := flag.NewFlagSet("", flag.ContinueOnError)
	fs.SetOutput(w)
	(&globals{}).register(fs)
	fs.PrintDefaults()
	fmt.Fprintln(w, "\nexit codes: 0 ok, 1 failure, 2 usage or missing confirmation, 3 partial failure, 130 interrupted")
	fmt.Fprintln(w, "S3 paths are written s3://bucket/key-or-prefix/ ; anything else is a local path.")
}

// run executes one CLI invocation and returns the process exit code.
func run(ctx context.Context, args []string, io IO) int {
	if io.Stdin == nil {
		io.Stdin = strings.NewReader("")
	}
	c := &cli{io: io}
	c.in = bufio.NewReader(io.Stdin)
	c.enc = json.NewEncoder(io.Stdout)
	c.enc.SetEscapeHTML(false)
	err := c.dispatch(ctx, args)
	if c.svc != nil {
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = c.svc.Shutdown(sctx)
		cancel()
	}
	return c.report(err)
}

func (c *cli) report(err error) int {
	if err == nil {
		return exitOK
	}
	var ee *exitErr
	if errors.As(err, &ee) {
		if ee.msg != "" {
			c.fail(ee.msg, "", ee.code)
		}
		return ee.code
	}
	var ue *usageError
	if errors.As(err, &ue) {
		c.fail(ue.msg, "usage", exitUsage)
		return exitUsage
	}
	if errors.Is(err, flag.ErrHelp) {
		return exitOK
	}
	var oe *app.OpError
	if errors.As(err, &oe) {
		code := exitFailure
		if oe.Kind == app.ErrInvalid || oe.Kind == app.ErrLocked {
			code = exitUsage
		}
		if oe.Kind == app.ErrCanceled {
			code = exitInterrupt
		}
		c.fail(describeOpError(oe), kindName(oe.Kind), code)
		return code
	}
	if errors.Is(err, context.Canceled) {
		c.fail("interrupted", "canceled", exitInterrupt)
		return exitInterrupt
	}
	if errors.Is(err, context.DeadlineExceeded) {
		c.fail("timed out", "timeout", exitFailure)
		return exitFailure
	}
	c.fail(err.Error(), "other", exitFailure)
	return exitFailure
}

func (c *cli) fail(msg, kind string, code int) {
	if c.g.json {
		c.emit(map[string]any{"type": "error", "kind": kind, "message": msg, "exitCode": code})
	}
	fmt.Fprintln(c.io.Stderr, "pail: "+msg)
}

// kindName is the OpError kind in human words.
func kindName(k app.ErrKind) string {
	switch k {
	case app.ErrNotFound:
		return "not found"
	case app.ErrAccessDenied:
		return "access denied"
	case app.ErrUnsupported:
		return "not supported by this provider"
	case app.ErrConflict:
		return "conflict (the target already exists or changed)"
	case app.ErrNetwork:
		return "network error"
	case app.ErrCanceled:
		return "canceled"
	case app.ErrInvalid:
		return "invalid input"
	case app.ErrLocked:
		return "locked"
	}
	return "error"
}

func describeOpError(oe *app.OpError) string {
	return kindName(oe.Kind) + ": " + oe.Error()
}

func (c *cli) emit(v any) { _ = c.enc.Encode(v) }

func (c *cli) infof(format string, a ...any) {
	if !c.g.quiet {
		fmt.Fprintf(c.io.Stderr, format+"\n", a...)
	}
}

// parseInterspersed parses flags that may appear before, between and after
// positional arguments.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for len(args) > 0 {
		arg := args[0]
		if arg == "--" {
			return append(pos, args[1:]...), nil
		}
		if len(arg) < 2 || arg[0] != '-' {
			pos = append(pos, arg)
			args = args[1:]
			continue
		}
		n := 1
		name := strings.TrimPrefix(strings.TrimPrefix(arg, "-"), "-")
		if f := fs.Lookup(name); f != nil {
			boolean, ok := f.Value.(interface{ IsBoolFlag() bool })
			if !ok || !boolean.IsBoolFlag() {
				n = min(2, len(args)) // A flag value may itself be "--".
			}
		}
		if err := fs.Parse(args[:n]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil, err
			}
			return nil, usagef("%v", err)
		}
		args = args[n:]
	}
	return pos, nil
}

func (c *cli) dispatch(ctx context.Context, args []string) error {
	top := flag.NewFlagSet("pail", flag.ContinueOnError)
	top.SetOutput(io.Discard)
	c.g.register(top)
	if err := top.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			c.usage(c.io.Stdout)
			return nil
		}
		return usagef("%v", err)
	}
	rest := top.Args()
	if len(rest) == 0 {
		c.usage(c.io.Stderr)
		return usagef("no command given")
	}
	if rest[0] == "help" {
		c.usage(c.io.Stdout)
		return nil
	}
	if rest[0] == "version" {
		fmt.Fprintln(c.io.Stdout, "pail", version)
		return nil
	}
	var cm *command
	for i := range commands {
		if commands[i].name == rest[0] {
			cm = &commands[i]
		}
	}
	if cm == nil {
		return usagef("unknown command %q (run pail help)", rest[0])
	}
	fs := flag.NewFlagSet(cm.name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	c.g.register(fs)
	rn := cm.setup(c, fs)
	pos, err := parseInterspersed(fs, rest[1:])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintf(c.io.Stdout, "usage: pail %s\n", cm.synopsis)
			fs.SetOutput(c.io.Stdout)
			fs.PrintDefaults()
			return nil
		}
		return err
	}
	if c.g.timeout < 0 {
		return usagef("--timeout must not be negative")
	}
	if c.g.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.g.timeout)
		defer cancel()
	}
	svc, err := app.New(app.Options{ConfigDir: c.g.configDir, Keyring: c.io.Keyring})
	if err != nil {
		return err
	}
	c.svc = svc
	if cm.needAccount {
		if err := c.resolveAccount(); err != nil {
			return err
		}
	}
	// Sync selects its saved job's account before deciding which secrets it needs.
	if cm.name != "vault" && cm.name != "sync" && c.needsVault(cm.name) {
		if err := c.unlockVault(); err != nil {
			return err
		}
	}
	return rn(ctx, pos)
}

func (c *cli) resolveAccount() error {
	accts := c.svc.Accounts()
	list := func() string {
		names := make([]string, len(accts))
		for i, a := range accts {
			names[i] = fmt.Sprintf("%s (%s)", a.Name, a.ID)
		}
		sort.Strings(names)
		return strings.Join(names, ", ")
	}
	if len(accts) == 0 {
		return usagef("no accounts are configured; add one in the Pail desktop app")
	}
	if c.g.account == "" {
		if len(accts) == 1 {
			c.acct = &accts[0]
			return nil
		}
		return usagef("several accounts exist; choose one with --account: %s", list())
	}
	for i := range accts {
		if accts[i].ID == c.g.account {
			c.acct = &accts[i]
			return nil
		}
	}
	var hit []int
	for i := range accts {
		if strings.EqualFold(accts[i].Name, c.g.account) {
			hit = append(hit, i)
		}
	}
	if len(hit) == 1 {
		c.acct = &accts[hit[0]]
		return nil
	}
	if len(hit) > 1 {
		return usagef("account name %q is ambiguous; use the ID: %s", c.g.account, list())
	}
	return usagef("no account %q; available: %s", c.g.account, list())
}

func (c *cli) loc(bucket, prefix string) app.Location {
	return app.Location{AccountID: c.acct.ID, Bucket: bucket, Prefix: prefix}
}

// ---- paths -------------------------------------------------------------

type s3Path struct{ Bucket, Key string }

func isS3(p string) bool { return len(p) >= 5 && strings.EqualFold(p[:5], "s3://") }

func parseS3(p string) (s3Path, error) {
	if !isS3(p) {
		return s3Path{}, usagef("%q is not an S3 path (expected s3://bucket/key)", p)
	}
	rest := p[5:]
	b, k, _ := strings.Cut(rest, "/")
	if b == "" {
		return s3Path{}, usagef("%q has no bucket name", p)
	}
	return s3Path{b, k}, nil
}

func (p s3Path) String() string { return "s3://" + p.Bucket + "/" + p.Key }

// folderPrefix turns a key into a prefix: "" stays, otherwise a trailing "/" is ensured.
func folderPrefix(key string) string {
	if key == "" || strings.HasSuffix(key, "/") {
		return key
	}
	return key + "/"
}

// parentPrefix is the folder part of key ("a/b/c" -> "a/b/", "a/b/" -> "a/").
func parentPrefix(key string) string {
	k := strings.TrimSuffix(key, "/")
	i := strings.LastIndexByte(k, '/')
	if i < 0 {
		return ""
	}
	return k[:i+1]
}

// selectionFor builds the Selection of a path. With recursive, the key is a
// folder (or the whole bucket); without it, it must name one object.
func (c *cli) selectionFor(p s3Path, recursive bool) (app.Selection, error) {
	if recursive {
		if p.Key == "" {
			return app.Selection{Location: c.loc(p.Bucket, ""), All: true}, nil
		}
		f := folderPrefix(p.Key)
		return app.Selection{Location: c.loc(p.Bucket, parentPrefix(f)), Keys: []string{f}}, nil
	}
	if p.Key == "" || strings.HasSuffix(p.Key, "/") {
		return app.Selection{}, usagef("%s is a prefix; add -r to act on everything below it", p)
	}
	return app.Selection{Location: c.loc(p.Bucket, parentPrefix(p.Key)), Keys: []string{p.Key}}, nil
}

func parseConflict(s string) (app.ConflictPolicy, error) {
	p := app.ConflictPolicy(s)
	if s != "" && !p.Valid() {
		return "", usagef("--conflict must be skip, overwrite, rename, newer or different_size, not %q", s)
	}
	return p, nil
}

// ---- output helpers -----------------------------------------------------

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit && exp < 5; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.UTC().Format("2006-01-02 15:04:05")
}

func fmtEstimate(e app.Estimate) string {
	n := fmt.Sprint(e.Objects)
	if e.Truncated {
		n += "+ (counting stopped)"
	}
	return fmt.Sprintf("%s objects, %s", n, humanSize(e.Bytes))
}

// confirm prints the scope and asks for approval. Without a terminal it
// requires --yes.
func (c *cli) confirm(scope []string) error {
	lines := append([]string{"Account: " + c.acct.Name + " (" + c.acct.ID + ")"}, scope...)
	if !(c.g.quiet && c.g.yes) {
		for _, l := range lines {
			fmt.Fprintln(c.io.Stderr, l)
		}
	}
	if c.g.yes {
		return nil
	}
	if !c.io.StdinTTY {
		return usagef("confirmation required: stdin is not a terminal; re-run with --yes to proceed")
	}
	fmt.Fprint(c.io.Stderr, "Proceed? [y/N] ")
	line, _ := c.in.ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return nil
	}
	return &exitErr{exitFailure, "aborted: nothing was changed"}
}
