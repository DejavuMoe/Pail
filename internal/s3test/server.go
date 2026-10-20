// Package s3test is an in-memory, path-style S3-compatible HTTP server for
// tests and the UI demo mode. It implements the subset of the S3 REST API that
// Pail uses, enough fault injection to exercise retry and error paths, and
// never verifies request signatures.
package s3test

import (
	"bytes"
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	xmlNS     = "http://s3.amazonaws.com/doc/2006-03-01/"
	ownerID   = "pail-test-owner"
	ownerName = "Pail"
	isoTime   = "2006-01-02T15:04:05.000Z"
)

// Request is one logged HTTP request.
type Request struct {
	Method   string
	Path     string
	RawQuery string
	Header   http.Header
}

// VersionInfo describes one stored version of a key.
type VersionInfo struct {
	Key          string
	VersionID    string
	IsLatest     bool
	DeleteMarker bool
	Size         int64
	ETag         string // quoted
	LastModified time.Time
	StorageClass string
}

// Fault describes a failure to inject for one request.
type Fault struct {
	Status         int // written as an S3 XML error with Code/Message
	Code, Message  string
	Delay          time.Duration // sleep before handling (respects the request context)
	DropConnection bool          // hijack and close without a response
	DropDeleteAcks []string      // DeleteObjects: omit <Deleted> entries for these keys
}

type tag struct{ Key, Value string }

type aclState struct {
	canned string
	raw    []byte
}

type objMeta struct {
	contentType, contentEncoding, cacheControl, contentDisposition, contentLanguage, expires string
	meta                                                                                     map[string]string
	storageClass, sse, kmsKey, ssecAlg, ssecMD5                                              string
	tags                                                                                     []tag
	acl                                                                                      aclState
}

type version struct {
	id       string
	marker   bool
	data     []byte // immutable once stored
	etag     string
	modified time.Time
	objMeta
}

type part struct {
	data     []byte
	etag     string
	sum      [md5.Size]byte
	modified time.Time
}

type upload struct {
	id        string
	key       string
	initiated time.Time
	meta      objMeta
	parts     map[int]*part
}

type bucket struct {
	name       string
	created    time.Time
	location   string
	versioning string // "", "Enabled", "Suspended"
	objects    map[string][]*version
	sub        map[string][]byte
	tags       []tag
	tagsSet    bool
	acl        aclState
	uploads    map[string]*upload
}

// Server is the in-memory S3 server.
type Server struct {
	URL string
	ts  *httptest.Server

	mu      sync.Mutex // guards buckets
	buckets map[string]*bucket

	cfgMu       sync.Mutex // guards reqs, inject, unsupported
	reqs        []Request
	inject      func(r *http.Request) *Fault
	unsupported map[string]bool

	condWrites atomic.Bool
	seq        atomic.Uint64
}

// New starts a server. Server.URL is the endpoint (path-style addressing).
func New() *Server {
	s := &Server{buckets: map[string]*bucket{}, unsupported: map[string]bool{}}
	s.condWrites.Store(true)
	s.ts = httptest.NewServer(http.HandlerFunc(s.serve))
	s.URL = s.ts.URL
	return s
}

// Close shuts the server down.
func (s *Server) Close() { s.ts.CloseClientConnections(); s.ts.Close() }

// Requests returns the request log (oldest first).
func (s *Server) Requests() []Request {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	out := make([]Request, len(s.reqs))
	copy(out, s.reqs)
	return out
}

// ResetRequests clears the request log.
func (s *Server) ResetRequests() {
	s.cfgMu.Lock()
	s.reqs = nil
	s.cfgMu.Unlock()
}

// Inject installs a fault function; nil clears it. fn may return nil for no fault.
func (s *Server) Inject(fn func(r *http.Request) *Fault) {
	s.cfgMu.Lock()
	s.inject = fn
	s.cfgMu.Unlock()
}

// SetUnsupported makes every request carrying the named subresource answer 501.
func (s *Server) SetUnsupported(op string) {
	s.cfgMu.Lock()
	s.unsupported[op] = true
	s.cfgMu.Unlock()
}

// ClearUnsupported undoes SetUnsupported (empty op clears all).
func (s *Server) ClearUnsupported(op string) {
	s.cfgMu.Lock()
	if op == "" {
		s.unsupported = map[string]bool{}
	} else {
		delete(s.unsupported, op)
	}
	s.cfgMu.Unlock()
}

