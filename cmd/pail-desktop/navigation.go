package main

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
)

// refreshAccounts re-reads the saved accounts and keeps the selection valid.
func (a *app) refreshAccounts() {
	a.accounts = a.svc.Accounts()
	if a.account().ID == "" {
		a.selectAccount("")
		if len(a.accounts) > 0 {
			a.selectAccount(a.accounts[0].ID)
		}
	}
}

// selectAccount makes id the current account and loads its buckets.
func (a *app) selectAccount(id string) {
	if id == a.accountID && id != "" {
		return
	}
	a.accountID = id
	a.scope = Scope{AccountID: id}
	a.navSel, a.tool = "", ""
	a.buckets, a.bucketsErr = nil, ""
	a.mg.del = deletedMode{} // deleted items are of a bucket of the account left
	a.accountSwitched()
	a.clearListing()
	a.bucketsGen++
	a.bucketsLoad = false
	if id != "" {
		a.loadBuckets(true)
	}
}

// ---- buckets -------------------------------------------------------------------------

func (a *app) loadBuckets(openFirst bool) {
	id := a.accountID
	if id == "" {
		return
	}
	a.bucketsGen++
	gen := a.bucketsGen
	a.bucketsLoad, a.bucketsErr, a.bucketsFix = true, "", fixRetry
	if a.account().SecretMissing { // nothing to connect with until the secret is entered
		a.bucketsLoad, a.buckets = false, nil // none of them can be opened without it
		a.bucketsErr, a.bucketsFix = locales.StateBucketsSecretMissing(), fixSecret
		return
	}
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	began, acct := time.Now(), a.account()
	a.bg(func() func() {
		defer cancel()
		bs, err := a.svc.Buckets(ctx, id)
		took := time.Since(began)
		return func() {
			if gen != a.bucketsGen {
				return
			}
			a.bucketsLoad = false
			op := opEntry{what: tookBuckets, ran: true, d: took, account: id, accountName: acct.Name}
			if err != nil {
				a.bucketsErr = a.noteErr(err, "buckets", func() {
					if a.accountID == id {
						a.loadBuckets(openFirst)
					}
				})
				switch {
				case isLocked(err):
					a.bucketsFix = fixUnlock
				case cannotListBuckets(err) && len(acct.Buckets) == 0:
					a.bucketsFix = fixNameBuckets // the key may not list buckets: offer to name them
				}
				op.state, op.err = opFailed, a.bucketsErr
				a.noteOp(op)
				return
			}
			a.buckets = bs
			a.noteOp(op)
			if openFirst && len(bs) > 0 && a.scope.Bucket == "" && a.tool == "" {
				a.openBucket(bs[0].Name)
			}
		}
	})
}

func (a *app) clearListing() {
	if a.cancelList != nil {
		a.cancelList()
	}
	a.listGen++
	a.raw, a.rows, a.byKey = nil, nil, nil
	a.loading, a.listErr, a.rowsDirty = false, "", false
	a.selectAfter = ""
	a.sel.Clear()
	a.cursor = -1
	a.resetHead()
}

// cannotListBuckets reports the refusal a key without s3:ListAllMyBuckets gets
// from ListBuckets. Other refusals (a wrong key, an expired token) carry their
// own codes, and naming buckets would not help them.
func cannotListBuckets(err error) bool {
	var oe *core.OpError
	return errors.As(err, &oe) && oe.Code == "AccessDenied"
}

// namedFolder is the folder the account names for a bucket (Account.Buckets):
// where the bucket opens. "" is the root, as for every bucket the service lists.
func (a *app) namedFolder(name string) string {
	for _, b := range a.account().Buckets {
		if b.Name == name {
			return b.Prefix
		}
	}
	return ""
}

// homeFolder is the top of what the window shows of the current bucket: the
// folder the account names for it while the scope is in or below that folder,
// else the bucket root. The key may not see above it, so the breadcrumb starts
// there and Up stops there.
func (a *app) homeFolder() string {
	if h := a.namedFolder(a.scope.Bucket); h != "" && strings.HasPrefix(a.scope.Prefix, h) {
		return h
	}
	return ""
}

func (a *app) openBucket(name string) {
	a.tool = ""
	a.objOps.flat = false
	a.navSel = "b:" + name
	a.openScope(a.bucketRoot(name))
}

// openScope navigates to scope (a folder or the bucket root).
func (a *app) openScope(s Scope) {
	a.scope = s
	a.search = ""
	a.clearListing()
	a.listScope()
}

func (a *app) openPrefix(prefix string) {
	s := a.scope
	s.Prefix = prefix
	a.openScope(s)
}

// canGoUp reports whether there is a parent folder to go to: false at the
// bucket root, and at the folder the account names for the bucket.
func (a *app) canGoUp() bool { return a.scope.Prefix != a.homeFolder() }

// up goes to the parent folder; false where canGoUp is false.
func (a *app) up() bool {
	if !a.canGoUp() {
		return false
	}
	parts := splitPrefix(a.scope.Prefix)
	p := strings.Join(parts[:len(parts)-1], "/")
	if p != "" {
		p += "/"
	}
	a.openPrefix(p)
	return true
}
