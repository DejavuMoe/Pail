package app

import (
	"strings"
	"testing"
)

func TestTagsRoundTrip(t *testing.T) {
	e := newS3Env(t)
	bkSeed(e, "bkt", false, "k")
	loc := e.loc("bkt", "")
	if err := e.s.SetTags(testCtx(t), loc, ObjectRef{Key: "k"}, map[string]string{"env": "prod", "team": "core"}); err != nil {
		t.Fatal(err)
	}
	info, err := e.s.Head(testCtx(t), loc, "k", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Tags) != 2 || info.Tags["env"] != "prod" || info.Tags["team"] != "core" {
		t.Fatalf("tags = %v", info.Tags)
	}
	// replaced, not merged
	if err := e.s.SetTags(testCtx(t), loc, ObjectRef{Key: "k"}, map[string]string{"only": "one"}); err != nil {
		t.Fatal(err)
	}
	info, _ = e.s.Head(testCtx(t), loc, "k", "")
	if len(info.Tags) != 1 || info.Tags["only"] != "one" {
		t.Fatalf("tags = %v", info.Tags)
	}
	// empty clears
	if err := e.s.SetTags(testCtx(t), loc, ObjectRef{Key: "k"}, nil); err != nil {
		t.Fatal(err)
	}
	info, _ = e.s.Head(testCtx(t), loc, "k", "")
	if len(info.Tags) != 0 {
		t.Fatalf("tags = %v", info.Tags)
	}
	if err := e.s.SetTags(testCtx(t), loc, ObjectRef{Key: "absent"}, map[string]string{"a": "b"}); !IsKind(err, ErrNotFound) {
		t.Fatalf("missing key: %v", err)
	}
}

