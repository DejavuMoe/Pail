package s3test

import (
	"crypto/md5"
	"encoding/xml"
	"sort"
	"strconv"
	"strings"
)

func (c *call) upload() (*upload, *resp) {
	u := c.b.uploads[c.q.Get("uploadId")]
	if u == nil || u.key != c.key {
		return nil, c.fail(404, "NoSuchUpload", "The specified upload does not exist. The upload ID may be invalid, or the upload may have been aborted or completed.")
	}
	return u, nil
}

func (c *call) createUpload() *resp {
	u := &upload{id: newID(), key: c.key, initiated: now(), meta: metaFromHeaders(c.r.Header), parts: map[int]*part{}}
	c.b.uploads[u.id] = u
	rs := c.xmlResp(200, func(x *xw) {
		x.root("InitiateMultipartUploadResult")
		x.el("Bucket", c.b.name)
		x.el("Key", c.key)
		x.el("UploadId", u.id)
		x.end("InitiateMultipartUploadResult")
	})
	echoSSE(rs.h, u.meta)
	return rs
}

func (c *call) partNumber() (int, *resp) {
	n, err := strconv.Atoi(c.q.Get("partNumber"))
	if err != nil || n < 1 || n > 10000 {
		return 0, c.fail(400, "InvalidArgument", "Part number must be an integer between 1 and 10000, inclusive")
	}
	return n, nil
}

func (c *call) uploadPart() *resp {
	u, rs := c.upload()
	if rs != nil {
		return rs
	}
	n, rs := c.partNumber()
	if rs != nil {
		return rs
	}
	data := cloneBytes(c.body)
	sum := md5.Sum(data)
	u.parts[n] = &part{data: data, etag: quoteETag(sum), sum: sum, modified: now()}
	rs = newResp(200)
	rs.h.Set("ETag", u.parts[n].etag)
	echoSSE(rs.h, u.meta)
	return rs
}

func (c *call) uploadPartCopy() *resp {
	u, rs := c.upload()
	if rs != nil {
		return rs
	}
	n, rs := c.partNumber()
	if rs != nil {
		return rs
	}
	sv, src, rs := c.resolveSource()
	if rs != nil {
		return rs
	}
	data := sv.data
	if rng := c.r.Header.Get("x-amz-copy-source-range"); rng != "" {
		s, e, ok, unsat := parseRange(rng, int64(len(data)))
		if !ok || unsat {
			return c.fail(400, "InvalidArgument", "The x-amz-copy-source-range value must be of the form bytes=first-last where first and last are the zero-based offsets of the first and last bytes to copy")
		}
		data = data[s : e+1]
	}
	data = cloneBytes(data)
	sum := md5.Sum(data)
	p := &part{data: data, etag: quoteETag(sum), sum: sum, modified: now()}
	u.parts[n] = p
	rs = c.xmlResp(200, func(x *xw) {
		x.root("CopyPartResult")
		x.el("ETag", p.etag)
		x.elt("LastModified", p.modified)
		x.end("CopyPartResult")
	})
	if c.s.buckets[src.bucket].versionedHeader() {
		rs.h.Set("x-amz-copy-source-version-id", sv.id)
	}
	return rs
}

func (c *call) abortUpload() *resp {
	u, rs := c.upload()
	if rs != nil {
		return rs
	}
	delete(c.b.uploads, u.id)
	return c.empty(204)
}

func (c *call) completeUpload() *resp {
	u, rs := c.upload()
	if rs != nil {
		return rs
	}
	var req struct {
		Parts []struct {
			PartNumber int    `xml:"PartNumber"`
			ETag       string `xml:"ETag"`
		} `xml:"Part"`
	}
	if err := xml.Unmarshal(c.body, &req); err != nil || len(req.Parts) == 0 {
		return c.fail(400, "MalformedXML", "The XML you provided was not well-formed or did not validate against our published schema")
	}
	if rs := c.checkWrite(liveCurrent(c.b, c.key)); rs != nil {
		return rs
	}
	var data []byte
	var sums []byte
	prev := 0
	for _, rp := range req.Parts {
		if rp.PartNumber <= prev {
			return c.fail(400, "InvalidPartOrder", "The list of parts was not in ascending order. The parts list must be specified in order by part number.")
		}
		prev = rp.PartNumber
		p := u.parts[rp.PartNumber]
		if p == nil || (rp.ETag != "" && stripETag(rp.ETag) != stripETag(p.etag)) {
			return c.fail(400, "InvalidPart", "One or more of the specified parts could not be found. The part may not have been uploaded, or the specified entity tag may not match the part's entity tag.")
		}
		data = append(data, p.data...)
		sums = append(sums, p.sum[:]...)
	}
	m := u.meta.clone()
	v := c.b.put(c.key, data, m)
	final := md5.Sum(sums)
	v.etag = `"` + strings.TrimSuffix(strings.TrimPrefix(quoteETag(final), `"`), `"`) + "-" + strconv.Itoa(len(req.Parts)) + `"`
	delete(c.b.uploads, u.id)
	rs = c.xmlResp(200, func(x *xw) {
		x.root("CompleteMultipartUploadResult")
		x.el("Location", "http://"+c.r.Host+"/"+c.b.name+"/"+c.key)
		x.el("Bucket", c.b.name)
		x.el("Key", c.key)
		x.el("ETag", v.etag)
		x.end("CompleteMultipartUploadResult")
	})
	if c.b.versionedHeader() {
		rs.h.Set("x-amz-version-id", v.id)
	}
	echoSSE(rs.h, m)
	return rs
}

