package selfupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/DejavuMoe/Pail/internal/updatefmt"
)

// Updater checks for and installs updates of the running program.
type Updater struct {
	// Current is the running version, as internal/buildstamp reports it.
	Current string
	// Source is where updates come from first; "" is SourceGitHub.
	Source Source
	// Proxy is the URL of the proxy requests go through; "" follows
	// HTTP_PROXY, HTTPS_PROXY and NO_PROXY.
	Proxy string

	// Tests replace these; zero values are the real ones.
	key    string
	bases  map[Source]string // the directory of the manifests per source
	target string
}

// Update is a newer version found by Check.
type Update struct {
	// Version of the update.
	Version string
	// Size is what Install downloads: the delta for the running version when
	// the manifest has one, else the archive.
	Size int64
	// Delta says that Size is a delta update's.
	Delta bool
	// From is the source that served the manifest.
	From Source

	manifest updatefmt.Manifest
	u        *Updater
}

func (u *Updater) publicKey() string {
	if u.key != "" {
		return u.key
	}
	return PublicKey
}

func (u *Updater) source() Source {
	if u.Source.Valid() {
		return u.Source
	}
	return SourceGitHub
}

func (u *Updater) targetName() string {
	if u.target != "" {
		return u.target
	}
	return Target()
}

func (u *Updater) base(s Source) string {
	if b, ok := u.bases[s]; ok {
		return b
	}
	if s == SourceMirror {
		return MirrorBase
	}
	return GitHubLatest
}

// Check reads the manifest of the running target from the chosen source, or
// from the other one when that fails, and returns the update it offers, or nil
// when the running version is the latest. A manifest of a prerelease is never
// offered.
func (u *Updater) Check(ctx context.Context) (*Update, error) {
	if err := Supported(u.Current); err != nil {
		return nil, err
	}
	return u.check(ctx)
}

func (u *Updater) check(ctx context.Context) (*Update, error) {
	first := u.source()
	m, err := u.manifest(ctx, first)
	from := first
	if err != nil && ctx.Err() == nil {
		var err2 error
		if m, err2 = u.manifest(ctx, first.other()); err2 == nil {
			from, err = first.other(), nil
		} else {
			err = fmt.Errorf("%w; %s: %v", err, first.other(), err2)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("checking for updates: %w", err)
	}
	if strings.ContainsAny(m.Version, "-+") || updatefmt.Compare(m.Version, u.Current) <= 0 {
		return nil, nil
	}
	up := &Update{Version: m.Version, Size: m.Size, From: from, manifest: *m, u: u}
	if d := m.Delta(u.Current); d != nil && d.Size > 0 && d.Size < m.Size {
		up.Size, up.Delta = d.Size, true
	}
	return up, nil
}

func (u *Updater) manifest(ctx context.Context, s Source) (*updatefmt.Manifest, error) {
	body, err := u.get(ctx, u.base(s)+ManifestName(u.targetName()))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s, err)
	}
	defer body.Close()
	var m updatefmt.Manifest
	if err := json.NewDecoder(io.LimitReader(body, updatefmt.MaxManifestSize)).Decode(&m); err != nil {
		return nil, fmt.Errorf("%s: reading the manifest: %w", s, err)
	}
	if m.Version == "" || m.URL == "" || m.Signature == "" || m.Size <= 0 {
		return nil, fmt.Errorf("%s: %w", s, errEmptyManifest)
	}
	return &m, nil
}

// open downloads a file the manifest names; when that fails, the file of the
// same name from the other source.
func (up *Update) open(ctx context.Context, fileURL string) (io.ReadCloser, error) {
	body, err := up.u.get(ctx, fileURL)
	if err == nil || ctx.Err() != nil {
		return body, err
	}
	name := path.Base(fileURL)
	alt := up.u.base(SourceMirror) + name
	if up.From == SourceMirror {
		alt = GitHubRelease(up.Version) + name
		if b, ok := up.u.bases[SourceGitHub]; ok {
			alt = b + name
		}
	}
	if alt == fileURL {
		return nil, err
	}
	body, err2 := up.u.get(ctx, alt)
	if err2 != nil {
		return nil, fmt.Errorf("%w; %v", err, err2)
	}
	return body, nil
}

// Timeouts of a request: the headers must arrive within headerTimeout, and a
// body that sends nothing for stallTimeout is given up.
var (
	headerTimeout = 30 * time.Second
	stallTimeout  = time.Minute
	retries       = []time.Duration{time.Second, 3 * time.Second}
)

