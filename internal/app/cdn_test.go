package app

import (
	"reflect"
	"testing"
)

func TestCDNURL(t *testing.T) {
	kr := newTestKeyring()
	s, dir := newTestServiceDir(t, "", kr)
	save := func(cdn string, buckets ...string) string {
		a := staticAccount("n", "https://s3-api.example.com")
		a.CDNURL, a.CDNBuckets = cdn, buckets
		got, err := s.SaveAccount(a, &Secrets{SecretAccessKey: "x"})
		if err != nil {
			t.Fatalf("%q: %v", cdn, err)
		}
		return got.ID
	}
	none := save("")
	plain := save("https://s3-cdn.example.com")
	slash := save("https://s3-cdn.example.com///")
	withPath := save("https://h/cdn/")
	tmpl := save("https://{bucket}.cdn.example.com/{key}")
	tmplPath := save("https://cdn.example.com/static/{bucket}/v1/{key}")
	keyOnly := save("https://cdn.example.com/files/{key}")
	filtered := save("https://s3-cdn.example.com", " smoji ", "other-bucket", "smoji")

	cases := []struct {
		name, acct, bucket, key, want string
	}{
		{"plain", plain, "smoji", "smoji.json", "https://s3-cdn.example.com/smoji/smoji.json"},
		{"plain nested", plain, "smoji", "a/b/c.png", "https://s3-cdn.example.com/smoji/a/b/c.png"},
		{"trailing slashes", slash, "b-1", "k", "https://s3-cdn.example.com/b-1/k"},
		{"base path", withPath, "bkt", "k", "https://h/cdn/bkt/k"},
		{"template", tmpl, "bkt", "dir/file.txt", "https://bkt.cdn.example.com/dir/file.txt"},
		{"template path", tmplPath, "bkt", "k", "https://cdn.example.com/static/bkt/v1/k"},
		{"template key only", keyOnly, "bkt", "k", "https://cdn.example.com/files/k"},
		{"unicode", plain, "bkt", "dir/ü.txt", "https://s3-cdn.example.com/bkt/dir/%C3%BC.txt"},
		{"space plus", plain, "bkt", "a b+c.txt", "https://s3-cdn.example.com/bkt/a%20b%2Bc.txt"},
		{"hash question", plain, "bkt", "a#b?c&d%e", "https://s3-cdn.example.com/bkt/a%23b%3Fc%26d%25e"},
		{"template escapes", tmpl, "bkt", "a b#c?", "https://bkt.cdn.example.com/a%20b%23c%3F"},
		{"no cdn", none, "bkt", "k", ""},
		{"unknown account", "nope", "bkt", "k", ""},
		{"empty bucket", plain, "", "k", ""},
		{"empty key", plain, "bkt", "", ""},
		{"folder key", plain, "bkt", "dir/", ""},
		{"filter hit", filtered, "smoji", "k", "https://s3-cdn.example.com/smoji/k"},
		{"filter hit 2", filtered, "other-bucket", "k", "https://s3-cdn.example.com/other-bucket/k"},
		{"filter miss", filtered, "third", "k", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := s.CDNURL(Location{AccountID: c.acct, Bucket: c.bucket}, c.key); got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
	if !s.HasCDN(Location{AccountID: plain, Bucket: "b"}) || s.HasCDN(Location{AccountID: none, Bucket: "b"}) ||
		s.HasCDN(Location{AccountID: filtered, Bucket: "third"}) || !s.HasCDN(Location{AccountID: filtered, Bucket: "smoji"}) {
		t.Fatal("HasCDN mismatch")
	}

	// Persistence round trip: a reloaded service serves the same links.
	s2, _ := newTestServiceDir(t, dir, kr)
	if got := s2.CDNURL(Location{AccountID: plain, Bucket: "smoji"}, "smoji.json"); got != "https://s3-cdn.example.com/smoji/smoji.json" {
		t.Fatalf("after reload: %q", got)
	}
	var f Account
	for _, a := range s2.Accounts() {
		if a.ID == filtered {
			f = a
		}
	}
	if f.CDNURL != "https://s3-cdn.example.com" || !reflect.DeepEqual(f.CDNBuckets, []string{"smoji", "other-bucket"}) {
		t.Fatalf("reloaded account: %+v", f)
	}
	// Accounts() hands out copies of the bucket list.
	f.CDNBuckets[0] = "mutated"
	for _, a := range s2.Accounts() {
		if a.ID == filtered && a.CDNBuckets[0] != "smoji" {
			t.Fatal("Accounts() leaks the CDNBuckets slice")
		}
	}
	// Editing the account (no secrets) updates the CDN; clearing it removes the link.
	f.CDNURL, f.CDNBuckets = "", nil
	if _, err := s2.SaveAccount(f, nil); err != nil {
		t.Fatal(err)
	}
	if got := s2.CDNURL(Location{AccountID: filtered, Bucket: "smoji"}, "k"); got != "" {
		t.Fatalf("cleared CDN still yields %q", got)
	}
}

func TestSaveAccountRejectsInvalidCDN(t *testing.T) {
	s := newTestService(t, nil)
	for _, cdn := range []string{"ftp://x", "cdn.example.com", "https://{bucket}.x.com", "https://u@x.com", "https://x.com/?a=1"} {
		a := staticAccount("n", "https://s3.example.com")
		a.CDNURL = cdn
		if _, err := s.SaveAccount(a, &Secrets{SecretAccessKey: "x"}); err == nil {
			t.Errorf("CDN URL %q accepted", cdn)
		}
	}
	a := staticAccount("n", "https://s3.example.com")
	a.CDNBuckets = []string{"NOT_VALID"}
	if _, err := s.SaveAccount(a, &Secrets{SecretAccessKey: "x"}); err == nil {
		t.Error("invalid CDN bucket accepted")
	}
	if len(s.Accounts()) != 0 {
		t.Fatal("invalid account was saved")
	}
}
