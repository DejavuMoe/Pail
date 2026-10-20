package cloud

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/DejavuMoe/Pail/internal/storage"
)

func TestNormalizeInvalidationPaths(t *testing.T) {
	got, err := NormalizeInvalidationPaths([]string{"/b", "/a/*", "/b", "/你好 空格", "/x?version=1", "no-slash/file", " /trim "})
	want := []string{"/%E4%BD%A0%E5%A5%BD%20%E7%A9%BA%E6%A0%BC", "/a/*", "/b", "/no-slash/file", "/trim", "/x?version=1"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("paths=%v err=%v", got, err)
	}
	// Existing escapes are preserved, not double-encoded.
	if got, err = NormalizeInvalidationPaths([]string{"/a%20b/*"}); err != nil || got[0] != "/a%20b/*" {
		t.Fatalf("%v %v", got, err)
	}
	if got, err = NormalizeInvalidationPaths([]string{"/*"}); err != nil || got[0] != "/*" {
		t.Fatalf("%v %v", got, err)
	}
	bad := [][]string{nil, {}, {""}, {"/a*tail"}, {"/a*/b"}, {"/a**"}, {"*/a"}, {"/bad#fragment"}, {"/bad%zz"}, {"/bad\n"}, {"/bad\x7f"},
		{`/bad\path`}, {"/" + strings.Repeat("x", 4000)}, {"https://example.com/a"}, {"//host/a"}, {"/x?token=%zz"}, {"/x?name=你好"}, {"/x?a b"}, {"\xff"}}
	for _, values := range bad {
		if _, err := NormalizeInvalidationPaths(values); err == nil {
			t.Errorf("accepted paths %q", values)
		}
	}
	if _, err := NormalizeInvalidationPaths([]string{"/" + strings.Repeat("x", 3999)}); err != nil {
		t.Errorf("4000-byte path rejected: %v", err)
	}
	many := make([]string, 3001)
	for i := range many {
		many[i] = fmt.Sprintf("/p%d", i)
	}
	if _, err := NormalizeInvalidationPaths(many); err == nil {
		t.Error("3001 paths accepted")
	}
	if got, err := NormalizeInvalidationPaths(many[:3000]); err != nil || len(got) != 3000 {
		t.Errorf("3000 paths: %d %v", len(got), err)
	}
}

func TestOriginMatchesBucket(t *testing.T) {
	for _, host := range []string{"assets.s3.amazonaws.com", "assets.s3.us-east-1.amazonaws.com", "assets.s3-us-west-2.amazonaws.com",
		"assets.s3.dualstack.eu-west-1.amazonaws.com", "assets.s3.cn-north-1.amazonaws.com.cn", "ASSETS.S3.AMAZONAWS.COM.",
		"assets.s3-website-us-east-1.amazonaws.com", "assets.s3-website.us-east-1.amazonaws.com"} {
		if !OriginMatchesBucket(host, "assets") {
			t.Errorf("valid S3 origin rejected: %s", host)
		}
	}
	for _, host := range []string{"assets.s3.amazonaws.com.attacker.invalid", "other.s3.amazonaws.com", "assets.example.com", "xassets.s3.amazonaws.com",
		"assets.s3.amazonaws.com.cn.evil", "assets.s3.Bad_Region.amazonaws.com", "assets.s3x.amazonaws.com", "evil.assets.s3.amazonaws.com", ""} {
		if OriginMatchesBucket(host, "assets") {
			t.Errorf("non-matching origin accepted: %s", host)
		}
	}
	if !OriginMatchesBucket("my.dotted.bucket.s3.amazonaws.com", "my.dotted.bucket") || OriginMatchesBucket("myXdottedXbucket.s3.amazonaws.com", "my.dotted.bucket") {
		t.Error("dots in bucket names must be literal")
	}
	if OriginMatchesBucket("x.s3.amazonaws.com", "") {
		t.Error("empty bucket matched")
	}
}

func TestDistributionsForBucket(t *testing.T) {
	ds := []Distribution{
		{ID: "A", Origins: []Origin{{DomainName: "assets.s3.amazonaws.com"}}},
		{ID: "B", Origins: []Origin{{DomainName: "api.example.com"}, {DomainName: "assets.s3.eu-west-1.amazonaws.com"}}},
		{ID: "C", Origins: []Origin{{DomainName: "assets.s3-website-us-east-1.amazonaws.com"}}},
		{ID: "D", Origins: []Origin{{DomainName: "other.s3.amazonaws.com"}}},
		{ID: "E"},
	}
	var ids []string
	for _, d := range DistributionsForBucket("assets", ds) {
		ids = append(ids, d.ID)
	}
	if strings.Join(ids, ",") != "A,B,C" {
		t.Fatalf("matched %v", ids)
	}
	if DistributionsForBucket("", ds) != nil || DistributionsForBucket("nothing", ds) != nil {
		t.Fatal("unexpected match")
	}
}

func mockClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	c, err := New(context.Background(), storage.Config{AccessKeyID: "MOCKKEY", SecretAccessKey: "mock-secret", Endpoint: "http://ignored.invalid", HTTPClient: server.Client()}, WithBaseEndpoint(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func xml(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "text/xml")
	fmt.Fprint(w, body)
}

func TestListDistributionsPaginationAndMapping(t *testing.T) {
	calls := 0
	c := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || !strings.HasSuffix(r.URL.Path, "/distribution") || !strings.Contains(r.Header.Get("Authorization"), "Credential=MOCKKEY/") || !strings.Contains(r.Header.Get("Authorization"), "/cloudfront/aws4_request") {
			t.Errorf("unexpected request %s %s %s", r.Method, r.URL, r.Header.Get("Authorization"))
		}
		if r.URL.Query().Get("Marker") == "" {
			xml(w, `<DistributionList><Marker></Marker><NextMarker>m2</NextMarker><MaxItems>1</MaxItems><IsTruncated>true</IsTruncated><Quantity>1</Quantity><Items><DistributionSummary><Id>E1</Id><ARN>arn</ARN><Status>Deployed</Status><LastModifiedTime>2024-01-01T00:00:00Z</LastModifiedTime><DomainName>d1.cloudfront.net</DomainName><Aliases><Quantity>1</Quantity><Items><CNAME>www.example.com</CNAME></Items></Aliases><Origins><Quantity>1</Quantity><Items><Origin><Id>o1</Id><DomainName>assets.s3.amazonaws.com</DomainName><OriginPath>/v1</OriginPath></Origin></Items></Origins><Enabled>true</Enabled><Comment>main</Comment></DistributionSummary></Items></DistributionList>`)
			return
		}
		if r.URL.Query().Get("Marker") != "m2" {
			t.Errorf("marker=%q", r.URL.Query().Get("Marker"))
		}
		xml(w, `<DistributionList><Marker>m2</Marker><MaxItems>1</MaxItems><IsTruncated>false</IsTruncated><Quantity>1</Quantity><Items><DistributionSummary><Id>E2</Id><Status>InProgress</Status><DomainName>d2.cloudfront.net</DomainName><Enabled>false</Enabled></DistributionSummary></Items></DistributionList>`)
	})
	got, err := c.ListDistributions(context.Background())
	if err != nil || calls != 2 || len(got) != 2 {
		t.Fatalf("%v %v calls=%d", got, err, calls)
	}
	d := got[0]
	if d.ID != "E1" || d.DomainName != "d1.cloudfront.net" || !d.Enabled || d.Status != "Deployed" || d.Comment != "main" ||
		!reflect.DeepEqual(d.Aliases, []string{"www.example.com"}) || !reflect.DeepEqual(d.Origins, []Origin{{"o1", "assets.s3.amazonaws.com", "/v1"}}) {
		t.Fatalf("bad mapping: %+v", d)
	}
	if got[1].Enabled || got[1].Status != "InProgress" || len(got[1].Origins) != 0 {
		t.Fatalf("bad mapping: %+v", got[1])
	}
}

const invalidationXML = `<Invalidation><Id>I123</Id><Status>%s</Status><CreateTime>2024-05-06T07:08:09Z</CreateTime><InvalidationBatch><Paths><Quantity>2</Quantity><Items><Path>/a/*</Path><Path>/b</Path></Items></Paths><CallerReference>%s</CallerReference></InvalidationBatch></Invalidation>`

func TestCreateInvalidation(t *testing.T) {
	var body string
	c := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		if r.Method != "POST" || !strings.HasSuffix(r.URL.Path, "/distribution/EDIST/invalidation") {
			t.Errorf("unexpected %s %s", r.Method, r.URL)
		}
		w.WriteHeader(201)
		xml(w, fmt.Sprintf(invalidationXML, "InProgress", "ref-1"))
	})
	inv, err := c.CreateInvalidation(context.Background(), "EDIST", []string{"/b", "a/*", "/b"}, "ref-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<Quantity>2</Quantity>", "<Path>/a/*</Path>", "<Path>/b</Path>", "<CallerReference>ref-1</CallerReference>"} {
		if !strings.Contains(body, want) {
			t.Errorf("request lacks %s: %s", want, body)
		}
	}
	if strings.Count(body, "<Path>/b</Path>") != 1 {
		t.Error("duplicate path sent")
	}
	if inv.ID != "I123" || inv.Status != "InProgress" || inv.DistributionID != "EDIST" || inv.CallerReference != "ref-1" || !reflect.DeepEqual(inv.Paths, []string{"/a/*", "/b"}) || inv.CreateTime.Year() != 2024 {
		t.Fatalf("%+v", inv)
	}
	// Mismatched caller reference in the response is an error.
	c = mockClient(t, func(w http.ResponseWriter, r *http.Request) {
		xml(w, fmt.Sprintf(invalidationXML, "InProgress", "other"))
	})
	if _, err = c.CreateInvalidation(context.Background(), "EDIST", []string{"/a"}, "ref-1"); err == nil {
		t.Error("mismatched response accepted")
	}
	// An empty reference is generated.
	var sent string
	c = mockClient(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		sent = string(b)
		xml(w, fmt.Sprintf(invalidationXML, "Completed", "generated"))
	})
	_, _ = c.CreateInvalidation(context.Background(), "EDIST", []string{"/a"}, "")
	if !strings.Contains(sent, "<CallerReference>pail-") {
		t.Errorf("no generated caller reference: %s", sent)
	}
}

