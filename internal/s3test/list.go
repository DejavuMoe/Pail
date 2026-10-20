package s3test

import (
	"encoding/base64"
	"strconv"
	"strings"
)

func (c *call) maxParam(name string, def int) (int, *resp) {
	v := c.q.Get(name)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, c.fail(400, "InvalidArgument", name+" must be a non-negative integer")
	}
	return min(n, def), nil
}

type listEntry struct {
	key string
	v   *version
}

func (c *call) listObjects(v2 bool) *resp {
	q := c.q
	prefix, delim := q.Get("prefix"), q.Get("delimiter")
	maxKeys, rs := c.maxParam("max-keys", 1000)
	if rs != nil {
		return rs
	}
	enc := q.Get("encoding-type") == "url"
	after, skipPrefix := "", ""
	token := q.Get("continuation-token")
	if v2 {
		after = q.Get("start-after")
		if token != "" {
			raw, err := base64.RawURLEncoding.DecodeString(token)
			if err != nil || len(raw) == 0 || (raw[0] != 'K' && raw[0] != 'P') {
				return c.fail(400, "InvalidArgument", "The continuation token provided is incorrect")
			}
			after = string(raw[1:])
			if raw[0] == 'P' {
				skipPrefix = after
			}
		}
	} else {
		after = q.Get("marker")
		if delim != "" && strings.HasSuffix(after, delim) {
			skipPrefix = after
		}
	}

	var contents []listEntry
	var prefixes []string
	seen := map[string]bool{}
	truncated := false
	last, lastKind := "", byte(0)
	items := 0
	if maxKeys > 0 {
		for _, k := range c.b.sortedKeys() {
			if !strings.HasPrefix(k, prefix) || (after != "" && k <= after) || (skipPrefix != "" && strings.HasPrefix(k, skipPrefix)) {
				continue
			}
			cur := c.b.current(k)
			if cur == nil || cur.marker {
				continue
			}
			if delim != "" {
				rest := k[len(prefix):]
				if i := strings.Index(rest, delim); i >= 0 {
					cp := prefix + rest[:i+len(delim)]
					if seen[cp] {
						continue
					}
					if items == maxKeys {
						truncated = true
						break
					}
					seen[cp] = true
					prefixes = append(prefixes, cp)
					items++
					last, lastKind = cp, 'P'
					continue
				}
			}
			if items == maxKeys {
				truncated = true
				break
			}
			contents = append(contents, listEntry{k, cur})
			items++
			last, lastKind = k, 'K'
		}
	}
	return c.xmlResp(200, func(x *xw) {
		x.root("ListBucketResult")
		x.el("Name", c.b.name)
		x.el("Prefix", encodeKey(enc, prefix))
		if v2 {
			if after != "" && token == "" {
				x.el("StartAfter", encodeKey(enc, after))
			}
			x.eli("KeyCount", int64(items))
		} else {
			x.el("Marker", encodeKey(enc, q.Get("marker")))
		}
		x.eli("MaxKeys", int64(maxKeys))
		if delim != "" {
			x.el("Delimiter", encodeKey(enc, delim))
		}
		if enc {
			x.el("EncodingType", "url")
		}
		x.elb("IsTruncated", truncated)
		if v2 {
			if token != "" {
				x.el("ContinuationToken", token)
			}
			if truncated {
				x.el("NextContinuationToken", base64.RawURLEncoding.EncodeToString([]byte(string(lastKind)+last)))
			}
		} else if truncated && delim != "" {
			x.el("NextMarker", encodeKey(enc, last))
		}
		for _, e := range contents {
			x.open("Contents")
			x.el("Key", encodeKey(enc, e.key))
			x.elt("LastModified", e.v.modified)
			x.el("ETag", e.v.etag)
			x.eli("Size", int64(len(e.v.data)))
			if q.Get("fetch-owner") == "true" || !v2 {
				x.owner()
			}
			x.el("StorageClass", e.v.storageClass)
			x.end("Contents")
		}
		for _, p := range prefixes {
			x.open("CommonPrefixes")
			x.el("Prefix", encodeKey(enc, p))
			x.end("CommonPrefixes")
		}
		x.end("ListBucketResult")
	})
}

func (c *call) listVersions() *resp {
	q := c.q
	prefix, delim := q.Get("prefix"), q.Get("delimiter")
	maxKeys, rs := c.maxParam("max-keys", 1000)
	if rs != nil {
		return rs
	}
	enc := q.Get("encoding-type") == "url"
	km, vm := q.Get("key-marker"), q.Get("version-id-marker")
	skipPrefix := ""
	if vm == "" && delim != "" && strings.HasSuffix(km, delim) {
		skipPrefix = km
	}

	type item struct {
		key    string
		v      *version
		latest bool
	}
	var items []item
	var prefixes []string
	seen := map[string]bool{}
	truncated := false
	lastKey, lastVer := "", ""
	count := 0
	if maxKeys > 0 {
	keys:
		for _, k := range c.b.sortedKeys() {
			if !strings.HasPrefix(k, prefix) || (km != "" && k < km) || (km != "" && k == km && vm == "") ||
				(skipPrefix != "" && strings.HasPrefix(k, skipPrefix)) {
				continue
			}
			skipping := km != "" && k == km && vm != ""
			for i, v := range c.b.objects[k] {
				if skipping {
					if v.id == vm {
						skipping = false
					}
					continue
				}
				if delim != "" {
					rest := k[len(prefix):]
					if j := strings.Index(rest, delim); j >= 0 {
						cp := prefix + rest[:j+len(delim)]
						if seen[cp] {
							continue keys
						}
						if count == maxKeys {
							truncated = true
							break keys
						}
						seen[cp] = true
						prefixes = append(prefixes, cp)
						count++
						lastKey, lastVer = cp, ""
						continue keys
					}
				}
				if count == maxKeys {
					truncated = true
					break keys
				}
				items = append(items, item{k, v, i == 0})
				count++
				lastKey, lastVer = k, v.id
			}
		}
	}
	return c.xmlResp(200, func(x *xw) {
		x.root("ListVersionsResult")
		x.el("Name", c.b.name)
		x.el("Prefix", encodeKey(enc, prefix))
		x.el("KeyMarker", encodeKey(enc, km))
		x.el("VersionIdMarker", vm)
		if truncated {
			x.el("NextKeyMarker", encodeKey(enc, lastKey))
			x.el("NextVersionIdMarker", lastVer)
		}
		x.eli("MaxKeys", int64(maxKeys))
		if delim != "" {
			x.el("Delimiter", encodeKey(enc, delim))
		}
		if enc {
			x.el("EncodingType", "url")
		}
		x.elb("IsTruncated", truncated)
		for _, it := range items {
			name := "Version"
			if it.v.marker {
				name = "DeleteMarker"
			}
			x.open(name)
			x.el("Key", encodeKey(enc, it.key))
			x.el("VersionId", it.v.id)
			x.elb("IsLatest", it.latest)
			x.elt("LastModified", it.v.modified)
			if !it.v.marker {
				x.el("ETag", it.v.etag)
				x.eli("Size", int64(len(it.v.data)))
			}
			x.owner()
			if !it.v.marker {
				x.el("StorageClass", it.v.storageClass)
			}
			x.end(name)
		}
		for _, p := range prefixes {
			x.open("CommonPrefixes")
			x.el("Prefix", encodeKey(enc, p))
			x.end("CommonPrefixes")
		}
		x.end("ListVersionsResult")
	})
}