// SetConditionalWrites controls whether If-None-Match is honoured on PUT and
// CompleteMultipartUpload (default true).
func (s *Server) SetConditionalWrites(enabled bool) { s.condWrites.Store(enabled) }

// ---- direct state helpers ----

func now() time.Time { return time.Now().UTC().Truncate(time.Second) }

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

func quoteETag(sum [md5.Size]byte) string { return `"` + hex.EncodeToString(sum[:]) + `"` }

func md5ETag(data []byte) string { return quoteETag(md5.Sum(data)) }

func cloneBytes(b []byte) []byte { return append([]byte(nil), b...) }

func cloneMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (m objMeta) clone() objMeta {
	m.meta = cloneMap(m.meta)
	m.tags = append([]tag(nil), m.tags...)
	m.acl.raw = cloneBytes(m.acl.raw)
	return m
}

func newBucket(name string) *bucket {
	return &bucket{name: name, created: now(), objects: map[string][]*version{}, sub: map[string][]byte{}, uploads: map[string]*upload{}}
}

// CreateBucket creates a bucket (or updates the versioning of an existing one).
func (s *Server) CreateBucket(name string, versioned bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.buckets[name]
	if b == nil {
		b = newBucket(name)
		s.buckets[name] = b
	}
	if versioned {
		b.versioning = "Enabled"
	}
}

// PutObject stores an object directly and returns its version ID ("" when the
// bucket does not exist; "null" for unversioned buckets).
func (s *Server) PutObject(bucketName, key string, data []byte, contentType string, meta map[string]string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.buckets[bucketName]
	if b == nil {
		return ""
	}
	if contentType == "" {
		contentType = "binary/octet-stream"
	}
	m := objMeta{contentType: contentType, meta: map[string]string{}, storageClass: "STANDARD"}
	for k, v := range meta {
		m.meta[strings.ToLower(k)] = v
	}
	return b.put(key, cloneBytes(data), m).id
}

// Object returns the current version's data.
func (s *Server) Object(bucketName, key string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.buckets[bucketName]
	if b == nil {
		return nil, false
	}
	v := b.current(key)
	if v == nil || v.marker {
		return nil, false
	}
	return cloneBytes(v.data), true
}

// Versions lists the versions of one key, newest first, including delete markers.
func (s *Server) Versions(bucketName, key string) []VersionInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.buckets[bucketName]
	if b == nil {
		return nil
	}
	var out []VersionInfo
	for i, v := range b.objects[key] {
		out = append(out, VersionInfo{Key: key, VersionID: v.id, IsLatest: i == 0, DeleteMarker: v.marker,
			Size: int64(len(v.data)), ETag: v.etag, LastModified: v.modified, StorageClass: v.storageClass})
	}
	return out
}

// DeleteObject deletes like an unversioned-id DeleteObject request and returns
// the delete marker's version ID (or "" when none was created).
func (s *Server) DeleteObject(bucketName, key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.buckets[bucketName]
	if b == nil {
		return ""
	}
	r := b.deleteKey(key, "")
	if r.created {
		return r.versionID
	}
	return ""
}

// DeleteMarker injects a delete marker on top of key regardless of the bucket's
// versioning state and returns its version ID.
func (s *Server) DeleteMarker(bucketName, key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.buckets[bucketName]
	if b == nil {
		return ""
	}
	m := &version{marker: true, modified: now(), id: newID()}
	b.objects[key] = append([]*version{m}, b.objects[key]...)
	return m.id
}

// SetStorageClass changes the storage class of the current version.
func (s *Server) SetStorageClass(bucketName, key, class string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b := s.buckets[bucketName]; b != nil {
		if v := b.current(key); v != nil && !v.marker {
			v.storageClass = class
			return true
		}
	}
	return false
}

