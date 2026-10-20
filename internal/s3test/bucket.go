package s3test

import (
	"encoding/xml"
	"net/http"
	"regexp"
	"sort"
	"strings"
)

// genericSub describes a bucket subresource stored as an opaque document.
type genericSub struct {
	status int
	code   string
	msg    string
	def    string // default document when not found is not an error
	mime   string
}

const xmlDecl = xmlNS

var genericSubs = map[string]genericSub{
	"policy":            {404, "NoSuchBucketPolicy", "The bucket policy does not exist", "", "application/json"},
	"cors":              {404, "NoSuchCORSConfiguration", "The CORS configuration does not exist", "", "application/xml"},
	"lifecycle":         {404, "NoSuchLifecycleConfiguration", "The lifecycle configuration does not exist", "", "application/xml"},
	"encryption":        {404, "ServerSideEncryptionConfigurationNotFoundError", "The server side encryption configuration was not found", "", "application/xml"},
	"website":           {404, "NoSuchWebsiteConfiguration", "The specified bucket does not have a website configuration", "", "application/xml"},
	"publicAccessBlock": {404, "NoSuchPublicAccessBlockConfiguration", "The public access block configuration was not found", "", "application/xml"},
	"ownershipControls": {404, "OwnershipControlsNotFoundError", "The bucket ownership controls were not found", "", "application/xml"},
	"object-lock":       {404, "ObjectLockConfigurationNotFoundError", "Object Lock configuration does not exist for this bucket", "", "application/xml"},
	"replication":       {404, "ReplicationConfigurationNotFoundError", "The replication configuration was not found", "", "application/xml"},
	"logging":           {0, "", "", `<BucketLoggingStatus xmlns="` + xmlDecl + `"/>`, "application/xml"},
	"requestPayment":    {0, "", "", `<RequestPaymentConfiguration xmlns="` + xmlDecl + `"><Payer>BucketOwner</Payer></RequestPaymentConfiguration>`, "application/xml"},
	"accelerate":        {0, "", "", `<AccelerateConfiguration xmlns="` + xmlDecl + `"/>`, "application/xml"},
	"notification":      {0, "", "", `<NotificationConfiguration xmlns="` + xmlDecl + `"/>`, "application/xml"},
}

func (c *call) genericName() string {
	for name := range genericSubs {
		if c.q.Has(name) {
			return name
		}
	}
	return ""
}

var bucketNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)

func (c *call) dispatch(bname string) *resp {
	m := c.r.Method
	if bname == "" {
		if m == http.MethodGet {
			return c.listBuckets()
		}
		return c.fail(405, "MethodNotAllowed", "The specified method is not allowed against this resource.")
	}
	c.b = c.s.buckets[bname]
	if c.key == "" {
		return c.bucketOp(bname, m)
	}
	if c.b == nil {
		return c.noSuchBucket()
	}
	return c.objectOp(m)
}

func (c *call) methodNotAllowed() *resp {
	return c.fail(405, "MethodNotAllowed", "The specified method is not allowed against this resource.")
}

