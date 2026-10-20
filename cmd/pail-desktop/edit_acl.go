package main

import (
	"strings"
	"time"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/egoist/mygo/ui"
)

// aclForm is the Permissions tab of a single object.
type aclForm struct {
	key      string
	gen      int
	state    int // 0 none, 1 loading, 2 ready, 3 failed
	acl      core.ACL
	err      string
	unsupp   bool // the bucket or provider has no ACLs
	canned   string
	applying bool
}

// cannedChoices are the canned ACLs a user can pick, labelled in the language
// the app shows.
func cannedChoices() []choice {
	return []choice{
		{"private", locales.EditAclPrivate()},
		{"public-read", locales.EditAclPublicRead()},
		{"public-read-write", locales.EditAclPublicReadWrite()},
		{"authenticated-read", locales.EditAclAuthenticatedRead()},
		{"bucket-owner-full-control", locales.EditAclBucketOwnerFullControl()},
	}
}

const (
	groupAllUsers  = "http://acs.amazonaws.com/groups/global/AllUsers"
	groupAuthUsers = "http://acs.amazonaws.com/groups/global/AuthenticatedUsers"
	groupLogs      = "http://acs.amazonaws.com/groups/s3/LogDelivery"
)

// cannedOf names the canned ACL the grants amount to, or "" for custom grants.
func cannedOf(acl core.ACL) string {
	var all, auth []string
	other := 0
	owner := false
	for _, g := range acl.Grants {
		switch {
		case g.Type == "Group" && g.Grantee == groupAllUsers:
			all = append(all, g.Permission)
		case g.Type == "Group" && g.Grantee == groupAuthUsers:
			auth = append(auth, g.Permission)
		case g.Type == "CanonicalUser" && g.Grantee == acl.Owner && g.Permission == "FULL_CONTROL":
			owner = true
		default:
			other++
		}
	}
	if other > 0 || !owner {
		return ""
	}
	has := func(l []string, p string) bool {
		for _, x := range l {
			if x == p {
				return true
			}
		}
		return false
	}
	switch {
	case len(all) == 0 && len(auth) == 0:
		return "private"
	case len(auth) == 0 && len(all) == 1 && has(all, "READ"):
		return "public-read"
	case len(auth) == 0 && len(all) == 2 && has(all, "READ") && has(all, "WRITE"):
		return "public-read-write"
	case len(all) == 0 && len(auth) == 1 && has(auth, "READ"):
		return "authenticated-read"
	}
	return ""
}

func granteeLabel(g core.Grant, owner string) string {
	switch {
	case g.Type == "Group" && g.Grantee == groupAllUsers:
		return locales.EditAclEveryone()
	case g.Type == "Group" && g.Grantee == groupAuthUsers:
		return locales.EditAclAuthUsers()
	case g.Type == "Group" && g.Grantee == groupLogs:
		return locales.EditAclLogDelivery()
	case g.Type == "CanonicalUser" && g.Grantee == owner:
		return locales.EditAclOwner()
	case g.Type == "Group":
		return g.Grantee[strings.LastIndexByte(g.Grantee, '/')+1:]
	}
	return g.Grantee
}

func permissionLabel(p string) string {
	switch p {
	case "READ":
		return locales.EditAclRead()
	case "WRITE":
		return locales.EditAclWrite()
	case "READ_ACP":
		return locales.EditAclReadPermissions()
	case "WRITE_ACP":
		return locales.EditAclWritePermissions()
	case "FULL_CONTROL":
		return locales.EditAclFullControl()
	}
	return p
}

// ensureACL loads the ACL of the selected object once its tab is open.
func (a *app) ensureACL() {
	f := &a.mg.acl
	keys := a.selectedKeys()
	if a.inspTab != 3 || len(keys) != 1 || strings.HasSuffix(keys[0], "/") || a.mg.del.on {
		return
	}
	key := keys[0]
	if f.key == key && f.state != 0 {
		return
	}
	*f = aclForm{key: key, state: 1, gen: f.gen + 1}
	gen, loc := f.gen, a.scope
	a.bg(func() func() {
		ctx, cancel := a.timeout(30 * time.Second)
		defer cancel()
		acl, err := a.svc.ACL(ctx, loc, core.ObjectRef{Key: key})
		return func() {
			if a.mg.acl.gen != gen {
				return
			}
			f := &a.mg.acl
			if err != nil {
				f.state = 3
				f.unsupp = core.KindOf(err) == core.ErrUnsupported
				f.err = plainError(err, locales.EditAclReadWhat())
				return
			}
			f.state, f.acl, f.canned = 2, acl, cannedOf(acl)
		}
	})
}

