package app

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// bmCF is a tiny CloudFront fake: one distribution list and invalidations kept in memory.
type bmCF struct {
	mu     sync.Mutex
	srv    *httptest.Server
	invs   []bmCFInv
	status string
	calls  int
}

type bmCFInv struct {
	ID, Ref string
	Paths   []string
}

func (f *bmCF) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	w.Header().Set("Content-Type", "text/xml")
	inv := func(i bmCFInv, status string) string {
		var p strings.Builder
		for _, x := range i.Paths {
			p.WriteString("<Path>" + x + "</Path>")
		}
		return fmt.Sprintf(`<Invalidation><Id>%s</Id><Status>%s</Status><CreateTime>2026-01-02T03:04:05Z</CreateTime><InvalidationBatch><Paths><Quantity>%d</Quantity><Items>%s</Items></Paths><CallerReference>%s</CallerReference></InvalidationBatch></Invalidation>`,
			i.ID, status, len(i.Paths), p.String(), i.Ref)
	}
	const base = "/2020-05-31/distribution"
	switch {
	case r.Method == "GET" && r.URL.Path == base:
		fmt.Fprint(w, `<DistributionList><Marker></Marker><MaxItems>100</MaxItems><IsTruncated>false</IsTruncated><Quantity>2</Quantity><Items>`+
			`<DistributionSummary><Id>EDIST1</Id><Status>Deployed</Status><DomainName>d1.cloudfront.net</DomainName><Comment>site</Comment><Enabled>true</Enabled>`+
			`<Aliases><Quantity>1</Quantity><Items><CNAME>www.example.com</CNAME></Items></Aliases>`+
			`<Origins><Quantity>1</Quantity><Items><Origin><Id>o1</Id><DomainName>assets.s3.us-east-1.amazonaws.com</DomainName></Origin></Items></Origins></DistributionSummary>`+
			`<DistributionSummary><Id>EDIST2</Id><Status>Deployed</Status><DomainName>d2.cloudfront.net</DomainName><Enabled>false</Enabled>`+
			`<Origins><Quantity>1</Quantity><Items><Origin><Id>o2</Id><DomainName>other.s3.amazonaws.com</DomainName></Origin></Items></Origins></DistributionSummary>`+
			`</Items></DistributionList>`)
	case r.Method == "POST" && r.URL.Path == base+"/EDIST1/invalidation":
		body, _ := io.ReadAll(r.Body)
		var b struct {
			Ref   string   `xml:"CallerReference"`
			Paths []string `xml:"Paths>Items>Path"`
		}
		if err := xml.Unmarshal(body, &b); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		i := bmCFInv{ID: fmt.Sprintf("INV%d", len(f.invs)+1), Ref: b.Ref, Paths: b.Paths}
		f.invs = append(f.invs, i)
		w.WriteHeader(201)
		fmt.Fprint(w, inv(i, "InProgress"))
	case r.Method == "GET" && r.URL.Path == base+"/EDIST1/invalidation":
		var items strings.Builder
		for _, i := range f.invs {
			items.WriteString(fmt.Sprintf(`<InvalidationSummary><Id>%s</Id><CreateTime>2026-01-02T03:04:05Z</CreateTime><Status>%s</Status></InvalidationSummary>`, i.ID, f.status))
		}
		fmt.Fprintf(w, `<InvalidationList><Marker></Marker><MaxItems>100</MaxItems><IsTruncated>false</IsTruncated><Quantity>%d</Quantity><Items>%s</Items></InvalidationList>`, len(f.invs), items.String())
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, base+"/EDIST1/invalidation/"):
		id := strings.TrimPrefix(r.URL.Path, base+"/EDIST1/invalidation/")
		for _, i := range f.invs {
			if i.ID == id {
				fmt.Fprint(w, inv(i, f.status))
				return
			}
		}
		w.WriteHeader(404)
		fmt.Fprint(w, `<ErrorResponse><Error><Type>Sender</Type><Code>NoSuchInvalidation</Code><Message>The specified invalidation does not exist.</Message></Error><RequestId>r1</RequestId></ErrorResponse>`)
	default:
		w.WriteHeader(403)
		fmt.Fprint(w, `<ErrorResponse><Error><Type>Sender</Type><Code>AccessDenied</Code><Message>denied</Message></Error><RequestId>r2</RequestId></ErrorResponse>`)
	}
}