func (c *call) bucketOp(bname, m string) *resp {
	if m == http.MethodPut && c.b == nil && c.q.Get("acl") == "" && c.genericName() == "" && !c.q.Has("tagging") && !c.q.Has("versioning") {
		return c.createBucket(bname)
	}
	if c.b == nil {
		return c.noSuchBucket()
	}
	q := c.q
	gen := c.genericName()
	switch m {
	case http.MethodHead:
		rs := newResp(200)
		rs.h.Set("x-amz-bucket-region", c.region())
		return rs
	case http.MethodGet:
		switch {
		case q.Has("location"):
			return c.xmlResp(200, func(x *xw) {
				x.root("LocationConstraint")
				x.WriteString(c.region2())
				x.end("LocationConstraint")
			})
		case q.Has("acl"):
			return c.aclDoc(c.b.acl)
		case q.Has("tagging"):
			if !c.b.tagsSet {
				return c.fail(404, "NoSuchTagSet", "The TagSet does not exist")
			}
			return c.taggingDoc(c.b.tags)
		case q.Has("versioning"):
			return c.xmlResp(200, func(x *xw) {
				x.root("VersioningConfiguration")
				if c.b.versioning != "" {
					x.el("Status", c.b.versioning)
				}
				x.end("VersioningConfiguration")
			})
		case q.Has("uploads"):
			return c.listUploads()
		case q.Has("versions"):
			return c.listVersions()
		case gen != "":
			return c.getGeneric(gen)
		}
		if q.Get("list-type") == "2" {
			return c.listObjects(true)
		}
		return c.listObjects(false)
	case http.MethodPut:
		switch {
		case q.Has("acl"):
			c.b.acl = parseACLRequest(c.r.Header, c.body)
			return c.empty(200)
		case q.Has("tagging"):
			tags, rs := c.parseTagging()
			if rs != nil {
				return rs
			}
			c.b.tags, c.b.tagsSet = tags, true
			return c.empty(204)
		case q.Has("versioning"):
			var req struct {
				Status string `xml:"Status"`
			}
			if err := xml.Unmarshal(c.body, &req); err != nil || (req.Status != "Enabled" && req.Status != "Suspended") {
				return c.fail(400, "MalformedXML", "The XML you provided was not well-formed or did not validate against our published schema")
			}
			c.b.versioning = req.Status
			return c.empty(200)
		case gen != "":
			c.b.sub[gen] = cloneBytes(c.body)
			if gen == "policy" {
				return c.empty(204)
			}
			return c.empty(200)
		}
		return c.createBucket(bname) // existing bucket
	case http.MethodDelete:
		switch {
		case q.Has("tagging"):
			c.b.tags, c.b.tagsSet = nil, false
			return c.empty(204)
		case gen != "":
			delete(c.b.sub, gen)
			return c.empty(204)
		}
		if c.b.hasObjects() {
			return c.fail(409, "BucketNotEmpty", "The bucket you tried to delete is not empty")
		}
		delete(c.s.buckets, bname)
		return c.empty(204)
	case http.MethodPost:
		if q.Has("delete") {
			return c.deleteObjects()
		}
	}
	return c.methodNotAllowed()
}

func (c *call) region() string {
	if c.b.location == "" {
		return "us-east-1"
	}
	return c.b.location
}

func (c *call) region2() string {
	if c.b.location == "us-east-1" {
		return ""
	}
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(c.b.location))
	return b.String()
}

func (c *call) listBuckets() *resp {
	names := make([]string, 0, len(c.s.buckets))
	for n := range c.s.buckets {
		names = append(names, n)
	}
	sort.Strings(names)
	return c.xmlResp(200, func(x *xw) {
		x.root("ListAllMyBucketsResult")
		x.owner()
		x.open("Buckets")
		for _, n := range names {
			b := c.s.buckets[n]
			x.open("Bucket")
			x.el("Name", n)
			x.elt("CreationDate", b.created)
			x.el("BucketRegion", func() string {
				if b.location == "" {
					return "us-east-1"
				}
				return b.location
			}())
			x.end("Bucket")
		}
		x.end("Buckets")
		x.end("ListAllMyBucketsResult")
	})
}

func (c *call) createBucket(name string) *resp {
	if !bucketNameRE.MatchString(name) {
		return c.fail(400, "InvalidBucketName", "The specified bucket is not valid.")
	}
	if c.b != nil {
		return c.fail(409, "BucketAlreadyOwnedByYou", "Your previous request to create the named bucket succeeded and you already own it.")
	}
	var cfg struct {
		LocationConstraint string `xml:"LocationConstraint"`
	}
	if len(c.body) > 0 {
		if err := xml.Unmarshal(c.body, &cfg); err != nil {
			return c.fail(400, "MalformedXML", "The XML you provided was not well-formed or did not validate against our published schema")
		}
	}
	b := newBucket(name)
	b.location = cfg.LocationConstraint
	if canned := c.r.Header.Get("x-amz-acl"); canned != "" {
		b.acl.canned = canned
	}
	if strings.EqualFold(c.r.Header.Get("x-amz-bucket-object-lock-enabled"), "true") {
		b.versioning = "Enabled"
		b.sub["object-lock"] = []byte(`<ObjectLockConfiguration xmlns="` + xmlNS + `"><ObjectLockEnabled>Enabled</ObjectLockEnabled></ObjectLockConfiguration>`)
	}
	c.s.buckets[name] = b
	rs := newResp(200)
	rs.h.Set("Location", "/"+name)
	return rs
}

