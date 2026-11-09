package app

import (
	"net/http"
	"strings"
	"testing"

	"github.com/DejavuMoe/Pail/internal/s3test"
)

func TestParseAccountBuckets(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []AccountBucket
		err  string // substring; "" = valid
	}{
		{"none", nil, nil, ""},
		{"blank entries dropped", []string{" ", ""}, nil, ""},
		{"name only", []string{"data"}, []AccountBucket{{Name: "data"}}, ""},
		{"name with folder", []string{"data/reports/"}, []AccountBucket{{Name: "data", Prefix: "reports/"}}, ""},
		{"folder without slash", []string{"data/reports"}, []AccountBucket{{Name: "data", Prefix: "reports/"}}, ""},
		{"nested folder", []string{"data/a/b/"}, []AccountBucket{{Name: "data", Prefix: "a/b/"}}, ""},
		{"trimmed", []string{"  data / reports/ "}, []AccountBucket{{Name: "data", Prefix: "reports/"}}, ""},
		{"two buckets in order", []string{"zeta", "alpha/x/"}, []AccountBucket{{Name: "zeta"}, {Name: "alpha", Prefix: "x/"}}, ""},
		{"same bucket twice", []string{"data/a/", "data/b/"}, nil, "twice"},
		{"bad name", []string{"Data"}, nil, "Data"},
		{"empty folder segment", []string{"data/a//b/"}, nil, "folder"},
		{"dot segment", []string{"data/../b/"}, nil, "folder"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseAccountBuckets(c.in)
			if c.err != "" {
				if err == nil || !strings.Contains(err.Error(), c.err) {
					t.Fatalf("err = %v, want it to mention %q", err, c.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(c.want) {
				t.Fatalf("got %+v, want %+v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("got %+v, want %+v", got, c.want)
				}
			}
		})
	}
}

func TestAccountBucketString(t *testing.T) {
	if s := (AccountBucket{Name: "data"}).String(); s != "data" {
		t.Fatalf("got %q", s)
	}
	if s := (AccountBucket{Name: "data", Prefix: "reports/"}).String(); s != "data/reports/" {
		t.Fatalf("got %q", s)
	}
}

// restrictedServer is an s3test server behaving like a key whose policy
// allows only the folder dir/ of the bucket data: ListBuckets and a listing of
// the bucket root are denied, a listing of dir/ is allowed.
func restrictedServer(t *testing.T) *s3test.Server {
	t.Helper()
	srv := s3test.New()
	t.Cleanup(srv.Close)
	srv.CreateBucket("data", false)
	srv.PutObject("data", "dir/a.txt", []byte("a"), "text/plain", nil)
	denied := &s3test.Fault{Status: 403, Code: "AccessDenied", Message: "Access Denied"}
	srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.URL.Path == "/" {
			return denied // ListBuckets
		}
		q := r.URL.Query()
		if q.Get("list-type") == "2" && !strings.HasPrefix(q.Get("prefix"), "dir/") {
			return denied
		}
		return nil
	})
	return srv
}

func listBucketsCalls(srv *s3test.Server) int {
	n := 0
	for _, r := range srv.Requests() {
		if r.Method == http.MethodGet && r.Path == "/" {
			n++
		}
	}
	return n
}

func TestNamedBucketsReplaceTheListing(t *testing.T) {
	srv := restrictedServer(t)
	kr := newTestKeyring()
	s := newTestService(t, kr)
	ctx := testCtx(t)
	sec := &Secrets{SecretAccessKey: "sk"}

	// The injection is real: without named buckets the key is refused.
	plain := staticAccount("plain", srv.URL)
	if err := s.TestAccount(ctx, plain, sec); !IsKind(err, ErrAccessDenied) {
		t.Fatalf("TestAccount without named buckets: %v", err)
	}
	saved, err := s.SaveAccount(plain, sec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Buckets(ctx, saved.ID); !IsKind(err, ErrAccessDenied) {
		t.Fatalf("Buckets without named buckets: %v", err)
	}

	// Named buckets: the connection test lists the named folder, not the account.
	named := staticAccount("named", srv.URL)
	named.Buckets = []AccountBucket{{Name: "data", Prefix: "dir/"}}
	srv.ResetRequests()
	if err := s.TestAccount(ctx, named, sec); err != nil {
		t.Fatalf("TestAccount with named buckets: %v", err)
	}
	if listBucketsCalls(srv) != 0 {
		t.Fatal("TestAccount with named buckets must not call ListBuckets")
	}
	// A named bucket the key may not list at that folder fails the test.
	root := named
	root.Buckets = []AccountBucket{{Name: "data"}}
	if err := s.TestAccount(ctx, root, sec); !IsKind(err, ErrAccessDenied) {
		t.Fatalf("TestAccount at a denied folder: %v", err)
	}

	saved, err = s.SaveAccount(named, sec)
	if err != nil {
		t.Fatal(err)
	}
	srv.ResetRequests()
	bs, err := s.Buckets(ctx, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(bs) != 1 || bs[0].Name != "data" || bs[0].Prefix != "dir/" || bs[0].Region != "" {
		t.Fatalf("Buckets = %+v", bs)
	}
	if listBucketsCalls(srv) != 0 {
		t.Fatal("Buckets with named buckets must not call ListBuckets")
	}
	// The folder the account names is listable with the saved client.
	if _, err := s.List(ctx, Location{AccountID: saved.ID, Bucket: "data", Prefix: "dir/"}, ListOptions{}); err != nil {
		t.Fatal(err)
	}

	// Accounts() hands out copies, and the setting survives a restart.
	got := s.Accounts()
	var acct Account
	for _, a := range got {
		if a.ID == saved.ID {
			acct = a
		}
	}
	if len(acct.Buckets) != 1 || acct.Buckets[0] != (AccountBucket{Name: "data", Prefix: "dir/"}) {
		t.Fatalf("Accounts() buckets = %+v", acct.Buckets)
	}
	acct.Buckets[0].Name = "changed"
	if rec, _ := s.account(saved.ID); rec.Buckets[0].Name != "data" {
		t.Fatal("Accounts() shares its Buckets slice with the saved record")
	}
	s2, _ := newTestServiceDir(t, s.dir, kr)
	for _, a := range s2.Accounts() {
		if a.ID == saved.ID && (len(a.Buckets) != 1 || a.Buckets[0].Prefix != "dir/") {
			t.Fatalf("reloaded buckets = %+v", a.Buckets)
		}
	}

	// Saving normalises what the caller passes.
	loose := staticAccount("loose", srv.URL)
	loose.Buckets = []AccountBucket{{Name: " data ", Prefix: "dir"}}
	if saved, err := s.SaveAccount(loose, sec); err != nil || saved.Buckets[0] != (AccountBucket{Name: "data", Prefix: "dir/"}) {
		t.Fatalf("saved = %+v, err = %v", saved.Buckets, err)
	}
	bad := staticAccount("bad", srv.URL)
	bad.Buckets = []AccountBucket{{Name: "data"}, {Name: "data", Prefix: "x/"}}
	if _, err := s.SaveAccount(bad, sec); !IsKind(err, ErrInvalid) {
		t.Fatalf("duplicate named bucket: %v", err)
	}
}