// Buckets returns the bucket names, sorted.
func (s *Server) Buckets() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.buckets))
	for n := range s.buckets {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (b *bucket) current(key string) *version {
	if vs := b.objects[key]; len(vs) > 0 {
		return vs[0]
	}
	return nil
}

func (b *bucket) find(key, id string) *version {
	for _, v := range b.objects[key] {
		if v.id == id {
			return v
		}
	}
	return nil
}

// insert adds v as the newest version, applying the versioning state.
func (b *bucket) insert(key string, v *version) {
	switch b.versioning {
	case "Enabled":
		v.id = newID()
	case "Suspended":
		v.id = "null"
		kept := b.objects[key][:0:0]
		for _, o := range b.objects[key] {
			if o.id != "null" {
				kept = append(kept, o)
			}
		}
		b.objects[key] = kept
	default:
		v.id = "null"
		b.objects[key] = nil
	}
	b.objects[key] = append([]*version{v}, b.objects[key]...)
}

func (b *bucket) put(key string, data []byte, m objMeta) *version {
	if m.storageClass == "" {
		m.storageClass = "STANDARD"
	}
	v := &version{data: data, etag: md5ETag(data), modified: now(), objMeta: m}
	b.insert(key, v)
	return v
}

func (b *bucket) hasObjects() bool {
	for _, vs := range b.objects {
		if len(vs) > 0 {
			return true
		}
	}
	return false
}

func (b *bucket) sortedKeys() []string {
	keys := make([]string, 0, len(b.objects))
	for k, vs := range b.objects {
		if len(vs) > 0 {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

type delResult struct {
	versionID string
	marker    bool // the affected version is a delete marker
	created   bool // a new delete marker was created
}

func (b *bucket) deleteKey(key, vid string) delResult {
	if vid != "" {
		vs := b.objects[key]
		for i, v := range vs {
			if v.id == vid {
				b.objects[key] = append(append([]*version(nil), vs[:i]...), vs[i+1:]...)
				if len(b.objects[key]) == 0 {
					delete(b.objects, key)
				}
				return delResult{versionID: vid, marker: v.marker}
			}
		}
		return delResult{versionID: vid}
	}
	if b.versioning == "" {
		delete(b.objects, key)
		return delResult{}
	}
	m := &version{marker: true, modified: now()}
	b.insert(key, m)
	return delResult{versionID: m.id, marker: true, created: true}
}

// ---- response plumbing ----

type resp struct {
	status  int
	h       http.Header
	body    []byte
	headLen int64
}

func newResp(status int) *resp { return &resp{status: status, h: http.Header{}, headLen: -1} }

type xw struct{ strings.Builder }

func (x *xw) open(n string)             { x.WriteString("<" + n + ">") }
func (x *xw) end(n string)              { x.WriteString("</" + n + ">") }
func (x *xw) root(n string)             { x.WriteString("<" + n + ` xmlns="` + xmlNS + `">`) }
func (x *xw) eli(n string, v int64)     { x.el(n, strconv.FormatInt(v, 10)) }
func (x *xw) elb(n string, v bool)      { x.el(n, strconv.FormatBool(v)) }
func (x *xw) elt(n string, t time.Time) { x.el(n, t.UTC().Format(isoTime)) }
func (x *xw) el(n, v string) {
	x.open(n)
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(v))
	x.Write(b.Bytes())
	x.end(n)
}
func (x *xw) owner() {
	x.open("Owner")
	x.el("ID", ownerID)
	x.el("DisplayName", ownerName)
	x.end("Owner")
}

type call struct {
	s     *Server
	r     *http.Request
	id    string
	q     url.Values
	body  []byte
	b     *bucket
	key   string
	fault *Fault
}

func (c *call) xmlResp(status int, build func(x *xw)) *resp {
	x := &xw{}
	build(x)
	rs := newResp(status)
	rs.h.Set("Content-Type", "application/xml")
	rs.body = []byte(xml.Header + x.String())
	return rs
}

func (c *call) fail(status int, code, msg string) *resp {
	rs := c.xmlResp(status, func(x *xw) {
		x.open("Error")
		x.el("Code", code)
		x.el("Message", msg)
		x.el("Resource", c.r.URL.Path)
		x.el("RequestId", c.id)
		x.end("Error")
	})
	return rs
}

func (c *call) noSuchBucket() *resp {
	return c.fail(404, "NoSuchBucket", "The specified bucket does not exist")
}

func (c *call) noSuchKey() *resp {
	return c.fail(404, "NoSuchKey", "The specified key does not exist.")
}

func (c *call) precondFailed() *resp {
	return c.fail(412, "PreconditionFailed", "At least one of the pre-conditions you specified did not hold")
}

func (c *call) empty(status int) *resp { return newResp(status) }

func (s *Server) writeResp(w http.ResponseWriter, r *http.Request, id string, rs *resp) {
	h := w.Header()
	for k, v := range rs.h {
		h[k] = v
	}
	h.Set("x-amz-request-id", id)
	h.Set("x-amz-id-2", id)
	h.Set("Server", "AmazonS3")
	if r.Method == http.MethodHead {
		n := rs.headLen
		if n < 0 {
			n = int64(len(rs.body))
		}
		h.Set("Content-Length", strconv.FormatInt(n, 10))
		w.WriteHeader(rs.status)
		return
	}
	if rs.status == http.StatusNotModified || rs.status == http.StatusNoContent {
		w.WriteHeader(rs.status)
		return
	}
	h.Set("Content-Length", strconv.Itoa(len(rs.body)))
	w.WriteHeader(rs.status)
	_, _ = w.Write(rs.body)
}

// serve is the single HTTP entry point.
func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	id := fmt.Sprintf("%016X", s.seq.Add(1))
	s.cfgMu.Lock()
	s.reqs = append(s.reqs, Request{Method: r.Method, Path: r.URL.Path, RawQuery: r.URL.RawQuery, Header: r.Header.Clone()})
	inj := s.inject
	s.cfgMu.Unlock()

	c := &call{s: s, r: r, id: id, q: r.URL.Query()}
	if inj != nil {
		c.fault = inj(r)
	}
	if f := c.fault; f != nil {
		if f.Delay > 0 {
			t := time.NewTimer(f.Delay)
			select {
			case <-t.C:
			case <-r.Context().Done():
				t.Stop()
				return
			}
		}
		if f.DropConnection {
			if hj, ok := w.(http.Hijacker); ok {
				if conn, _, err := hj.Hijack(); err == nil {
					_ = conn.Close()
					return
				}
			}
			panic(http.ErrAbortHandler)
		}
		if f.Status != 0 {
			code, msg := f.Code, f.Message
			if code == "" {
				code = http.StatusText(f.Status)
			}
			s.writeResp(w, r, id, c.fail(f.Status, code, msg))
			return
		}
	}

	if r.Header.Get("Authorization") == "" && c.q.Get("X-Amz-Signature") == "" {
		s.writeResp(w, r, id, c.fail(403, "AccessDenied", "Access Denied"))
		return
	}
	if rs := c.checkPresignExpiry(); rs != nil {
		s.writeResp(w, r, id, rs)
		return
	}
	s.cfgMu.Lock()
	var unsup bool
	for name := range s.unsupported {
		if c.q.Has(name) {
			unsup = true
		}
	}
	s.cfgMu.Unlock()
	if unsup {
		s.writeResp(w, r, id, c.fail(501, "NotImplemented", "A header you provided implies functionality that is not implemented"))
		return
	}

	body, err := readBody(r)
	if err != nil {
		s.writeResp(w, r, id, c.fail(400, "IncompleteBody", err.Error()))
		return
	}
	c.body = body

	path := strings.TrimPrefix(r.URL.Path, "/")
	bname, key, _ := strings.Cut(path, "/")
	c.key = key

	s.mu.Lock()
	rs := c.dispatch(bname)
	s.mu.Unlock()
	s.writeResp(w, r, id, rs)
}

func (c *call) checkPresignExpiry() *resp {
	if c.q.Get("X-Amz-Signature") == "" {
		return nil
	}
	t, err := time.Parse("20060102T150405Z", c.q.Get("X-Amz-Date"))
	if err != nil {
		return nil
	}
	secs, err := strconv.Atoi(c.q.Get("X-Amz-Expires"))
	if err != nil {
		return nil
	}
	if time.Now().After(t.Add(time.Duration(secs) * time.Second)) {
		return c.fail(403, "AccessDenied", "Request has expired")
	}
	return nil
}

// readBody reads the request body, decoding aws-chunked streaming payloads.
func readBody(r *http.Request) ([]byte, error) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	chunked := strings.HasPrefix(r.Header.Get("x-amz-content-sha256"), "STREAMING-")
	for _, e := range strings.Split(r.Header.Get("Content-Encoding"), ",") {
		if strings.EqualFold(strings.TrimSpace(e), "aws-chunked") {
			chunked = true
		}
	}
	if !chunked {
		return raw, nil
	}
	out, err := decodeAWSChunked(raw)
	if err != nil {
		return nil, err
	}
	if v := r.Header.Get("x-amz-decoded-content-length"); v != "" {
		if n, e := strconv.ParseInt(v, 10, 64); e == nil && n != int64(len(out)) {
			return nil, fmt.Errorf("decoded length %d does not match x-amz-decoded-content-length %d", len(out), n)
		}
	}
	return out, nil
}

func decodeAWSChunked(raw []byte) ([]byte, error) {
	var out []byte
	for {
		i := bytes.Index(raw, []byte("\r\n"))
		if i < 0 {
			return nil, fmt.Errorf("malformed aws-chunked body")
		}
		sizeStr, _, _ := strings.Cut(string(raw[:i]), ";")
		n, err := strconv.ParseInt(strings.TrimSpace(sizeStr), 16, 64)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("malformed aws-chunked chunk size")
		}
		raw = raw[i+2:]
		if n == 0 {
			return out, nil // trailers are ignored
		}
		if int64(len(raw)) < n+2 {
			return nil, fmt.Errorf("truncated aws-chunked body")
		}
		out = append(out, raw[:n]...)
		raw = raw[n+2:]
	}
}