func TestTagsLimits(t *testing.T) {
	e := newS3Env(t)
	bkSeed(e, "bkt", false, "k")
	loc := e.loc("bkt", "")
	many := map[string]string{}
	for i := 0; i < 11; i++ {
		many[string(rune('a'+i))] = "v"
	}
	ten := map[string]string{}
	for i := 0; i < 10; i++ {
		ten[string(rune('a'+i))] = "v"
	}
	long := strings.Repeat("é", 129) // 129 characters, 258 bytes
	cases := map[string]map[string]string{
		"11 tags":       many,
		"empty key":     {"": "v"},
		"key 129":       {long: "v"},
		"value 257":     {"k": strings.Repeat("v", 257)},
		"reserved aws:": {"aws:x": "v"},
	}
	for name, tags := range cases {
		if err := e.s.SetTags(testCtx(t), loc, ObjectRef{Key: "k"}, tags); !IsKind(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	ok := map[string]map[string]string{
		"10 tags":   ten,
		"key 128":   {strings.Repeat("k", 128): strings.Repeat("v", 256)},
		"unicode":   {strings.Repeat("é", 128): "värde"},
		"value ''":  {"k": ""},
		"spaces/ +": {"a b": "c+d"},
	}
	for name, tags := range ok {
		if err := e.s.SetTags(testCtx(t), loc, ObjectRef{Key: "k"}, tags); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestTagsOnSpecificVersion(t *testing.T) {
	e := newS3Env(t)
	e.srv.CreateBucket("bkt", true)
	v1 := e.srv.PutObject("bkt", "k", []byte("one"), "text/plain", nil)
	e.srv.PutObject("bkt", "k", []byte("two"), "text/plain", nil)
	loc := e.loc("bkt", "")
	if err := e.s.SetTags(testCtx(t), loc, ObjectRef{Key: "k", VersionID: v1}, map[string]string{"old": "yes"}); err != nil {
		t.Fatal(err)
	}
	old, err := e.s.Head(testCtx(t), loc, "k", v1)
	if err != nil || old.Tags["old"] != "yes" {
		t.Fatalf("old version tags = %v, %v", old.Tags, err)
	}
	cur, _ := e.s.Head(testCtx(t), loc, "k", "")
	if len(cur.Tags) != 0 {
		t.Fatalf("current version must be untouched: %v", cur.Tags)
	}
	if err := e.s.SetTags(testCtx(t), loc, ObjectRef{Key: "k", VersionID: "nope"}, map[string]string{"a": "b"}); !IsKind(err, ErrNotFound) {
		t.Fatalf("unknown version: %v", err)
	}
}

func TestACLRoundTrip(t *testing.T) {
	e := newS3Env(t)
	bkSeed(e, "bkt", false, "k")
	loc := e.loc("bkt", "")
	ref := ObjectRef{Key: "k"}

	acl, err := e.s.ACL(testCtx(t), loc, ref)
	if err != nil {
		t.Fatal(err)
	}
	if acl.Owner == "" || len(acl.Grants) != 1 || acl.Grants[0].Permission != "FULL_CONTROL" || acl.Grants[0].Type != "CanonicalUser" || acl.Grants[0].Grantee != acl.Owner {
		t.Fatalf("default acl = %+v", acl)
	}
	if err := e.s.SetACL(testCtx(t), loc, ref, ACL{Canned: "public-read"}); err != nil {
		t.Fatal(err)
	}
	acl, _ = e.s.ACL(testCtx(t), loc, ref)
	if len(acl.Grants) != 2 || acl.Grants[1].Type != "Group" || !strings.HasSuffix(acl.Grants[1].Grantee, "/AllUsers") || acl.Grants[1].Permission != "READ" {
		t.Fatalf("public-read = %+v", acl)
	}
	// explicit grants; the owner is looked up when omitted
	const group = "http://acs.amazonaws.com/groups/global/AuthenticatedUsers"
	err = e.s.SetACL(testCtx(t), loc, ref, ACL{Grants: []Grant{
		{Grantee: acl.Owner, Type: "CanonicalUser", Permission: "FULL_CONTROL"},
		{Grantee: group, Type: "Group", Permission: "READ"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := e.s.ACL(testCtx(t), loc, ref)
	if got.Owner != acl.Owner || len(got.Grants) != 2 || got.Grants[1].Grantee != group || got.Grants[1].Permission != "READ" || got.Grants[1].Type != "Group" {
		t.Fatalf("grants = %+v", got)
	}
	// back to private
	if err := e.s.SetACL(testCtx(t), loc, ref, ACL{Canned: "private"}); err != nil {
		t.Fatal(err)
	}
	got, _ = e.s.ACL(testCtx(t), loc, ref)
	if len(got.Grants) != 1 {
		t.Fatalf("private = %+v", got)
	}
}

func TestACLValidationAndErrors(t *testing.T) {
	e := newS3Env(t)
	bkSeed(e, "bkt", false, "k")
	loc := e.loc("bkt", "")
	ref := ObjectRef{Key: "k"}
	bad := map[string]ACL{
		"empty":           {},
		"unknown canned":  {Canned: "world-writable"},
		"unknown perm":    {Grants: []Grant{{Grantee: "x", Type: "CanonicalUser", Permission: "ALL"}}},
		"unknown type":    {Grants: []Grant{{Grantee: "x", Type: "Robot", Permission: "READ"}}},
		"missing grantee": {Grants: []Grant{{Type: "CanonicalUser", Permission: "READ"}}},
	}
	for name, acl := range bad {
		if err := e.s.SetACL(testCtx(t), loc, ref, acl); !IsKind(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := e.s.ACL(testCtx(t), loc, ObjectRef{Key: "absent"}); !IsKind(err, ErrNotFound) {
		t.Errorf("absent: %v", err)
	}
	// unsupported provider: a typed error, not a crash
	e.srv.SetUnsupported("acl")
	if _, err := e.s.ACL(testCtx(t), loc, ref); !IsKind(err, ErrUnsupported) {
		t.Errorf("ACL on unsupported: %v", err)
	}
	if err := e.s.SetACL(testCtx(t), loc, ref, ACL{Canned: "private"}); !IsKind(err, ErrUnsupported) {
		t.Errorf("SetACL on unsupported: %v", err)
	}
}

func TestACLOnSpecificVersion(t *testing.T) {
	e := newS3Env(t)
	e.srv.CreateBucket("bkt", true)
	v1 := e.srv.PutObject("bkt", "k", []byte("one"), "text/plain", nil)
	e.srv.PutObject("bkt", "k", []byte("two"), "text/plain", nil)
	loc := e.loc("bkt", "")
	if err := e.s.SetACL(testCtx(t), loc, ObjectRef{Key: "k", VersionID: v1}, ACL{Canned: "public-read"}); err != nil {
		t.Fatal(err)
	}
	old, _ := e.s.ACL(testCtx(t), loc, ObjectRef{Key: "k", VersionID: v1})
	cur, _ := e.s.ACL(testCtx(t), loc, ObjectRef{Key: "k"})
	if len(old.Grants) != 2 || len(cur.Grants) != 1 {
		t.Fatalf("old %+v current %+v", old, cur)
	}
}