func (u *Updater) client() (*http.Client, error) {
	proxy := http.ProxyFromEnvironment
	if p := strings.TrimSpace(u.Proxy); p != "" {
		pu, err := url.Parse(p)
		if err != nil || pu.Host == "" {
			return nil, errors.New("invalid proxy address")
		}
		proxy = http.ProxyURL(pu)
	}
	return &http.Client{CheckRedirect: checkRedirect, Transport: &http.Transport{
		Proxy:                 proxy,
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: headerTimeout,
		ForceAttemptHTTP2:     true,
	}}, nil
}

// maxRedirects caps the hops of one download.
const maxRedirects = 5

// errRedirect marks a redirect that was refused; it is never retried.
var errRedirect = errors.New("refusing redirect")

// allowedScheme reports whether updates may be fetched from u: HTTPS, or plain
// HTTP from the loopback interface (tests).
func allowedScheme(u *url.URL) bool {
	return u.Scheme == "https" || (u.Scheme == "http" && isLoopback(u.Hostname()))
}

// checkRedirect applies the scheme rule of the first request to every hop.
func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > maxRedirects {
		return fmt.Errorf("%w: more than %d redirects", errRedirect, maxRedirects)
	}
	if !allowedScheme(req.URL) {
		return fmt.Errorf("%w to %s: updates need HTTPS", errRedirect, req.URL.Redacted())
	}
	return nil
}

// get fetches a URL over HTTPS (plain HTTP only from the loopback interface,
// for tests), retrying failures that a moment may mend.
func (u *Updater) get(ctx context.Context, rawURL string) (io.ReadCloser, error) {
	pu, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if !allowedScheme(pu) {
		return nil, fmt.Errorf("refusing to download %s: updates need HTTPS", rawURL)
	}
	client, err := u.client()
	if err != nil {
		return nil, err
	}
	for try := 0; ; try++ {
		reqCtx, cancel := context.WithCancel(ctx)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, rawURL, nil)
		if err != nil {
			cancel()
			return nil, err
		}
		// A fixed, ASCII name: no version, nothing that tells installations apart.
		req.Header.Set("User-Agent", "pail-updater")
		resp, err := client.Do(req)
		var wait time.Duration
		switch {
		case err != nil:
			cancel()
			if ctx.Err() != nil || try >= len(retries) || errors.Is(err, errRedirect) {
				return nil, err
			}
		case resp.StatusCode == http.StatusOK:
			return newStalledBody(resp.Body, cancel), nil
		default:
			resp.Body.Close()
			cancel()
			if !transient(resp.StatusCode) || try >= len(retries) {
				return nil, fmt.Errorf("GET %s: %s", rawURL, resp.Status)
			}
			if n, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After"))); err == nil && n > 0 {
				wait = min(time.Duration(n)*time.Second, 30*time.Second)
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(max(wait, retries[try])):
		}
	}
}

func transient(code int) bool {
	switch code {
	case http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// stalledBody cancels the request of a body that sends nothing for
// stallTimeout.
type stalledBody struct {
	body    io.ReadCloser
	cancel  context.CancelFunc
	d       time.Duration
	last    atomic.Int64
	stalled atomic.Bool
	done    atomic.Bool
	timer   *time.Timer
}

func newStalledBody(body io.ReadCloser, cancel context.CancelFunc) *stalledBody {
	b := &stalledBody{body: body, cancel: cancel, d: stallTimeout}
	b.last.Store(time.Now().UnixNano())
	b.timer = time.AfterFunc(1<<63-1, b.check)
	b.timer.Stop()
	b.timer.Reset(b.d / 4)
	return b
}

func (b *stalledBody) check() {
	if b.done.Load() {
		return
	}
	if time.Since(time.Unix(0, b.last.Load())) >= b.d {
		b.stalled.Store(true)
		b.cancel()
		return
	}
	b.timer.Reset(b.d / 4)
}

func (b *stalledBody) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	if n > 0 {
		b.last.Store(time.Now().UnixNano())
	}
	if err != nil && b.stalled.Load() {
		return n, errors.New("the download stalled: the connection sent nothing")
	}
	return n, err
}

func (b *stalledBody) Close() error {
	b.done.Store(true)
	b.timer.Stop()
	b.cancel()
	return b.body.Close()
}
