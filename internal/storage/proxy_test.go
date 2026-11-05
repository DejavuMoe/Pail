package storage

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func TestNormalizeProxy(t *testing.T) {
	for in, want := range map[string]string{
		"":                                 "",
		"   ":                              "",
		"socks5://127.0.0.1:7890":          "socks5://127.0.0.1:7890",
		"SOCKS5H://proxy.lan:1080/":        "socks5h://proxy.lan:1080",
		"http://127.0.0.1:7890":            "http://127.0.0.1:7890",
		"https://proxy.example.com":        "https://proxy.example.com",
		"socks5://[::1]:1080":              "socks5://[::1]:1080",
		"http://proxy.example.com:1":       "http://proxy.example.com:1",
		"http://user:p%40ss@10.0.0.1:3128": "http://user:p%40ss@10.0.0.1:3128",
		"socks5://user@10.0.0.1:1080":      "socks5://user@10.0.0.1:1080",
	} {
		got, err := NormalizeProxy(in)
		if err != nil || got != want {
			t.Errorf("NormalizeProxy(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{
		"ftp://127.0.0.1:21",
		"socks://127.0.0.1:7890",
		"127.0.0.1:7890", // no scheme
		"http://",
		"socks5://:7890",
		"http://127.0.0.1:7890/path",
		"http://127.0.0.1:7890?x=1",
		"http://127.0.0.1:0",
		"http://127.0.0.1:70000",
		"http://127.0.0.1:abc",
		"not a proxy",
	} {
		if got, err := NormalizeProxy(in); err == nil {
			t.Errorf("NormalizeProxy(%q) = %q, want an error", in, got)
		}
	}
	// An error never repeats the password.
	if _, err := NormalizeProxy("gopher://user:hunter2@127.0.0.1:70"); err == nil || strings.Contains(err.Error(), "hunter2") {
		t.Errorf("error %v", err)
	}
}

// clearProxyEnv makes the tests independent of the machine's proxy variables.
func clearProxyEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy"} {
		t.Setenv(k, "")
	}
}

func TestProxyFunc(t *testing.T) {
	clearProxyEnv(t)
	req := func(raw string) *http.Request {
		r, err := http.NewRequest(http.MethodGet, raw, nil)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	pick := proxyFunc("socks5://127.0.0.1:7890")
	for raw, want := range map[string]string{
		"https://s3.amazonaws.com/b":        "socks5://127.0.0.1:7890",
		"http://minio.lan:9000/b":           "socks5://127.0.0.1:7890",
		"http://localhost:9000/b":           "", // local services are never sent to a proxy
		"http://127.0.0.1:9000/b":           "",
		"https://[::1]:9000/b":              "",
		"https://account.r2.cloudflare.com": "socks5://127.0.0.1:7890",
	} {
		u, err := pick(req(raw))
		got := ""
		if u != nil {
			got = u.String()
		}
		if err != nil || got != want {
			t.Errorf("proxy for %s = %q, %v; want %q", raw, got, err, want)
		}
	}

	t.Setenv("NO_PROXY", "minio.lan,.internal")
	pick = proxyFunc("http://127.0.0.1:7890")
	for raw, want := range map[string]string{
		"http://minio.lan:9000/b":      "",
		"https://store.internal/b":     "",
		"https://s3.amazonaws.com/b":   "http://127.0.0.1:7890",
		"https://other.example.com/b2": "http://127.0.0.1:7890",
	} {
		u, err := pick(req(raw))
		got := ""
		if u != nil {
			got = u.String()
		}
		if err != nil || got != want {
			t.Errorf("proxy for %s with NO_PROXY = %q, %v; want %q", raw, got, err, want)
		}
	}

	if _, err := proxyFunc("ftp://x")(req("https://s3.amazonaws.com/b")); err == nil {
		t.Error("an invalid proxy address connected directly instead of failing")
	}
}

func TestEmptyProxyFollowsEnvironment(t *testing.T) {
	clearProxyEnv(t)
	// The standard library reads the environment once per process, so what can
	// be checked is that an empty address selects its function.
	got := reflect.ValueOf(proxyFunc("")).Pointer()
	if want := reflect.ValueOf(http.ProxyFromEnvironment).Pointer(); got != want {
		t.Fatal("an empty proxy does not follow the environment")
	}
	// An explicit proxy replaces the environment one.
	t.Setenv("HTTPS_PROXY", "http://env-proxy.test:3128")
	r, _ := http.NewRequest(http.MethodGet, "https://s3.amazonaws.com/b", nil)
	u, err := proxyFunc("http://own.test:1")(r)
	if err != nil || u == nil || u.Host != "own.test:1" {
		t.Fatalf("explicit proxy = %v, %v", u, err)
	}
}

const emptyBuckets = `<ListAllMyBucketsResult><Buckets/></ListAllMyBucketsResult>`

// A plain-HTTP endpoint on a name that cannot resolve is reachable only through
// the proxy, so a successful call proves the request went there.
func TestHTTPProxy(t *testing.T) {
	clearProxyEnv(t)
	var seen atomic.Value
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Store(r.Method + " " + r.URL.String())
		fmt.Fprint(w, emptyBuckets)
	}))
	defer proxy.Close()

	c, err := New(context.Background(), Config{Endpoint: "http://s3.unreachable.test:9000", AccessKeyID: "a", SecretAccessKey: "b",
		UsePathStyle: true, ProxyURL: proxy.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListBuckets(context.Background()); err != nil {
		t.Fatalf("ListBuckets through the HTTP proxy: %v", err)
	}
	got, _ := seen.Load().(string)
	if !strings.HasPrefix(got, "GET http://s3.unreachable.test:9000/") {
		t.Fatalf("the proxy saw %q", got)
	}

	// Without the proxy the same endpoint is unreachable.
	c, err = New(context.Background(), Config{Endpoint: "http://s3.unreachable.test:9000", AccessKeyID: "a", SecretAccessKey: "b", UsePathStyle: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := c.S3().ListBuckets(ctx, &s3.ListBucketsInput{}, func(o *s3.Options) { o.RetryMaxAttempts = 1 }); err == nil {
		t.Fatal("the unresolvable endpoint answered without the proxy")
	}
}

// A local endpoint is not sent to the proxy, even when one is set.
func TestLocalEndpointBypassesProxy(t *testing.T) {
	clearProxyEnv(t)
	var viaProxy atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		viaProxy.Add(1)
		http.Error(w, "should not be used", http.StatusBadGateway)
	}))
	defer proxy.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, emptyBuckets) }))
	defer origin.Close()

	c, err := New(context.Background(), Config{Endpoint: origin.URL, AccessKeyID: "a", SecretAccessKey: "b", UsePathStyle: true, ProxyURL: proxy.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListBuckets(context.Background()); err != nil {
		t.Fatal(err)
	}
	if viaProxy.Load() != 0 {
		t.Fatal("a loopback endpoint was sent through the proxy")
	}
}

// socks5Server is a minimal SOCKS5 proxy (CONNECT only) that records the
// destinations it was asked for and connects them to target. With user set it
// requires that user name and password (RFC 1929).
type socks5Server struct {
	ln         net.Listener
	target     string
	user, pass string
	mu         sync.Mutex
	asked      []string
	badLogins  int
}

func newSocks5Server(t *testing.T, target string) *socks5Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &socks5Server{ln: ln, target: target}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(conn)
		}
	}()
	return s
}

