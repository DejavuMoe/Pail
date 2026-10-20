package s3test

import (
	"encoding/xml"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func (c *call) objectOp(m string) *resp {
	q := c.q
	switch m {
	case http.MethodGet, http.MethodHead:
		switch {
		case q.Has("acl"):
			v, rs := c.resolve(c.b, c.key, q.Get("versionId"))
			if rs != nil {
				return rs
			}
			return c.aclDoc(v.acl)
		case q.Has("tagging"):
			v, rs := c.resolve(c.b, c.key, q.Get("versionId"))
			if rs != nil {
				return rs
			}
			rs = c.taggingDoc(v.tags)
			if c.b.versionedHeader() {
				rs.h.Set("x-amz-version-id", v.id)
			}
			return rs
		case q.Has("uploadId"):
			return c.listParts()
		}
		return c.getObject()
	case http.MethodPut:
		switch {
		case q.Has("acl"):
			v, rs := c.resolve(c.b, c.key, q.Get("versionId"))
			if rs != nil {
				return rs
			}
			v.acl = parseACLRequest(c.r.Header, c.body)
			return c.empty(200)
		case q.Has("tagging"):
			v, rs := c.resolve(c.b, c.key, q.Get("versionId"))
			if rs != nil {
				return rs
			}
			tags, rs := c.parseTagging()
			if rs != nil {
				return rs
			}
			v.tags = tags
			rs = c.empty(200)
			if c.b.versionedHeader() {
				rs.h.Set("x-amz-version-id", v.id)
			}
			return rs
		case q.Has("partNumber") && q.Has("uploadId"):
			if c.r.Header.Get("x-amz-copy-source") != "" {
				return c.uploadPartCopy()
			}
			return c.uploadPart()
		case c.r.Header.Get("x-amz-copy-source") != "":
			return c.copyObject()
		}
		return c.putObject()
	case http.MethodDelete:
		switch {
		case q.Has("tagging"):
			v, rs := c.resolve(c.b, c.key, q.Get("versionId"))
			if rs != nil {
				return rs
			}
			v.tags = nil
			return c.empty(204)
		case q.Has("uploadId"):
			return c.abortUpload()
		}
		return c.deleteObject()
	case http.MethodPost:
		switch {
		case q.Has("uploads"):
			return c.createUpload()
		case q.Has("uploadId"):
			return c.completeUpload()
		}
	}
	return c.methodNotAllowed()
}

// resolve finds the target version of key in b; a delete marker is an error.
func (c *call) resolve(b *bucket, key, vid string) (*version, *resp) {
	if vid != "" {
		v := b.find(key, vid)
		if v == nil {
			return nil, c.fail(404, "NoSuchVersion", "The specified version does not exist.")
		}
		if v.marker {
			rs := c.fail(405, "MethodNotAllowed", "The specified method is not allowed against this resource.")
			rs.h.Set("x-amz-delete-marker", "true")
			rs.h.Set("x-amz-version-id", v.id)
			return nil, rs
		}
		return v, nil
	}
	v := b.current(key)
	if v == nil {
		return nil, c.noSuchKey()
	}
	if v.marker {
		rs := c.noSuchKey()
		rs.h.Set("x-amz-delete-marker", "true")
		rs.h.Set("x-amz-version-id", v.id)
		return nil, rs
	}
	return v, nil
}

// checkWrite enforces destination preconditions; cur is the live current
// version or nil.
func (c *call) checkWrite(cur *version) *resp {
	h := c.r.Header
	if inm := h.Get("If-None-Match"); inm != "" && c.s.condWrites.Load() {
		if cur != nil && etagListMatches(inm, cur.etag) {
			return c.precondFailed()
		}
	}
	if im := h.Get("If-Match"); im != "" {
		if cur == nil {
			return c.noSuchKey()
		}
		if !etagListMatches(im, cur.etag) {
			return c.precondFailed()
		}
	}
	return nil
}

func liveCurrent(b *bucket, key string) *version {
	if v := b.current(key); v != nil && !v.marker {
		return v
	}
	return nil
}

func (c *call) putObject() *resp {
	if rs := c.checkWrite(liveCurrent(c.b, c.key)); rs != nil {
		return rs
	}
	m := metaFromHeaders(c.r.Header)
	v := c.b.put(c.key, cloneBytes(c.body), m)
	rs := newResp(200)
	rs.h.Set("ETag", v.etag)
	echoSSE(rs.h, v.objMeta)
	if c.b.versionedHeader() {
		rs.h.Set("x-amz-version-id", v.id)
	}
	return rs
}

func echoSSE(h http.Header, m objMeta) {
	set := func(k, v string) {
		if v != "" {
			h.Set(k, v)
		}
	}
	set("x-amz-server-side-encryption", m.sse)
	set("x-amz-server-side-encryption-aws-kms-key-id", m.kmsKey)
	set("x-amz-server-side-encryption-customer-algorithm", m.ssecAlg)
	set("x-amz-server-side-encryption-customer-key-MD5", m.ssecMD5)
}

// parseRange parses a single-range Range header. ok=false means "ignore the
// header"; unsat=true means 416.
func parseRange(h string, size int64) (start, end int64, ok, unsat bool) {
	spec, found := strings.CutPrefix(h, "bytes=")
	if !found || strings.Contains(spec, ",") {
		return 0, 0, false, false
	}
	a, b, found := strings.Cut(spec, "-")
	if !found {
		return 0, 0, false, false
	}
	if a == "" { // suffix
		n, err := strconv.ParseInt(b, 10, 64)
		if err != nil || n < 0 {
			return 0, 0, false, false
		}
		if n == 0 || size == 0 {
			return 0, 0, false, true
		}
		return max(0, size-n), size - 1, true, false
	}
	s, err := strconv.ParseInt(a, 10, 64)
	if err != nil || s < 0 {
		return 0, 0, false, false
	}
	e := size - 1
	if b != "" {
		e, err = strconv.ParseInt(b, 10, 64)
		if err != nil || e < s {
			return 0, 0, false, false
		}
	}
	if s >= size {
		return 0, 0, false, true
	}
	return s, min(e, size-1), true, false
}

func (c *call) getObject() *resp {
	v, rs := c.resolve(c.b, c.key, c.q.Get("versionId"))
	if rs != nil {
		return rs
	}
	h := c.r.Header
	if im := h.Get("If-Match"); im != "" && !etagListMatches(im, v.etag) {
		return c.precondFailed()
	}
	if ius := h.Get("If-Unmodified-Since"); ius != "" {
		if t, err := http.ParseTime(ius); err == nil && v.modified.After(t) {
			return c.precondFailed()
		}
	}
	notModified := false
	if inm := h.Get("If-None-Match"); inm != "" {
		notModified = etagListMatches(inm, v.etag)
	} else if ims := h.Get("If-Modified-Since"); ims != "" {
		if t, err := http.ParseTime(ims); err == nil && !v.modified.After(t) {
			notModified = true
		}
	}
	if notModified {
		rs := newResp(304)
		rs.h.Set("ETag", v.etag)
		rs.h.Set("Last-Modified", v.modified.UTC().Format(http.TimeFormat))
		return rs
	}
	rs = newResp(200)
	c.b.objectHeaders(rs.h, v)
	for q, hdr := range map[string]string{"response-content-type": "Content-Type", "response-content-disposition": "Content-Disposition",
		"response-cache-control": "Cache-Control", "response-content-encoding": "Content-Encoding",
		"response-content-language": "Content-Language", "response-expires": "Expires"} {
		if val := c.q.Get(q); val != "" {
			rs.h.Set(hdr, val)
		}
	}
	data := v.data
	if rh := h.Get("Range"); rh != "" {
		s, e, ok, unsat := parseRange(rh, int64(len(data)))
		if unsat {
			return c.fail(416, "InvalidRange", "The requested range is not satisfiable")
		}
		if ok {
			rs.status = 206
			rs.h.Set("Content-Range", "bytes "+strconv.FormatInt(s, 10)+"-"+strconv.FormatInt(e, 10)+"/"+strconv.Itoa(len(data)))
			data = data[s : e+1]
		}
	}
	rs.h.Set("Content-Type", rs.h.Get("Content-Type"))
	rs.body = data
	rs.headLen = int64(len(data))
	return rs
}

// ---- copy ----

type copySrc struct {
	bucket, key, versionID string
}

func parseCopySource(raw string) (copySrc, bool) {
	raw = strings.TrimPrefix(raw, "/")
	path, query, _ := strings.Cut(raw, "?")
	// Like AWS, a literal "+" in the copy source means a space.
	path, err := url.QueryUnescape(path)
	if err != nil {
		return copySrc{}, false
	}
	b, k, ok := strings.Cut(path, "/")
	if !ok || b == "" || k == "" {
		return copySrc{}, false
	}
	q, err := url.ParseQuery(query)
	if err != nil {
		return copySrc{}, false
	}
	return copySrc{b, k, q.Get("versionId")}, true
}

func (c *call) resolveSource() (*version, copySrc, *resp) {
	src, ok := parseCopySource(c.r.Header.Get("x-amz-copy-source"))
	if !ok {
		return nil, src, c.fail(400, "InvalidArgument", "Copy Source must mention the source bucket and key: sourcebucket/sourcekey")
	}
	sb := c.s.buckets[src.bucket]
	if sb == nil {
		return nil, src, c.noSuchBucket()
	}
	v, rs := c.resolve(sb, src.key, src.versionID)
	if rs != nil {
		if rs.status == 405 || (rs.status == 404 && rs.h.Get("x-amz-delete-marker") != "") {
			rs = c.fail(404, "NoSuchKey", "The specified key does not exist.")
		}
		return nil, src, rs
	}
	h := c.r.Header
	if im := h.Get("x-amz-copy-source-if-match"); im != "" && !etagListMatches(im, v.etag) {
		return nil, src, c.precondFailed()
	}
	if inm := h.Get("x-amz-copy-source-if-none-match"); inm != "" && etagListMatches(inm, v.etag) {
		return nil, src, c.precondFailed()
	}
	if s := h.Get("x-amz-copy-source-if-unmodified-since"); s != "" {
		if t, err := http.ParseTime(s); err == nil && v.modified.After(t) {
			return nil, src, c.precondFailed()
		}
	}
	if s := h.Get("x-amz-copy-source-if-modified-since"); s != "" {
		if t, err := http.ParseTime(s); err == nil && !v.modified.After(t) {
			return nil, src, c.precondFailed()
		}
	}
	return v, src, nil
}

func (c *call) copyObject() *resp {
	sv, src, rs := c.resolveSource()
	if rs != nil {
		return rs
	}
	if rs := c.checkWrite(liveCurrent(c.b, c.key)); rs != nil {
		return rs
	}
	h := c.r.Header
	req := metaFromHeaders(h)
	var m objMeta
	if strings.EqualFold(h.Get("x-amz-metadata-directive"), "REPLACE") {
		m = req
	} else {
		m = sv.objMeta.clone()
		m.storageClass = req.storageClass
	}
	m.sse, m.kmsKey, m.ssecAlg, m.ssecMD5 = req.sse, req.kmsKey, req.ssecAlg, req.ssecMD5
	m.acl = req.acl
	if strings.EqualFold(h.Get("x-amz-tagging-directive"), "REPLACE") {
		m.tags = req.tags
	} else {
		m.tags = append([]tag(nil), sv.tags...)
	}
	sameObject := src.bucket == c.b.name && src.key == c.key
	if sameObject && !c.b.versionedHeader() && !strings.EqualFold(h.Get("x-amz-metadata-directive"), "REPLACE") &&
		!strings.EqualFold(h.Get("x-amz-tagging-directive"), "REPLACE") && h.Get("x-amz-storage-class") == "" &&
		req.sse == "" && req.kmsKey == "" {
		return c.fail(400, "InvalidRequest", "This copy request is illegal because it is trying to copy an object to itself without changing the object's metadata, storage class, website redirect location or encryption attributes.")
	}
	nv := c.b.put(c.key, sv.data, m)
	rs = c.xmlResp(200, func(x *xw) {
		x.root("CopyObjectResult")
		x.el("ETag", nv.etag)
		x.elt("LastModified", nv.modified)
		x.end("CopyObjectResult")
	})
	if c.s.buckets[src.bucket].versionedHeader() {
		rs.h.Set("x-amz-copy-source-version-id", sv.id)
	}
	if c.b.versionedHeader() {
		rs.h.Set("x-amz-version-id", nv.id)
	}
	echoSSE(rs.h, m)
	return rs
}

// ---- delete ----

func (c *call) deleteObject() *resp {
	vid := c.q.Get("versionId")
	r := c.b.deleteKey(c.key, vid)
	rs := c.empty(204)
	if vid != "" {
		rs.h.Set("x-amz-version-id", vid)
		if r.marker {
			rs.h.Set("x-amz-delete-marker", "true")
		}
	} else if r.created {
		rs.h.Set("x-amz-delete-marker", "true")
		rs.h.Set("x-amz-version-id", r.versionID)
	}
	return rs
}

func (c *call) deleteObjects() *resp {
	var req struct {
		Quiet   bool `xml:"Quiet"`
		Objects []struct {
			Key       string `xml:"Key"`
			VersionID string `xml:"VersionId"`
		} `xml:"Object"`
	}
	if err := xml.Unmarshal(c.body, &req); err != nil || len(req.Objects) == 0 || len(req.Objects) > 1000 {
		return c.fail(400, "MalformedXML", "The XML you provided was not well-formed or did not validate against our published schema")
	}
	drop := map[string]bool{}
	if c.fault != nil {
		for _, k := range c.fault.DropDeleteAcks {
			drop[k] = true
		}
	}
	return c.xmlResp(200, func(x *xw) {
		x.root("DeleteResult")
		for _, o := range req.Objects {
			r := c.b.deleteKey(o.Key, o.VersionID)
			if req.Quiet || drop[o.Key] {
				continue
			}
			x.open("Deleted")
			x.el("Key", o.Key)
			if o.VersionID != "" {
				x.el("VersionId", o.VersionID)
			}
			if r.marker {
				x.elb("DeleteMarker", true)
				x.el("DeleteMarkerVersionId", r.versionID)
			}
			x.end("Deleted")
		}
		x.end("DeleteResult")
	})
}
