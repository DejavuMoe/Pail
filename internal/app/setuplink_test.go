package app

import (
	"reflect"
	"strings"
	"testing"
)

func TestSetupLinkRoundTrip(t *testing.T) {
	in := SetupLink{
		Name: "Quant data", Provider: ProviderS3, Endpoint: "https://s3.example.com", Region: "ap-northeast-1",
		PathStyle: false, Credentials: CredStatic, AccessKeyID: "AKIAEXAMPLE", SecretAccessKey: " s3cr+t/&= ",
		RoleARN: "arn:aws:iam::123456789012:role/read", ExternalID: "ext", RoleSessionName: "quant",
		ChecksumMode: "when_required", RequesterPays: true, CDNURL: "https://cdn.example.com",
		CDNBuckets: []string{"a", "b"}, Buckets: []string{"raw", "derived/daily/"},
	}
	raw := in.URL()
	if !strings.HasPrefix(raw, "pail://account?") || !strings.Contains(raw, "v=1") {
		t.Fatalf("URL = %s", raw)
	}
	out, err := ParseSetupLink(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip:\n in  %+v\n out %+v", in, out)
	}
}

func TestSetupLinkFromLeavesKeysOut(t *testing.T) {
	ac := Account{
		ID: "x", Name: "Prod", Provider: ProviderAWS, Region: "us-west-2", Credentials: CredStatic,
		AccessKeyID: "AKIAOWN", InsecureSkipVerify: true, ProxyMode: ProxyCustom, ProxyURL: "socks5://127.0.0.1:1080",
		BucketAliases: map[string]string{"b": "B"}, Buckets: []AccountBucket{{Name: "b", Prefix: "in/"}},
	}
	raw := SetupLinkFrom(ac).URL()
	for _, banned := range []string{"AKIAOWN", "ak=", "sk=", "socks5", "proxy", "insecure", "alias"} {
		if strings.Contains(raw, banned) {
			t.Errorf("link %s contains %q", raw, banned)
		}
	}
	l, err := ParseSetupLink(raw)
	if err != nil {
		t.Fatal(err)
	}
	if l.Name != "Prod" || l.Region != "us-west-2" || l.Credentials != CredStatic || len(l.Buckets) != 1 || l.Buckets[0] != "b/in/" {
		t.Fatalf("parsed %+v", l)
	}
	if l.HasKeys() {
		t.Fatal("keys in a link made from an account")
	}
}

func TestSetupLinkProfile(t *testing.T) {
	l := SetupLinkFrom(Account{Name: "Dev", Provider: ProviderAWS, Credentials: CredProfile, Profile: "quant", AccessKeyID: "stale"})
	got, err := ParseSetupLink(l.URL())
	if err != nil {
		t.Fatal(err)
	}
	if got.Credentials != CredProfile || got.Profile != "quant" || got.AccessKeyID != "" {
		t.Fatalf("parsed %+v", got)
	}
	// Keys in a link for profile credentials are ignored.
	got, _ = ParseSetupLink("pail://account?creds=profile&profile=p&ak=A&sk=S")
	if got.HasKeys() {
		t.Fatalf("keys read for a profile: %+v", got)
	}
}

func TestParseSetupLinkLenient(t *testing.T) {
	for _, c := range []struct {
		raw  string
		want SetupLink
	}{
		// Unknown values fall back to the defaults; unknown parameters are ignored.
		{"pail://account?provider=gcs&creds=magic&checksum=sometimes&future=1&name=%20N%20",
			SetupLink{Name: "N", Provider: ProviderAWS}},
		// An endpoint without a provider is an S3-compatible service, path-style by default.
		{"<pail://account?endpoint=https://minio.local:9000>",
			SetupLink{Provider: ProviderS3, Endpoint: "https://minio.local:9000", PathStyle: true}},
		// AWS accounts have no endpoint.
		{"PAIL://ACCOUNT?provider=aws&endpoint=https://x&path=1",
			SetupLink{Provider: ProviderAWS}},
		{"pail:account?name=A", SetupLink{Name: "A", Provider: ProviderAWS}},
		{"pail:///account?name=A", SetupLink{Name: "A", Provider: ProviderAWS}},
		{"pail://account?name=A&bad=%zz", SetupLink{Name: "A", Provider: ProviderAWS}},
	} {
		got, err := ParseSetupLink(c.raw)
		if err != nil {
			t.Errorf("%s: %v", c.raw, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got  %+v\n want %+v", c.raw, got, c.want)
		}
	}
}

func TestParseSetupLinkRejects(t *testing.T) {
	for _, raw := range []string{
		"", "https://pail.download/account?name=A", "pail://bucket?name=A", "pail://", "not a url",
		"pail://account?" + strings.Repeat("a", maxSetupLink),
	} {
		if _, err := ParseSetupLink(raw); err != ErrNotSetupLink {
			t.Errorf("%.60q: err = %v", raw, err)
		}
	}
}