func (s *socks5Server) serve(conn net.Conn) {
	defer conn.Close()
	head := make([]byte, 2)
	if _, err := io.ReadFull(conn, head); err != nil || head[0] != 5 {
		return
	}
	methods := make([]byte, head[1])
	if _, err := io.ReadFull(conn, methods); err != nil {
		return
	}
	if s.user == "" {
		conn.Write([]byte{5, 0}) // no authentication
	} else {
		if !bytes.Contains(methods, []byte{2}) {
			conn.Write([]byte{5, 0xff})
			return
		}
		conn.Write([]byte{5, 2}) // user name and password
		ver := make([]byte, 2)
		if _, err := io.ReadFull(conn, ver[:2]); err != nil || ver[0] != 1 {
			return
		}
		user := make([]byte, ver[1])
		io.ReadFull(conn, user)
		plen := make([]byte, 1)
		io.ReadFull(conn, plen)
		pass := make([]byte, plen[0])
		io.ReadFull(conn, pass)
		if string(user) != s.user || string(pass) != s.pass {
			s.mu.Lock()
			s.badLogins++
			s.mu.Unlock()
			conn.Write([]byte{1, 1})
			return
		}
		conn.Write([]byte{1, 0})
	}
	req := make([]byte, 4)
	if _, err := io.ReadFull(conn, req); err != nil || req[1] != 1 {
		return
	}
	var host string
	switch req[3] {
	case 1:
		ip := make([]byte, 4)
		io.ReadFull(conn, ip)
		host = net.IP(ip).String()
	case 3:
		n := make([]byte, 1)
		io.ReadFull(conn, n)
		name := make([]byte, n[0])
		io.ReadFull(conn, name)
		host = string(name)
	default:
		return
	}
	port := make([]byte, 2)
	if _, err := io.ReadFull(conn, port); err != nil {
		return
	}
	s.mu.Lock()
	s.asked = append(s.asked, fmt.Sprintf("%s:%d", host, binary.BigEndian.Uint16(port)))
	s.mu.Unlock()
	up, err := net.Dial("tcp", s.target)
	if err != nil {
		conn.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer up.Close()
	conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
	done := make(chan struct{}, 2)
	go func() { io.Copy(up, conn); done <- struct{}{} }()
	go func() { io.Copy(conn, up); done <- struct{}{} }()
	<-done
}

func (s *socks5Server) destinations() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.asked...)
}

