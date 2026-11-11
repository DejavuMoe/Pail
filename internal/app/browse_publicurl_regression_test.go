package app

import (
	"net/url"
	"testing"
)

func TestPublicURLPreservesEndpointEscapes(t *testing.T) {
	for _, pathStyle := range []bool{true, false} {
		for _, base := range []string{"gateway%23v1", "gateway%3Fv1", "gateway%20v1", "gateway%2Fv1", "gateway%2F", "gateway%252Fv1", "gateway%2fv1/", "gateway/"} {
			t.Run(base+map[bool]string{true: "/path", false: "/virtual"}[pathStyle], func(t *testing.T) {
				s := newTestService(t, nil)
				a, err := s.SaveAccount(Account{Name: "n", Provider: ProviderS3, Credentials: CredAnonymous, Endpoint: "https://example.invalid:9000/" + base, PathStyle: pathStyle}, nil)
				if err != nil {
					t.Fatal(err)
				}
				got := s.PublicURL(Location{AccountID: a.ID, Bucket: "bkt"}, "dir/a #?+%.txt")
				endpoint, _ := url.Parse(a.Endpoint)
				wantPath := endpoint.EscapedPath()
				if wantPath[len(wantPath)-1] == '/' {
					wantPath = wantPath[:len(wantPath)-1]
				}
				wantHost := "bkt.example.invalid:9000"
				if pathStyle {
					wantHost = "example.invalid:9000"
					wantPath += "/bkt"
				}
				wantPath += "/dir/a%20%23%3F%2B%25.txt"
				u, err := url.Parse(got)
				if err != nil || u.Host != wantHost || u.EscapedPath() != wantPath || u.Fragment != "" || u.RawQuery != "" {
					t.Fatalf("URL=%q error=%v; want host=%q escaped path=%q and no fragment/query", got, err, wantHost, wantPath)
				}
			})
		}
	}
}