// ---- shared header / precondition helpers ----

func stripETag(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "W/")
	return strings.Trim(s, `"`)
}

// etagListMatches reports whether header (a "*" or comma list) matches etag.
func etagListMatches(header, etag string) bool {
	for _, p := range strings.Split(header, ",") {
		p = strings.TrimSpace(p)
		if p == "*" || (p != "" && stripETag(p) == stripETag(etag)) {
			return true
		}
	}
	return false
}

func parseTagHeader(v string) []tag {
	if v == "" {
		return nil
	}
	q, _ := url.ParseQuery(v)
	var out []tag
	for k, vs := range q {
		val := ""
		if len(vs) > 0 {
			val = vs[0]
		}
		out = append(out, tag{k, val})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

const metaPrefix = "X-Amz-Meta-"

func metaFromHeaders(h http.Header) objMeta {
	m := objMeta{
		contentType:        h.Get("Content-Type"),
		cacheControl:       h.Get("Cache-Control"),
		contentDisposition: h.Get("Content-Disposition"),
		contentLanguage:    h.Get("Content-Language"),
		expires:            h.Get("Expires"),
		storageClass:       h.Get("x-amz-storage-class"),
		sse:                h.Get("x-amz-server-side-encryption"),
		kmsKey:             h.Get("x-amz-server-side-encryption-aws-kms-key-id"),
		ssecAlg:            h.Get("x-amz-server-side-encryption-customer-algorithm"),
		ssecMD5:            h.Get("x-amz-server-side-encryption-customer-key-MD5"),
		tags:               parseTagHeader(h.Get("x-amz-tagging")),
		meta:               map[string]string{},
	}
	var enc []string
	for _, e := range strings.Split(h.Get("Content-Encoding"), ",") {
		e = strings.TrimSpace(e)
		if e != "" && !strings.EqualFold(e, "aws-chunked") {
			enc = append(enc, e)
		}
	}
	m.contentEncoding = strings.Join(enc, ", ")
	if m.contentType == "" {
		m.contentType = "binary/octet-stream"
	}
	if m.storageClass == "" {
		m.storageClass = "STANDARD"
	}
	for k, vs := range h {
		if strings.HasPrefix(k, metaPrefix) {
			m.meta[strings.ToLower(k[len(metaPrefix):])] = strings.Join(vs, ",")
		}
	}
	if canned := h.Get("x-amz-acl"); canned != "" {
		m.acl.canned = canned
	}
	return m
}

func (b *bucket) versionedHeader() bool { return b.versioning != "" }

// objectHeaders sets the headers describing v on h.
func (b *bucket) objectHeaders(h http.Header, v *version) {
	h.Set("ETag", v.etag)
	h.Set("Last-Modified", v.modified.UTC().Format(http.TimeFormat))
	h.Set("Accept-Ranges", "bytes")
	h.Set("Content-Type", v.contentType)
	set := func(k, val string) {
		if val != "" {
			h.Set(k, val)
		}
	}
	set("Content-Encoding", v.contentEncoding)
	set("Cache-Control", v.cacheControl)
	set("Content-Disposition", v.contentDisposition)
	set("Content-Language", v.contentLanguage)
	set("Expires", v.expires)
	if v.storageClass != "" && v.storageClass != "STANDARD" {
		h.Set("x-amz-storage-class", v.storageClass)
	}
	set("x-amz-server-side-encryption", v.sse)
	set("x-amz-server-side-encryption-aws-kms-key-id", v.kmsKey)
	set("x-amz-server-side-encryption-customer-algorithm", v.ssecAlg)
	set("x-amz-server-side-encryption-customer-key-MD5", v.ssecMD5)
	if len(v.tags) > 0 {
		h.Set("x-amz-tagging-count", strconv.Itoa(len(v.tags)))
	}
	for k, val := range v.meta {
		h[metaPrefix+k] = []string{val}
	}
	if b.versionedHeader() {
		h.Set("x-amz-version-id", v.id)
	}
}

// encodeKey applies encoding-type=url when requested.
func encodeKey(enc bool, k string) string {
	if enc {
		return url.QueryEscape(k)
	}
	return k
}