func TestSocks5Proxy(t *testing.T) {
	clearProxyEnv(t)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, emptyBuckets) }))
	defer origin.Close()
	ou, _ := url.Parse(origin.URL)
	socks := newSocks5Server(t, ou.Host)

	// The endpoint names a host that does not resolve; only the proxy can reach it.
	for _, scheme := range []string{"socks5", "socks5h"} {
		proxyURL := scheme + "://" + socks.ln.Addr().String()
		c, err := New(context.Background(), Config{Endpoint: "http://s3.unreachable.test:9000", AccessKeyID: "a", SecretAccessKey: "b",
			UsePathStyle: true, ProxyURL: proxyURL})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.ListBuckets(context.Background()); err != nil {
			t.Fatalf("ListBuckets through %s://: %v", scheme, err)
		}
	}
	for _, d := range socks.destinations() {
		if d != "s3.unreachable.test:9000" {
			t.Errorf("the SOCKS5 proxy was asked for %q", d)
		}
	}
	if len(socks.destinations()) == 0 {
		t.Fatal("nothing went through the SOCKS5 proxy")
	}
}

func TestSocks5ProxyWithUserNameAndPassword(t *testing.T) {
	clearProxyEnv(t)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, emptyBuckets) }))
	defer origin.Close()
	ou, _ := url.Parse(origin.URL)
	socks := newSocks5Server(t, ou.Host)
	socks.user, socks.pass = "robin", "p@ss:w/rd"

	list := func(userinfo string) error {
		c, err := New(context.Background(), Config{Endpoint: "http://s3.unreachable.test:9000", AccessKeyID: "a", SecretAccessKey: "b",
			UsePathStyle: true, ProxyURL: "socks5://" + userinfo + socks.ln.Addr().String()})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, err = c.S3().ListBuckets(ctx, &s3.ListBucketsInput{}, func(o *s3.Options) { o.RetryMaxAttempts = 1 })
		return err
	}
	good := (&url.URL{User: url.UserPassword("robin", "p@ss:w/rd")}).String()[len("//"):] // percent-encoded "user:password@"
	if err := list(good); err != nil {
		t.Fatalf("with the right user name and password: %v", err)
	}
	if err := list(""); err == nil {
		t.Fatal("a proxy that requires a login answered without one")
	}
	if err := list("robin:wrong@"); err == nil {
		t.Fatal("a wrong password was accepted")
	}
	socks.mu.Lock()
	defer socks.mu.Unlock()
	if socks.badLogins == 0 || len(socks.asked) == 0 {
		t.Fatalf("bad logins %d, connects %v", socks.badLogins, socks.asked)
	}
}

func TestHTTPProxyWithUserNameAndPassword(t *testing.T) {
	clearProxyEnv(t)
	var gotAuth atomic.Value
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth.Store(r.Header.Get("Proxy-Authorization"))
		if u, p, ok := proxyBasicAuth(r); !ok || u != "robin" || p != "p@ss:w/rd" {
			w.Header().Set("Proxy-Authenticate", `Basic realm="proxy"`)
			http.Error(w, "proxy authentication required", http.StatusProxyAuthRequired)
			return
		}
		fmt.Fprint(w, emptyBuckets)
	}))
	defer proxy.Close()
	pu, _ := url.Parse(proxy.URL)
	withLogin := (&url.URL{Scheme: "http", User: url.UserPassword("robin", "p@ss:w/rd"), Host: pu.Host}).String()

	c, err := New(context.Background(), Config{Endpoint: "http://s3.unreachable.test:9000", AccessKeyID: "a", SecretAccessKey: "b", UsePathStyle: true, ProxyURL: withLogin})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListBuckets(context.Background()); err != nil {
		t.Fatalf("ListBuckets through an HTTP proxy with a login: %v", err)
	}
	if got, _ := gotAuth.Load().(string); !strings.HasPrefix(got, "Basic ") {
		t.Fatalf("Proxy-Authorization = %q", got)
	}

	// Without the login the proxy refuses.
	c, err = New(context.Background(), Config{Endpoint: "http://s3.unreachable.test:9000", AccessKeyID: "a", SecretAccessKey: "b", UsePathStyle: true, ProxyURL: proxy.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := c.S3().ListBuckets(ctx, &s3.ListBucketsInput{}, func(o *s3.Options) { o.RetryMaxAttempts = 1 }); err == nil {
		t.Fatal("a proxy that requires a login answered without one")
	}
}

func proxyBasicAuth(r *http.Request) (user, pass string, ok bool) {
	req := &http.Request{Header: http.Header{"Authorization": {r.Header.Get("Proxy-Authorization")}}}
	return req.BasicAuth()
}