func TestInvalidationInputValidationAvoidsNetwork(t *testing.T) {
	c := mockClient(t, func(w http.ResponseWriter, r *http.Request) { t.Errorf("request sent: %s", r.URL) })
	ctx := context.Background()
	if _, err := c.CreateInvalidation(ctx, "bad/id", []string{"/a"}, ""); err == nil {
		t.Error("bad distribution accepted")
	}
	if _, err := c.CreateInvalidation(ctx, "", []string{"/a"}, ""); err == nil {
		t.Error("empty distribution accepted")
	}
	if _, err := c.CreateInvalidation(ctx, "EDIST", []string{"/a*b"}, ""); err == nil {
		t.Error("bad path accepted")
	}
	if _, err := c.GetInvalidation(ctx, "EDIST", "../x"); err == nil {
		t.Error("bad invalidation ID accepted")
	}
	if _, err := c.ListInvalidations(ctx, ""); err == nil {
		t.Error("empty distribution accepted")
	}
}

func TestGetInvalidationValidatesResponse(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		ok   bool
	}{
		"completed":      {fmt.Sprintf(invalidationXML, "Completed", "ref"), true},
		"unknown status": {fmt.Sprintf(invalidationXML, "Accepted", "ref"), false},
		"id mismatch":    {strings.Replace(fmt.Sprintf(invalidationXML, "Completed", "ref"), "I123", "OTHER", 1), false},
		"no batch":       {`<Invalidation><Id>I123</Id><Status>Completed</Status></Invalidation>`, false},
	} {
		t.Run(name, func(t *testing.T) {
			c := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || !strings.HasSuffix(r.URL.Path, "/distribution/EDIST/invalidation/I123") {
					t.Errorf("unexpected %s %s", r.Method, r.URL)
				}
				xml(w, tc.body)
			})
			inv, err := c.GetInvalidation(context.Background(), "EDIST", "I123")
			if (err == nil) != tc.ok {
				t.Fatalf("err=%v", err)
			}
			if tc.ok && (inv.Status != "Completed" || len(inv.Paths) != 2) {
				t.Fatalf("%+v", inv)
			}
		})
	}
}

func TestListInvalidationsPagination(t *testing.T) {
	calls := 0
	c := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("Marker") == "" {
			xml(w, `<InvalidationList><Marker></Marker><NextMarker>n</NextMarker><MaxItems>1</MaxItems><IsTruncated>true</IsTruncated><Quantity>1</Quantity><Items><InvalidationSummary><Id>I2</Id><CreateTime>2024-02-01T00:00:00Z</CreateTime><Status>InProgress</Status></InvalidationSummary></Items></InvalidationList>`)
			return
		}
		xml(w, `<InvalidationList><Marker>n</Marker><MaxItems>1</MaxItems><IsTruncated>false</IsTruncated><Quantity>1</Quantity><Items><InvalidationSummary><Id>I1</Id><CreateTime>2024-01-01T00:00:00Z</CreateTime><Status>Completed</Status></InvalidationSummary></Items></InvalidationList>`)
	})
	got, err := c.ListInvalidations(context.Background(), "EDIST")
	if err != nil || calls != 2 || len(got) != 2 || got[0].ID != "I2" || got[0].Status != "InProgress" || got[1].ID != "I1" || got[1].CreateTime.Month() != 1 {
		t.Fatalf("%+v %v calls=%d", got, err, calls)
	}
}

func TestCloudFrontErrorsPropagate(t *testing.T) {
	c := mockClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		xml(w, `<ErrorResponse><Error><Type>Sender</Type><Code>AccessDenied</Code><Message>no</Message></Error></ErrorResponse>`)
	})
	_, err := c.ListDistributions(context.Background())
	if storage.ErrorCode(err) != "AccessDenied" {
		t.Fatalf("typed error lost: %v", err)
	}
}

func TestNewRequiresCredentials(t *testing.T) {
	if _, err := New(context.Background(), storage.Config{}); err == nil {
		t.Fatal("implicit credentials accepted")
	}
}