func bmNewCF(t *testing.T) *bmCF {
	t.Helper()
	f := &bmCF{status: "InProgress"}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(f.srv.Close)
	old := cloudTestEndpoint
	cloudTestEndpoint = f.srv.URL
	t.Cleanup(func() { cloudTestEndpoint = old })
	return f
}

func bmAWSAccount(t *testing.T, s *Service) Account {
	t.Helper()
	acc, err := s.SaveAccount(Account{Name: "aws", Provider: ProviderAWS, Region: "us-east-1", Credentials: CredStatic, AccessKeyID: "AKIATEST"},
		&Secrets{SecretAccessKey: "sk"})
	if err != nil {
		t.Fatal(err)
	}
	return acc
}

func TestCloudFrontDistributionsAndInvalidation(t *testing.T) {
	f := bmNewCF(t)
	s := newTestService(t, nil)
	acc := bmAWSAccount(t, s)
	ctx := testCtx(t)

	ds, err := s.Distributions(ctx, acc.ID)
	if err != nil || len(ds) != 2 || ds[0].ID != "EDIST1" || ds[0].Aliases[0] != "www.example.com" || !ds[0].Enabled || ds[1].Enabled {
		t.Fatalf("%+v %v", ds, err)
	}
	fb, err := s.DistributionsForBucket(ctx, acc.ID, "assets")
	if err != nil || len(fb) != 1 || fb[0].ID != "EDIST1" {
		t.Fatalf("%+v %v", fb, err)
	}
	if fb, _ := s.DistributionsForBucket(ctx, acc.ID, "missing"); len(fb) != 0 {
		t.Fatalf("%+v", fb)
	}

	inv, err := s.Invalidate(ctx, acc.ID, "EDIST1", []string{"b", "/a/*", "/b", "/你好 空格"})
	if err != nil || inv.ID != "INV1" || inv.Status != "InProgress" || inv.DistributionID != "EDIST1" || inv.CallerReference == "" {
		t.Fatalf("%+v %v", inv, err)
	}
	want := []string{"/%E4%BD%A0%E5%A5%BD%20%E7%A9%BA%E6%A0%BC", "/a/*", "/b"}
	f.mu.Lock()
	got := f.invs[0].Paths
	f.mu.Unlock()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paths sent %v want %v", got, want)
	}
	inv2, err := s.Invalidate(ctx, acc.ID, "EDIST1", []string{"/x"})
	if err != nil || inv2.CallerReference == inv.CallerReference {
		t.Fatalf("caller references must be unique: %+v %v", inv2, err)
	}

	got1, err := s.Invalidation(ctx, acc.ID, "EDIST1", "INV1")
	if err != nil || got1.Status != "InProgress" || !reflect.DeepEqual(got1.Paths, want) {
		t.Fatalf("%+v %v", got1, err)
	}
	f.mu.Lock()
	f.status = "Completed"
	f.mu.Unlock()
	if got1, _ = s.Invalidation(ctx, acc.ID, "EDIST1", "INV1"); got1.Status != "Completed" {
		t.Fatalf("%+v", got1)
	}
	list, err := s.Invalidations(ctx, acc.ID, "EDIST1")
	if err != nil || len(list) != 2 || list[0].Status != "Completed" {
		t.Fatalf("%+v %v", list, err)
	}
	if _, err := s.Invalidation(ctx, acc.ID, "EDIST1", "INV99"); !IsKind(err, ErrNotFound) {
		t.Fatalf("want NotFound, got %v", err)
	}
	if _, err := s.Invalidations(ctx, acc.ID, "EOTHER"); !IsKind(err, ErrAccessDenied) {
		t.Fatalf("want AccessDenied, got %v", err)
	}
}

