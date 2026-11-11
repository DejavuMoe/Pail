package app

import (
	"testing"

	"github.com/DejavuMoe/Pail/internal/storage"
)

func TestRequesterPaysHeader(t *testing.T) {
	e := newS3Env(t)
	e.srv.CreateBucket("pays", false)
	acc := e.acc
	acc.RequesterPays = true
	if _, err := e.s.SaveAccount(acc, nil); err != nil {
		t.Fatal(err)
	}
	e.srv.ResetRequests()
	if _, err := e.s.List(testCtx(t), e.loc("pays", ""), ListOptions{}); err != nil {
		t.Fatal(err)
	}
	reqs := e.srv.Requests()
	if len(reqs) == 0 {
		t.Fatal("no request")
	}
	for _, r := range reqs {
		if r.Header.Get("x-amz-request-payer") != "requester" {
			t.Fatalf("%s %s without x-amz-request-payer", r.Method, r.Path)
		}
	}
}

func TestAccountProxyModes(t *testing.T) {
	base := staticAccount("p", "http://127.0.0.1:9000")
	for _, c := range []struct {
		mode, url string
		ok        bool
		want      string
	}{
		{ProxyGlobal, "ignored", true, "global"},
		{ProxyDirect, "", true, storage.DirectConnection},
		{ProxyCustom, "socks5://127.0.0.1:1080", true, "socks5://127.0.0.1:1080"},
		{ProxyCustom, "", false, ""},
		{ProxyCustom, "http://user:pw@127.0.0.1:8080", false, ""},
		{ProxyCustom, "ftp://127.0.0.1", false, ""},
		{"sometimes", "", false, ""},
	} {
		a := base
		a.ProxyMode, a.ProxyURL = c.mode, c.url
		n, err := normalizeAccount(a)
		if (err == nil) != c.ok {
			t.Errorf("%q %q: %v", c.mode, c.url, err)
			continue
		}
		if !c.ok {
			continue
		}
		got := storageConfig(n, Secrets{SecretAccessKey: "sk"}, "global").ProxyURL
		if got != c.want {
			t.Errorf("%q %q: proxy %q, want %q", c.mode, c.url, got, c.want)
		}
	}
}
