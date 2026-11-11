package main

import (
	"context"
	"flag"
	"fmt"
	"slices"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/DejavuMoe/Pail/internal/app"
)

// accountFieldFlags are the flags that describe an account. They are given to
// "account add" and "account edit" only.
var accountFieldFlags = []string{"provider", "endpoint", "region", "path-style", "credentials", "access-key-id", "profile",
	"role-arn", "external-id", "role-session-name", "insecure-skip-verify", "checksum-mode", "cdn-url", "cdn-bucket", "bucket", "requester-pays", "proxy"}

// secretFlags control how an account's secret is read; none of them carries it.
var secretFlags = []string{"secret-stdin", "ask-session-token"}

type accountFlags struct {
	provider, endpoint, region, credentials, accessKeyID, profile      string
	roleARN, externalID, roleSessionName, checksumMode, cdnURL, rename string
	proxy                                                              string
	pathStyle, insecure, requesterPays                                 bool
	cdnBuckets, buckets                                                multiFlag
	noTest, secretStdin, setSecret, askSession                         bool
}

func (f *accountFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&f.provider, "provider", "", "aws or s3 (default: s3 with --endpoint, else aws)")
	fs.StringVar(&f.endpoint, "endpoint", "", "endpoint URL of an S3-compatible service")
	fs.StringVar(&f.region, "region", "", "region")
	fs.BoolVar(&f.pathStyle, "path-style", false, "address buckets as endpoint/bucket instead of bucket.endpoint")
	fs.StringVar(&f.credentials, "credentials", "", "static, profile, default or anonymous (default: static, or profile with --profile)")
	fs.StringVar(&f.accessKeyID, "access-key-id", "", "access key ID (not secret) for static credentials")
	fs.StringVar(&f.profile, "profile", "", "shared-credentials profile name")
	fs.StringVar(&f.roleARN, "role-arn", "", "role to assume (arn:...)")
	fs.StringVar(&f.externalID, "external-id", "", "external ID for the assumed role")
	fs.StringVar(&f.roleSessionName, "role-session-name", "", "session name for the assumed role")
	fs.BoolVar(&f.insecure, "insecure-skip-verify", false, "do not verify the server's TLS certificate")
	fs.StringVar(&f.checksumMode, "checksum-mode", "", "auto, when_required or when_supported")
	fs.BoolVar(&f.requesterPays, "requester-pays", false, "send x-amz-request-payer: requester (buckets that bill the requester; your account pays)")
	fs.StringVar(&f.proxy, "proxy", "", "the account's proxy: a URL such as socks5://127.0.0.1:1080, \"direct\" for none, or empty to use the proxy from the settings")
	fs.StringVar(&f.cdnURL, "cdn-url", "", "CDN base URL or template (empty removes it)")
	fs.Var(&f.cdnBuckets, "cdn-bucket", "bucket served by the CDN (repeatable; add an empty value to clear the list)")
	fs.Var(&f.buckets, "bucket", "a bucket the account shows instead of listing them, as NAME or NAME/folder/, for keys that cannot list buckets (repeatable; add an empty value to clear the list)")
	fs.BoolVar(&f.noTest, "no-test", false, "add: save without testing the connection first")
	fs.BoolVar(&f.secretStdin, "secret-stdin", false, "read the secret access key from one line of stdin instead of asking on the terminal")
	fs.BoolVar(&f.setSecret, "set-secret", false, "edit: replace the stored secret access key (asked on the terminal or read with --secret-stdin)")
	fs.BoolVar(&f.askSession, "ask-session-token", false, "also read a session token (the line after the secret, or a second prompt)")
	fs.StringVar(&f.rename, "rename", "", "edit: new account name")
}

// onlyFlags rejects flags that were given but do not belong to what is being
// run. The global flags always belong.
func onlyFlags(fs *flag.FlagSet, what string, allowed ...[]string) error {
	ok := map[string]bool{}
	g := flag.NewFlagSet("", flag.ContinueOnError)
	(&globals{}).register(g)
	g.VisitAll(func(f *flag.Flag) { ok[f.Name] = true })
	for _, l := range allowed {
		for _, n := range l {
			ok[n] = true
		}
	}
	var bad error
	fs.Visit(func(f *flag.Flag) {
		if !ok[f.Name] && bad == nil {
			bad = usagef("--%s does not apply to %s", f.Name, what)
		}
	})
	return bad
}