// permissionsTab is the Permissions tab: owner, grants and the canned ACL.
func (a *app) permissionsTab(c *ui.Context) {
	a.ensureACL()
	f := &a.mg.acl
	k := tk(c)
	switch f.state {
	case 0, 1:
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c).Size(14, 14)
			mutedText(c, locales.EditAclLoading())
		})
		return
	case 3:
		if f.unsupp {
			emptyBlock(c, "shield", locales.EditAclDisabled(), "", 200)
			return
		}
		errorLine(c, f.err)
		ghostButton(c, iconOf("refresh-cw"), locales.CommonRetry(), locales.CommonRetry(), false).AlignSelf(ui.Start).OnClick(func() { f.state = 0 })
		return
	}
	ui.Column(c).Gap(16).Children(func() {
		ui.Column(c).Gap(8).Children(func() {
			sectionTitle(c, locales.EditAclAccess()).FontSize(sizeBody)
			choices := cannedChoices()
			if f.canned == "" {
				choices = append([]choice{{"", locales.EditAclCustom()}}, choices...)
			}
			formFieldHelp(c, locales.EditAclCanned(), locales.EditAclCannedHelp(), "", func() {
				if selectBox(c, locales.EditAclCanned(), &f.canned, choices, 0) {
					a.chooseCanned(f.canned)
				}
			})
			if f.err != "" {
				errorLine(c, f.err)
			}
		})
		ui.Column(c).Gap(8).Children(func() {
			sectionTitle(c, locales.EditAclGrants()).FontSize(sizeBody)
			kvRow(c, locales.EditAclOwner(), f.acl.Owner, true, kvCopy{locales.EditAclCopyOwnerId(), locales.EditAclOwnerCopied()})
			if len(f.acl.Grants) == 0 {
				mutedText(c, locales.CommonNone())
			}
			for i, g := range f.acl.Grants {
				ui.Row(c.Key(i)).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, granteeLabel(g, f.acl.Owner)).FontSize(sizeBody).TextColor(k.Text).SingleLine().Grow(1).MinWidth(0)
					ui.Text(c, permissionLabel(g.Permission)).FontSize(sizeSecondary).TextColor(k.TextMuted).SingleLine().Shrink(0)
				})
			}
		})
	})
}

// chooseCanned applies a canned ACL. Making the object public asks first.
func (a *app) chooseCanned(canned string) {
	f := &a.mg.acl
	if canned == "" || f.applying {
		return
	}
	loc, key := a.scope, f.key
	apply := func() { a.applyCanned(loc, key, canned) }
	if canned == "public-read" || canned == "public-read-write" {
		msg := locales.EditAclPublicReadMsg()
		if canned == "public-read-write" {
			msg = locales.EditAclPublicRWMsg()
		}
		rows := append(a.scopeRows(loc), KV{locales.EditAclObject(), key})
		a.confirm = confirmState{open: true, title: locales.EditAclMakePublicTitle(), rows: rows, message: msg, action: locales.EditAclMakePublic(), run: apply}
		// Show what the ACL is until the user confirms.
		f.canned = cannedOf(f.acl)
		return
	}
	apply()
}

func (a *app) applyCanned(loc Scope, key, canned string) {
	f := &a.mg.acl
	f.applying, f.err = true, ""
	gen := f.gen
	a.bg(func() func() {
		ctx, cancel := a.shortCtx()
		defer cancel()
		err := a.svc.SetACL(ctx, loc, core.ObjectRef{Key: key}, core.ACL{Canned: canned})
		var acl core.ACL
		var rerr error
		if err == nil {
			acl, rerr = a.svc.ACL(ctx, loc, core.ObjectRef{Key: key})
		}
		return func() {
			f := &a.mg.acl
			f.applying = false
			if f.gen != gen {
				return
			}
			if err != nil {
				f.err = plainError(err, locales.EditAclChangeWhat())
				f.canned = cannedOf(f.acl)
				return
			}
			if rerr == nil {
				f.acl, f.canned = acl, cannedOf(acl)
			}
			a.toast(locales.EditAclUpdated())
		}
	})
}