func (c *call) listParts() *resp {
	u, rs := c.upload()
	if rs != nil {
		return rs
	}
	maxParts, rs := c.maxParam("max-parts", 1000)
	if rs != nil {
		return rs
	}
	marker, _ := strconv.Atoi(c.q.Get("part-number-marker"))
	nums := make([]int, 0, len(u.parts))
	for n := range u.parts {
		if n > marker {
			nums = append(nums, n)
		}
	}
	sort.Ints(nums)
	truncated := false
	if len(nums) > maxParts {
		nums, truncated = nums[:maxParts], true
	}
	next := marker
	if len(nums) > 0 {
		next = nums[len(nums)-1]
	}
	return c.xmlResp(200, func(x *xw) {
		x.root("ListPartsResult")
		x.el("Bucket", c.b.name)
		x.el("Key", c.key)
		x.el("UploadId", u.id)
		x.eli("PartNumberMarker", int64(marker))
		if truncated {
			x.eli("NextPartNumberMarker", int64(next))
		}
		x.eli("MaxParts", int64(maxParts))
		x.elb("IsTruncated", truncated)
		for _, n := range nums {
			p := u.parts[n]
			x.open("Part")
			x.eli("PartNumber", int64(n))
			x.elt("LastModified", p.modified)
			x.el("ETag", p.etag)
			x.eli("Size", int64(len(p.data)))
			x.end("Part")
		}
		x.open("Initiator")
		x.el("ID", ownerID)
		x.el("DisplayName", ownerName)
		x.end("Initiator")
		x.owner()
		x.el("StorageClass", u.meta.storageClass)
		x.end("ListPartsResult")
	})
}

func (c *call) listUploads() *resp {
	q := c.q
	prefix, delim := q.Get("prefix"), q.Get("delimiter")
	maxUploads, rs := c.maxParam("max-uploads", 1000)
	if rs != nil {
		return rs
	}
	enc := q.Get("encoding-type") == "url"
	km, um := q.Get("key-marker"), q.Get("upload-id-marker")
	var all []*upload
	for _, u := range c.b.uploads {
		if strings.HasPrefix(u.key, prefix) {
			all = append(all, u)
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].key != all[j].key {
			return all[i].key < all[j].key
		}
		if !all[i].initiated.Equal(all[j].initiated) {
			return all[i].initiated.Before(all[j].initiated)
		}
		return all[i].id < all[j].id
	})
	var ups []*upload
	var prefixes []string
	seen := map[string]bool{}
	truncated := false
	lastKey, lastID := "", ""
	count := 0
	for _, u := range all {
		if km != "" {
			if u.key < km || (u.key == km && (um == "" || u.id <= um)) {
				continue
			}
		}
		if delim != "" {
			rest := u.key[len(prefix):]
			if i := strings.Index(rest, delim); i >= 0 {
				cp := prefix + rest[:i+len(delim)]
				if seen[cp] || (km != "" && strings.HasPrefix(km, cp) && um == "") {
					continue
				}
				if count == maxUploads {
					truncated = true
					break
				}
				seen[cp] = true
				prefixes = append(prefixes, cp)
				count++
				lastKey, lastID = cp, ""
				continue
			}
		}
		if count == maxUploads {
			truncated = true
			break
		}
		ups = append(ups, u)
		count++
		lastKey, lastID = u.key, u.id
	}
	return c.xmlResp(200, func(x *xw) {
		x.root("ListMultipartUploadsResult")
		x.el("Bucket", c.b.name)
		x.el("KeyMarker", encodeKey(enc, km))
		x.el("UploadIdMarker", um)
		if truncated {
			x.el("NextKeyMarker", encodeKey(enc, lastKey))
			x.el("NextUploadIdMarker", lastID)
		}
		if delim != "" {
			x.el("Delimiter", encodeKey(enc, delim))
		}
		x.el("Prefix", encodeKey(enc, prefix))
		x.eli("MaxUploads", int64(maxUploads))
		if enc {
			x.el("EncodingType", "url")
		}
		x.elb("IsTruncated", truncated)
		for _, u := range ups {
			x.open("Upload")
			x.el("Key", encodeKey(enc, u.key))
			x.el("UploadId", u.id)
			x.open("Initiator")
			x.el("ID", ownerID)
			x.el("DisplayName", ownerName)
			x.end("Initiator")
			x.owner()
			x.el("StorageClass", u.meta.storageClass)
			x.elt("Initiated", u.initiated)
			x.end("Upload")
		}
		for _, p := range prefixes {
			x.open("CommonPrefixes")
			x.el("Prefix", encodeKey(enc, p))
			x.end("CommonPrefixes")
		}
		x.end("ListMultipartUploadsResult")
	})
}