func setupAccounts(c *cli, fs *flag.FlagSet) runner {
	return func(ctx context.Context, pos []string) error {
		if len(pos) != 0 {
			return usagef("accounts takes no arguments")
		}
		return c.listAccounts("accounts")
	}
}

func (c *cli) listAccounts(command string) error {
	accts := c.svc.Accounts()
	if c.g.json {
		for _, a := range accts {
			c.emit(map[string]any{"type": "account", "id": a.ID, "name": a.Name, "provider": a.Provider,
				"endpoint": a.Endpoint, "region": a.Region, "credentials": a.Credentials})
		}
		c.emit(map[string]any{"type": "summary", "command": command, "ok": true, "count": len(accts)})
		return nil
	}
	if len(accts) == 0 {
		c.infof("no accounts configured; add one with pail account add or in the Pail desktop app")
		return nil
	}
	tw := tabwriter.NewWriter(c.io.Stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tID\tPROVIDER\tENDPOINT\tREGION")
	for _, a := range accts {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", a.Name, a.ID, a.Provider, orDash(a.Endpoint), orDash(a.Region))
	}
	return tw.Flush()
}

func setupAccount(c *cli, fs *flag.FlagSet) runner {
	var f accountFlags
	f.register(fs)
	return func(ctx context.Context, pos []string) error {
		if len(pos) == 0 {
			return usagef("account needs a subcommand: ls, show, add, edit, test or rm")
		}
		sub, args := pos[0], pos[1:]
		switch sub {
		case "ls":
			if len(args) != 0 {
				return usagef("account ls takes no arguments")
			}
			if err := onlyFlags(fs, "account ls"); err != nil {
				return err
			}
			return c.listAccounts("account ls")
		case "show", "test", "rm":
			if len(args) > 1 {
				return usagef("account %s takes at most one account name", sub)
			}
			if err := onlyFlags(fs, "account "+sub); err != nil {
				return err
			}
			if err := c.pickAccount(args); err != nil {
				return err
			}
			switch sub {
			case "show":
				return c.accountShow()
			case "test":
				return c.accountTest(ctx)
			}
			return c.accountRemove()
		case "add":
			if len(args) != 1 {
				return usagef("account add takes the new account's name")
			}
			if err := onlyFlags(fs, "account add", accountFieldFlags, secretFlags, []string{"no-test"}); err != nil {
				return err
			}
			return c.accountAdd(ctx, fs, &f, args[0])
		case "edit":
			if len(args) > 1 {
				return usagef("account edit takes at most one account name")
			}
			if err := onlyFlags(fs, "account edit", accountFieldFlags, secretFlags, []string{"rename", "set-secret"}); err != nil {
				return err
			}
			if err := c.pickAccount(args); err != nil {
				return err
			}
			return c.accountEdit(fs, &f)
		}
		return usagef("unknown account subcommand %q (ls, show, add, edit, test, rm)", sub)
	}
}

// pickAccount selects the account named by the optional positional argument,
// else by --account, else the only one.
func (c *cli) pickAccount(args []string) error {
	if len(args) == 1 {
		c.g.account = args[0]
	}
	return c.resolveAccount()
}

// readSecretLine reads one secret: the first remaining line of stdin with
// fromStdin, else a no-echo terminal prompt. It is never taken from a flag
// value or the environment.
func (c *cli) readSecretLine(prompt, what string, fromStdin, allowEmpty bool) (string, error) {
	var v string
	if fromStdin {
		line, err := c.in.ReadString('\n')
		v = strings.TrimRight(line, "\r\n")
		if line == "" && err != nil {
			if allowEmpty {
				return "", nil
			}
			return "", &exitErr{exitUsage, fmt.Sprintf("--secret-stdin: no %s on stdin (expected one line)", what)}
		}
	} else {
		if c.io.ReadPassword == nil {
			return "", &exitErr{exitUsage, fmt.Sprintf("there is no terminal to ask for the %s; pass --secret-stdin and send it as one line on stdin", what)}
		}
		var err error
		if v, err = c.io.ReadPassword(prompt); err != nil {
			return "", &exitErr{exitUsage, fmt.Sprintf("could not read the %s: %v", what, err)}
		}
	}
	if v == "" && !allowEmpty {
		return "", &exitErr{exitUsage, "the " + what + " must not be empty"}
	}
	return v, nil
}

// readAccountSecrets asks for the secret access key and, with ask, a session token.
func (c *cli) readAccountSecrets(f *accountFlags) (*app.Secrets, error) {
	key, err := c.readSecretLine("Secret access key: ", "secret access key", f.secretStdin, false)
	if err != nil {
		return nil, err
	}
	sec := &app.Secrets{SecretAccessKey: key}
	if f.askSession {
		if sec.SessionToken, err = c.readSecretLine("Session token (empty for none): ", "session token", f.secretStdin, true); err != nil {
			return nil, err
		}
	}
	return sec, nil
}

// scrubErr removes the secret strings from an error before it is shown.
func scrubErr(err error, sec *app.Secrets) error {
	if err == nil || sec == nil {
		return err
	}
	msg := err.Error()
	changed := false
	for _, s := range []string{sec.SecretAccessKey, sec.SessionToken} {
		if s != "" && strings.Contains(msg, s) {
			msg = strings.ReplaceAll(msg, s, "***")
			changed = true
		}
	}
	if !changed {
		return err
	}
	code := exitFailure
	if app.IsKind(err, app.ErrInvalid) || app.IsKind(err, app.ErrLocked) {
		code = exitUsage
	}
	return &exitErr{code, msg}
}

func (c *cli) accountAdd(ctx context.Context, fs *flag.FlagSet, f *accountFlags, name string) error {
	name = strings.TrimSpace(name)
	for _, a := range c.svc.Accounts() {
		if strings.EqualFold(a.Name, name) {
			return usagef("an account named %q already exists (%s); choose another name or use account edit", a.Name, a.ID)
		}
	}
	a := app.Account{Name: name}
	if err := applyAccountFlags(fs, f, &a); err != nil {
		return err
	}
	if a.Credentials == "" {
		a.Credentials = app.CredStatic
		if a.Profile != "" {
			a.Credentials = app.CredProfile
		}
	}
	static := a.Credentials == app.CredStatic
	if !static && (f.secretStdin || f.askSession) {
		return usagef("--secret-stdin and --ask-session-token apply to static credentials only")
	}
	a, err := app.ValidateAccount(a)
	if err != nil {
		return err
	}
	var sec *app.Secrets
	if static {
		if err := c.unlockVault(); err != nil {
			return err
		}
		if sec, err = c.readAccountSecrets(f); err != nil {
			return err
		}
	}
	if !f.noTest {
		c.infof("testing the connection ...")
		if err := c.svc.TestAccount(ctx, a, sec); err != nil {
			c.infof("nothing was saved; fix the settings, name the buckets of a key that cannot list them with --bucket, or add --no-test to save without testing")
			return scrubErr(err, sec)
		}
	}
	saved, err := c.svc.SaveAccount(a, sec)
	if saved.ID == "" {
		return scrubErr(err, sec)
	}
	if err != nil {
		c.infof("warning: %v", scrubErr(err, sec))
	}
	return c.single("account add", fmt.Sprintf("added account %s (%s)", saved.Name, saved.ID),
		map[string]any{"id": saved.ID, "name": saved.Name})
}

// applyAccountFlags copies the account flags that were given onto a. Only
// --bucket can fail here, on an entry that does not parse; the other values
// are checked by ValidateAccount.
func applyAccountFlags(fs *flag.FlagSet, f *accountFlags, a *app.Account) error {
	var bad error
	fs.Visit(func(fl *flag.Flag) {
		switch fl.Name {
		case "provider":
			a.Provider = f.provider
		case "endpoint":
			a.Endpoint = f.endpoint
		case "region":
			a.Region = f.region
		case "path-style":
			a.PathStyle = f.pathStyle
		case "credentials":
			a.Credentials = f.credentials
		case "access-key-id":
			a.AccessKeyID = f.accessKeyID
		case "profile":
			a.Profile = f.profile
		case "role-arn":
			a.RoleARN = f.roleARN
		case "external-id":
			a.ExternalID = f.externalID
		case "role-session-name":
			a.RoleSessionName = f.roleSessionName
		case "insecure-skip-verify":
			a.InsecureSkipVerify = f.insecure
		case "checksum-mode":
			a.ChecksumMode = f.checksumMode
		case "requester-pays":
			a.RequesterPays = f.requesterPays
		case "proxy":
			switch p := strings.TrimSpace(f.proxy); p {
			case "":
				a.ProxyMode, a.ProxyURL = app.ProxyGlobal, ""
			case app.ProxyDirect:
				a.ProxyMode, a.ProxyURL = app.ProxyDirect, ""
			default:
				a.ProxyMode, a.ProxyURL = app.ProxyCustom, p
			}
		case "cdn-url":
			a.CDNURL = f.cdnURL
		case "cdn-bucket":
			a.CDNBuckets = nil
			for _, b := range f.cdnBuckets {
				if b = strings.TrimSpace(b); b != "" {
					a.CDNBuckets = append(a.CDNBuckets, b)
				}
			}
		case "bucket":
			var err error
			if a.Buckets, err = app.ParseAccountBuckets(f.buckets); err != nil {
				bad = usagef("--bucket: %s", strings.TrimPrefix(err.Error(), "buckets: "))
			}
		}
	})
	return bad
}

// bucketTexts are named buckets in the form --bucket takes.
func bucketTexts(bs []app.AccountBucket) []string {
	out := make([]string, len(bs))
	for i, b := range bs {
		out[i] = b.String()
	}
	return out
}

func (c *cli) accountEdit(fs *flag.FlagSet, f *accountFlags) error {
	old := *c.acct
	a := old
	changed := false
	fs.Visit(func(fl *flag.Flag) {
		if fl.Name == "rename" || slices.Contains(accountFieldFlags, fl.Name) {
			changed = true
		}
	})
	if err := applyAccountFlags(fs, f, &a); err != nil {
		return err
	}
	if f.rename != "" {
		a.Name = f.rename
		for _, o := range c.svc.Accounts() {
			if o.ID != old.ID && strings.EqualFold(o.Name, strings.TrimSpace(a.Name)) {
				return usagef("an account named %q already exists (%s)", o.Name, o.ID)
			}
		}
	}
	if (f.secretStdin || f.askSession) && !f.setSecret {
		return usagef("--secret-stdin and --ask-session-token change the secret only together with --set-secret")
	}
	if !changed && !f.setSecret {
		return usagef("nothing to change: give at least one account flag or --set-secret")
	}
	if f.setSecret && a.Credentials != app.CredStatic {
		return usagef("--set-secret applies to static credentials only")
	}
	if a.Credentials == app.CredStatic && old.Credentials != app.CredStatic && !f.setSecret {
		return usagef("switching to static credentials needs the secret key: add --set-secret")
	}
	a, err := app.ValidateAccount(a)
	if err != nil {
		return err
	}
	var sec *app.Secrets
	if f.setSecret || (old.Credentials == app.CredStatic && a.Credentials != app.CredStatic) {
		if err := c.unlockVault(); err != nil { // writing or deleting secrets
			return err
		}
	}
	if f.setSecret {
		if sec, err = c.readAccountSecrets(f); err != nil {
			return err
		}
	}
	saved, err := c.svc.SaveAccount(a, sec)
	if saved.ID == "" {
		return scrubErr(err, sec)
	}
	if err != nil {
		c.infof("warning: %v", scrubErr(err, sec))
	}
	return c.single("account edit", fmt.Sprintf("updated account %s (%s)", saved.Name, saved.ID),
		map[string]any{"id": saved.ID, "name": saved.Name})
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func (c *cli) accountShow() error {
	a := *c.acct
	if c.g.json {
		o := map[string]any{"type": "account", "id": a.ID, "name": a.Name, "provider": a.Provider, "endpoint": a.Endpoint,
			"region": a.Region, "pathStyle": a.PathStyle, "credentials": a.Credentials, "accessKeyId": a.AccessKeyID,
			"profile": a.Profile, "roleArn": a.RoleARN, "externalId": a.ExternalID, "roleSessionName": a.RoleSessionName,
			"insecureSkipVerify": a.InsecureSkipVerify, "checksumMode": a.ChecksumMode, "cdnUrl": a.CDNURL,
			"requesterPays": a.RequesterPays, "proxyMode": a.ProxyMode, "proxyUrl": a.ProxyURL,
			"cdnBuckets": a.CDNBuckets, "buckets": a.Buckets, "bucketAliases": a.BucketAliases}
		if a.Credentials == app.CredStatic {
			o["secretStored"] = !a.SecretMissing
		}
		c.emit(o)
		c.emit(map[string]any{"type": "summary", "command": "account show", "ok": true})
		return nil
	}
	tw := tabwriter.NewWriter(c.io.Stdout, 2, 4, 2, ' ', 0)
	row := func(k, v string) { fmt.Fprintf(tw, "%s:\t%s\n", k, v) }
	opt := func(k, v string) {
		if v != "" {
			row(k, v)
		}
	}
	row("name", a.Name)
	row("id", a.ID)
	row("provider", a.Provider)
	opt("endpoint", a.Endpoint)
	opt("region", a.Region)
	row("path-style", yesNo(a.PathStyle))
	row("credentials", a.Credentials)
	opt("access key ID", a.AccessKeyID)
	if a.Credentials == app.CredStatic {
		row("secret key", map[bool]string{true: "stored", false: "missing"}[!a.SecretMissing])
	}
	opt("profile", a.Profile)
	opt("role ARN", a.RoleARN)
	opt("external ID", a.ExternalID)
	opt("role session name", a.RoleSessionName)
	if a.InsecureSkipVerify {
		row("TLS verification", "off")
	}
	row("checksum mode", a.ChecksumMode)
	if a.RequesterPays {
		row("requester pays", "yes")
	}
	switch a.ProxyMode {
	case app.ProxyDirect:
		row("proxy", "none (direct)")
	case app.ProxyCustom:
		row("proxy", a.ProxyURL)
	}
	opt("CDN URL", a.CDNURL)
	if len(a.CDNBuckets) > 0 {
		row("CDN buckets", strings.Join(a.CDNBuckets, ", "))
	}
	if len(a.Buckets) > 0 {
		row("buckets", strings.Join(bucketTexts(a.Buckets), ", "))
	}
	if len(a.BucketAliases) > 0 {
		names := make([]string, 0, len(a.BucketAliases))
		for b, al := range a.BucketAliases {
			names = append(names, b+" = "+al)
		}
		sort.Strings(names)
		row("bucket aliases", strings.Join(names, ", "))
	}
	return tw.Flush()
}

func (c *cli) accountTest(ctx context.Context) error {
	if c.acct.Credentials == app.CredStatic {
		if err := c.unlockVault(); err != nil {
			return err
		}
	}
	if err := c.svc.TestAccount(ctx, *c.acct, nil); err != nil {
		return err
	}
	msg := fmt.Sprintf("ok: account %s can list its buckets", c.acct.Name)
	if len(c.acct.Buckets) > 0 {
		msg = fmt.Sprintf("ok: account %s can list %s", c.acct.Name, strings.Join(bucketTexts(c.acct.Buckets), ", "))
	}
	return c.single("account test", msg, map[string]any{"id": c.acct.ID, "name": c.acct.Name})
}

func (c *cli) accountRemove() error {
	a := *c.acct
	where := "the system keychain"
	if c.svc.SecretBackend() == app.SecretBackendVault {
		where = "the encrypted vault"
	}
	scope := []string{"Remove account: " + a.Name + " (" + a.ID + ")"}
	if a.Credentials == app.CredStatic {
		scope = append(scope, "Its saved secret key is deleted from "+where+".")
	}
	scope = append(scope, "Buckets and objects are not touched. Saved sync jobs of this account stay and stop working.")
	if err := c.confirm(scope); err != nil {
		return err
	}
	if a.Credentials == app.CredStatic {
		if err := c.unlockVault(); err != nil {
			return err
		}
	}
	if err := c.svc.DeleteAccount(a.ID); err != nil {
		return err
	}
	return c.single("account rm", fmt.Sprintf("removed account %s (%s)", a.Name, a.ID), map[string]any{"id": a.ID, "name": a.Name})
}

// ---- vault -------------------------------------------------------------------------

func setupVault(c *cli, fs *flag.FlagSet) runner {
	newStdin := fs.Bool("new-vault-password-stdin", false, "passwd: read the new master password from the line after the current one on stdin")
	return func(ctx context.Context, pos []string) error {
		if len(pos) == 0 {
			return usagef("vault needs a subcommand: status, init, unlock, lock, passwd, backend or reset")
		}
		sub, args := pos[0], pos[1:]
		allowed := []string{}
		if sub == "passwd" {
			allowed = append(allowed, "new-vault-password-stdin")
		}
		if err := onlyFlags(fs, "vault "+sub, allowed); err != nil {
			return err
		}
		switch sub {
		case "status":
			if len(args) != 0 {
				return usagef("usage: pail vault status [--vault-password-stdin]")
			}
			return c.vaultStatus()
		case "init":
			if len(args) != 0 {
				return usagef("vault init takes no arguments")
			}
			return c.vaultInit(ctx)
		case "unlock":
			if len(args) != 0 {
				return usagef("vault unlock takes no arguments")
			}
			return c.vaultUnlock()
		case "lock":
			if len(args) != 0 {
				return usagef("vault lock takes no arguments")
			}
			c.svc.LockVault()
			return c.single("vault lock", "nothing to keep locked: every pail run starts with the vault locked and unlocks it only for that run; the desktop app locks itself from its own menu", nil)
		case "passwd":
			if len(args) != 0 {
				return usagef("vault passwd takes no arguments")
			}
			return c.vaultPasswd(*newStdin)
		case "backend":
			if len(args) != 1 {
				return usagef("usage: pail vault backend keychain|vault")
			}
			switch strings.ToLower(args[0]) {
			case "vault":
				return c.vaultInit(ctx)
			case "keychain", "keyring":
				return c.vaultToKeychain(ctx)
			}
			return usagef("the secret backend must be keychain or vault, not %q", args[0])
		case "reset":
			if len(args) != 0 {
				return usagef("vault reset takes no arguments")
			}
			return c.vaultReset()
		}
		return usagef("unknown vault subcommand %q (status, init, unlock, lock, passwd, backend, reset)", sub)
	}
}

// vaultPassword reads one master password: a line of stdin with
// --vault-password-stdin, else a no-echo prompt.
func (c *cli) vaultPassword(prompt string) (string, error) {
	if c.g.vaultPasswordStdin {
		line, err := c.in.ReadString('\n')
		pw := strings.TrimRight(line, "\r\n")
		if pw == "" {
			if err != nil || line == "" {
				return "", &exitErr{exitUsage, "--vault-password-stdin: no password on stdin (expected one line)"}
			}
			return "", &exitErr{exitUsage, "--vault-password-stdin: the password line is empty"}
		}
		return pw, nil
	}
	return c.promptPassword(prompt)
}

func (c *cli) promptPassword(prompt string) (string, error) {
	if c.io.ReadPassword == nil {
		return "", &exitErr{exitUsage, "there is no terminal to ask for the master password; " +
			"pass --vault-password-stdin and send the password as a line of stdin"}
	}
	pw, err := c.io.ReadPassword(prompt)
	if err != nil {
		return "", &exitErr{exitUsage, "could not read the master password: " + err.Error()}
	}
	if pw == "" {
		return "", &exitErr{exitUsage, "the master password must not be empty"}
	}
	return pw, nil
}

// newVaultPassword reads a new master password: a line of stdin when fromStdin,
// else a prompt that must be repeated identically.
func (c *cli) newVaultPassword(fromStdin bool) (string, error) {
	if fromStdin {
		saved := c.g.vaultPasswordStdin
		c.g.vaultPasswordStdin = true
		defer func() { c.g.vaultPasswordStdin = saved }()
		return c.vaultPassword("")
	}
	pw, err := c.promptPassword("New vault master password: ")
	if err != nil {
		return "", err
	}
	again, err := c.promptPassword("Repeat the new master password: ")
	if err != nil {
		return "", err
	}
	if pw != again {
		return "", &exitErr{exitUsage, "the two passwords differ; nothing was changed"}
	}
	return pw, nil
}

// vaultInit moves the secrets into the encrypted vault, creating it.
func (c *cli) vaultInit(ctx context.Context) error {
	if c.svc.SecretBackend() == app.SecretBackendVault {
		return &exitErr{exitFailure, "the encrypted vault is already in use; change its password with pail vault passwd"}
	}
	pw, err := c.newVaultPassword(c.g.vaultPasswordStdin)
	if err != nil {
		return err
	}
	if err := c.svc.SetSecretBackend(ctx, app.SecretBackendVault, pw); err != nil {
		return err
	}
	return c.single("vault init", "secrets now live in the encrypted vault (vault.json); every run asks for the master password",
		map[string]any{"backend": app.SecretBackendVault})
}

func (c *cli) vaultToKeychain(ctx context.Context) error {
	if c.svc.SecretBackend() != app.SecretBackendVault {
		return c.single("vault backend", "the system keychain is already the secret backend", map[string]any{"backend": app.SecretBackendKeyring})
	}
	if err := c.unlockVault(); err != nil {
		return err
	}
	if err := c.svc.SetSecretBackend(ctx, app.SecretBackendKeyring, ""); err != nil {
		return err
	}
	return c.single("vault backend", "secrets now live in the system keychain; vault.json was removed",
		map[string]any{"backend": app.SecretBackendKeyring})
}

func (c *cli) vaultUnlock() error {
	if c.svc.SecretBackend() != app.SecretBackendVault {
		return &exitErr{exitFailure, "the secret backend is the system keychain; there is no vault to unlock"}
	}
	if err := c.unlockVault(); err != nil {
		return err
	}
	return c.single("vault unlock", "master password accepted (the unlock lasts for this run only; commands that need credentials ask again)", nil)
}

func (c *cli) vaultPasswd(newStdin bool) error {
	st := c.svc.VaultState()
	if c.svc.SecretBackend() != app.SecretBackendVault || !st.Exists {
		return &exitErr{exitFailure, "the encrypted vault is not in use; there is no master password to change"}
	}
	old, err := c.vaultPassword("Current vault master password: ")
	if err != nil {
		return err
	}
	pw, err := c.newVaultPassword(newStdin)
	if err != nil {
		return err
	}
	if err := c.svc.ChangeVaultPassword(old, pw); err != nil {
		return err
	}
	return c.single("vault passwd", "the master password was changed", nil)
}

func (c *cli) vaultReset() error {
	st := c.svc.VaultState()
	inUse := c.svc.SecretBackend() == app.SecretBackendVault
	if !inUse && !st.Exists {
		return &exitErr{exitFailure, "nothing to reset: there is no encrypted vault"}
	}
	var names []string
	for _, a := range c.svc.Accounts() {
		if a.Credentials == app.CredStatic && !a.SecretMissing {
			names = append(names, a.Name)
		}
	}
	scope := []string{
		"Reset the encrypted vault (vault.json): the file is deleted and every secret in it is lost.",
		"The secret backend becomes the system keychain. Without the master password the secrets cannot be recovered.",
	}
	if inUse && len(names) > 0 {
		scope = append(scope, "These accounts need their secret key entered again: "+strings.Join(names, ", "))
	}
	if err := c.confirmTyped(scope, "RESET"); err != nil {
		return err
	}
	res, err := c.svc.ResetVault()
	if err != nil {
		return err
	}
	return c.single("vault reset", fmt.Sprintf("vault reset: %d account(s) need their secret key entered again", len(res.Accounts)),
		map[string]any{"accounts": len(res.Accounts), "backend": app.SecretBackendKeyring})
}