func (c *call) getGeneric(name string) *resp {
	g := genericSubs[name]
	body, ok := c.b.sub[name]
	if !ok {
		if g.def == "" {
			return c.fail(g.status, g.code, g.msg)
		}
		body = []byte(g.def)
		if len(body) > 0 && !strings.HasPrefix(string(body), "<?xml") {
			body = []byte(xml.Header + string(body))
		}
	}
	rs := newResp(200)
	rs.h.Set("Content-Type", g.mime)
	rs.body = body
	return rs
}

// ---- tagging / ACL documents shared by bucket and object ----

func (c *call) parseTagging() ([]tag, *resp) {
	var req struct {
		Tags []struct {
			Key   string `xml:"Key"`
			Value string `xml:"Value"`
		} `xml:"TagSet>Tag"`
	}
	if err := xml.Unmarshal(c.body, &req); err != nil {
		return nil, c.fail(400, "MalformedXML", "The XML you provided was not well-formed or did not validate against our published schema")
	}
	if len(req.Tags) > 10 {
		return nil, c.fail(400, "BadRequest", "Object tags cannot be greater than 10")
	}
	seen := map[string]bool{}
	var out []tag
	for _, t := range req.Tags {
		if t.Key == "" || seen[t.Key] {
			return nil, c.fail(400, "InvalidTag", "The TagSet contains an empty or duplicate key")
		}
		seen[t.Key] = true
		out = append(out, tag{t.Key, t.Value})
	}
	return out, nil
}

func (c *call) taggingDoc(tags []tag) *resp {
	return c.xmlResp(200, func(x *xw) {
		x.root("Tagging")
		x.open("TagSet")
		for _, t := range tags {
			x.open("Tag")
			x.el("Key", t.Key)
			x.el("Value", t.Value)
			x.end("Tag")
		}
		x.end("TagSet")
		x.end("Tagging")
	})
}

func parseACLRequest(h http.Header, body []byte) aclState {
	if strings.Contains(string(body), "AccessControlPolicy") {
		return aclState{raw: cloneBytes(body)}
	}
	return aclState{canned: h.Get("x-amz-acl")}
}

func (c *call) aclDoc(a aclState) *resp {
	if len(a.raw) > 0 {
		rs := newResp(200)
		rs.h.Set("Content-Type", "application/xml")
		rs.body = a.raw
		return rs
	}
	const xsi = ` xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"`
	grant := func(x *xw, grantee, perm string) {
		x.open("Grant")
		x.WriteString(grantee)
		x.el("Permission", perm)
		x.end("Grant")
	}
	owner := `<Grantee` + xsi + ` xsi:type="CanonicalUser"><ID>` + ownerID + `</ID><DisplayName>` + ownerName + `</DisplayName></Grantee>`
	group := func(g string) string {
		return `<Grantee` + xsi + ` xsi:type="Group"><URI>http://acs.amazonaws.com/groups/global/` + g + `</URI></Grantee>`
	}
	return c.xmlResp(200, func(x *xw) {
		x.root("AccessControlPolicy")
		x.owner()
		x.open("AccessControlList")
		grant(x, owner, "FULL_CONTROL")
		switch a.canned {
		case "public-read":
			grant(x, group("AllUsers"), "READ")
		case "public-read-write":
			grant(x, group("AllUsers"), "READ")
			grant(x, group("AllUsers"), "WRITE")
		case "authenticated-read":
			grant(x, group("AuthenticatedUsers"), "READ")
		}
		x.end("AccessControlList")
		x.end("AccessControlPolicy")
	})
}