func TestCloudFrontInvalidateRejectsBadInputBeforeNetwork(t *testing.T) {
	f := bmNewCF(t)
	s := newTestService(t, nil)
	acc := bmAWSAccount(t, s)
	ctx := testCtx(t)
	many := make([]string, 3001)
	for i := range many {
		many[i] = fmt.Sprintf("/p%d", i)
	}
	for name, run := range map[string]func() error{
		"no paths":   func() error { _, err := s.Invalidate(ctx, acc.ID, "EDIST1", nil); return err },
		"too many":   func() error { _, err := s.Invalidate(ctx, acc.ID, "EDIST1", many); return err },
		"bad path":   func() error { _, err := s.Invalidate(ctx, acc.ID, "EDIST1", []string{"/a#frag"}); return err },
		"bad wild":   func() error { _, err := s.Invalidate(ctx, acc.ID, "EDIST1", []string{"/a*b"}); return err },
		"empty dist": func() error { _, err := s.Invalidate(ctx, acc.ID, "", []string{"/a"}); return err },
		"bad dist":   func() error { _, err := s.Invalidate(ctx, acc.ID, "../x", []string{"/a"}); return err },
		"empty inv":  func() error { _, err := s.Invalidation(ctx, acc.ID, "EDIST1", ""); return err },
		"list dist":  func() error { _, err := s.Invalidations(ctx, acc.ID, "a b"); return err },
		"bucket":     func() error { _, err := s.DistributionsForBucket(ctx, acc.ID, ""); return err },
		"no account": func() error { _, err := s.Distributions(ctx, ""); return err },
		"3000 paths ok then rejected by dedupe-free limit": func() error {
			_, err := s.Invalidate(ctx, acc.ID, "EDIST1", append(many, "/one-more"))
			return err
		},
	} {
		if err := run(); !IsKind(err, ErrInvalid) {
			t.Errorf("%s: want Invalid, got %v", name, err)
		}
	}
	if f.calls != 0 {
		t.Fatalf("invalid requests reached CloudFront (%d calls)", f.calls)
	}
	// Exactly 3000 paths is accepted.
	if _, err := s.Invalidate(ctx, acc.ID, "EDIST1", many[:3000]); err != nil {
		t.Fatalf("3000 paths: %v", err)
	}
}

func TestCloudFrontRejectsNonAWSProvider(t *testing.T) {
	f := bmNewCF(t)
	e := newS3Env(t) // provider "s3"
	ctx := testCtx(t)
	check := func(name string, err error) {
		t.Helper()
		if !IsKind(err, ErrUnsupported) || !strings.Contains(err.Error(), "AWS") {
			t.Errorf("%s: want Unsupported mentioning AWS, got %v", name, err)
		}
	}
	_, err := e.s.Distributions(ctx, e.acc.ID)
	check("Distributions", err)
	_, err = e.s.DistributionsForBucket(ctx, e.acc.ID, "b")
	check("DistributionsForBucket", err)
	_, err = e.s.Invalidate(ctx, e.acc.ID, "EDIST1", []string{"/a"})
	check("Invalidate", err)
	_, err = e.s.Invalidation(ctx, e.acc.ID, "EDIST1", "INV1")
	check("Invalidation", err)
	_, err = e.s.Invalidations(ctx, e.acc.ID, "EDIST1")
	check("Invalidations", err)
	if f.calls != 0 {
		t.Fatal("CloudFront was contacted for a non-AWS account")
	}
	if _, err := e.s.Distributions(ctx, "nope"); !IsKind(err, ErrNotFound) {
		t.Fatalf("unknown account: %v", err)
	}
}
