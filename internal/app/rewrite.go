package app

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/DejavuMoe/Pail/internal/storage"
)

// This file implements the in-place attribute rewrites: SetStorageClass,
// SetEncryption and SetProperties (APP_API.md section 6, invariant 3).
//
// S3 cannot edit these attributes; the object is copied onto itself with
// MetadataDirective REPLACE. Every header and metadata entry that the
// operation does not change is read from a HEAD and sent again; tags are kept
// with TaggingDirective COPY, the ACL is read before and written back after
// (skipped only where ACLs are disabled or unsupported; if the ACL cannot be
// read for any other reason, e.g. AccessDenied, the item fails before the copy).
// Objects above 5 GiB go through storage.Copy's multipart copy with the same
// headers. In a versioned bucket every rewrite creates a NEW CURRENT VERSION;
// older versions keep their old attributes. The copy is pinned to the ETag
// that was read, so a concurrent upload makes the item fail (412) instead of
// being overwritten with stale content.
//
// Not carried over: checksums of the old object (the copy computes its own).
// Object Lock mode, retain-until date and legal hold are carried over.

// bkRewrite describes one kind of rewrite.
type bkRewrite struct {
	op, title string
	// mutate edits the self-copy request (already filled with the object's
	// current attributes). It returns a non-empty reason to skip the object
	// because nothing would change.
	mutate func(in *s3.CopyObjectInput, h *s3.HeadObjectOutput, class string) (skip string, err error)
	prep   func(ctx context.Context) error // runs once at task start (secret lookup)
	// verify, when set, checks a HEAD taken after the rewrite and returns why
	// the change did not take effect. Some servers accept the copy with 200
	// and silently keep the old class or encryption.
	verify   func(h *s3.HeadObjectOutput) string
	headOpts func(in *s3.HeadObjectInput) // extra HEAD headers for verify (SSE-C key)
}

// bkNotApplied is the item error when the server accepted a rewrite but the
// object does not show the requested attribute.
const bkNotApplied = "the server accepted the change but did not apply it: "

func (s *Service) bkSubmitRewrite(loc Location, scope bkScope, rw *bkRewrite) string {
	return s.submit(s.bkRewriteSpec(loc, scope, rw))
}

func (s *Service) bkRewriteSpec(loc Location, scope bkScope, rw *bkRewrite) taskSpec {
	st := &bkState{}
	return taskSpec{
		Kind:   KindOther,
		Title:  rw.title,
		Detail: bkURL(loc),
		Work: func(ctx context.Context, r *run) error {
			c, err := s.bucketClient(ctx, rw.op, loc)
			if err != nil {
				return err
			}
			if rw.prep != nil {
				if err := rw.prep(ctx); err != nil {
					return err
				}
			}
			pipe := &bkPipe{
				workers: s.bkWorkers(), batch: 1, lister: bkListObjects(c, loc.Bucket),
				process: func(ctx context.Context, b []bkObj) []ItemResult {
					return []ItemResult{s.bkRewriteOne(ctx, r, c, loc, b[0], rw)}
				},
			}
			rem, err := pipe.run(ctx, r, scope)
			st.set(rem)
			return err
		},
		Retry: func(failed []ItemResult) (taskSpec, bool) {
			sc, ok := st.retryScope(failed)
			if !ok {
				return taskSpec{}, false
			}
			return s.bkRewriteSpec(loc, sc, rw), true
		},
	}
}

// bkRestoreDone reports whether an x-amz-restore value says that the restore
// finished (ongoing-request="false").
func bkRestoreDone(v string) bool { return strings.Contains(v, `ongoing-request="false"`) }

// bkSelfCopy builds a CopyObject request that rewrites bucket/key unchanged
// from its HEAD.
func bkSelfCopy(bucket, key string, h *s3.HeadObjectOutput, class string) *s3.CopyObjectInput {
	in := &s3.CopyObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key),
		CopySource: aws.String(storage.CopySource(bucket, key, "")), CopySourceIfMatch: h.ETag,
		MetadataDirective: types.MetadataDirectiveReplace, TaggingDirective: types.TaggingDirectiveCopy,
		ContentType: h.ContentType, ContentEncoding: h.ContentEncoding, ContentLanguage: h.ContentLanguage,
		ContentDisposition: h.ContentDisposition, CacheControl: h.CacheControl,
		Metadata:                maps.Clone(h.Metadata),
		WebsiteRedirectLocation: h.WebsiteRedirectLocation,
		ServerSideEncryption:    h.ServerSideEncryption, SSEKMSKeyId: h.SSEKMSKeyId,
	}
	if aws.ToBool(h.BucketKeyEnabled) {
		in.BucketKeyEnabled = aws.Bool(true)
	}
	if h.ExpiresString != nil {
		if t, err := http.ParseTime(*h.ExpiresString); err == nil {
			in.Expires = aws.Time(t)
		}
	} else if h.Expires != nil {
		in.Expires = h.Expires
	}
	if class != "" && class != string(types.StorageClassStandard) {
		in.StorageClass = types.StorageClass(class)
	}
	if h.ObjectLockMode != "" {
		in.ObjectLockMode = h.ObjectLockMode
		in.ObjectLockRetainUntilDate = h.ObjectLockRetainUntilDate
	}
	if h.ObjectLockLegalHoldStatus == types.ObjectLockLegalHoldStatusOn {
		in.ObjectLockLegalHoldStatus = types.ObjectLockLegalHoldStatusOn
	}
	return in
}

func (s *Service) bkRewriteOne(ctx context.Context, r *run, c *storage.Client, loc Location, o bkObj, rw *bkRewrite) ItemResult {
	res := ItemResult{Key: o.Key}
	if o.VersionID != "" {
		return bkFailf(res, "a specific version cannot be rewritten; only current objects")
	}
	h, err := c.S3().HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(loc.Bucket), Key: aws.String(o.Key)})
	if err != nil {
		return bkFail(ctx, res, classify("Head", err))
	}
	size := aws.ToInt64(h.ContentLength)
	if !o.Resolved {
		r.AddTotal(size, 0)
	}
	progress := bkProgress(r)
	if aws.ToString(h.SSECustomerAlgorithm) != "" {
		return bkFailf(res, "the object is encrypted with a customer-provided key (SSE-C) and cannot be rewritten without that key")
	}
	class := string(h.StorageClass)
	if class == "" {
		class = string(types.StorageClassStandard)
	}
	if (class == "GLACIER" || class == "DEEP_ARCHIVE") && !bkRestoreDone(aws.ToString(h.Restore)) {
		return bkFailf(res, "the object is archived (%s); restore it first", class)
	}
	if h.ServerSideEncryption == types.ServerSideEncryptionAwsKms || h.ServerSideEncryption == types.ServerSideEncryptionAwsKmsDsse {
		if reason := s.kmsReadOnlyReason(ctx, c, loc.Bucket, o.Key, "", size); reason != "" {
			return bkFailf(res, "%s", reason)
		}
	}
	in := bkSelfCopy(loc.Bucket, o.Key, h, class)
	skip, err := rw.mutate(in, h, class)
	if err != nil {
		return bkFailf(res, "%v", err)
	}
	if skip != "" {
		progress(size)
		res.Status, res.Err = ItemSkipped, skip
		return res
	}
	// The self-copy resets the ACL. Only "ACLs are unavailable here" may skip
	// restoring it; a denied or failed read must stop the item before the copy.
	acl, haveACL, err := bkReadACL(ctx, c, loc.Bucket, o.Key)
	if err != nil {
		if ctx.Err() != nil {
			return bkFail(ctx, res, err)
		}
		return bkFailf(res, "cannot read ACL; rewrite would reset it: %v", classify("", err))
	}
	if err := c.Copy(ctx, in, size, s.bkTransferOpts(progress)); err != nil {
		return bkFail(ctx, res, err)
	}
	progress(size)
	res.Status = ItemSucceeded
	if haveACL {
		if err := bkWriteACL(ctx, c, loc.Bucket, o.Key, acl); err != nil {
			res.Err = "rewritten, but the ACL could not be restored: " + classify("", err).Error()
		}
	}
	if rw.verify != nil {
		hin := &s3.HeadObjectInput{Bucket: aws.String(loc.Bucket), Key: aws.String(o.Key)}
		if rw.headOpts != nil {
			rw.headOpts(hin)
		}
		after, err := c.S3().HeadObject(ctx, hin)
		switch {
		case err != nil:
			res.Status, res.Err = ItemUnknown, "rewritten, but the result could not be checked: "+classify("Head", err).Error()
		case rw.verify(after) != "":
			res.Status, res.Err = ItemFailed, bkNotApplied+rw.verify(after)
		}
	}
	return res
}

// bkACLUnsupported reports errors that mean "ACLs are not available here".
func bkACLUnsupported(err error) bool {
	switch storage.ErrorCode(err) {
	case "NotImplemented", "AccessControlListNotSupported", "InvalidBucketAclWithObjectOwnership", "MethodNotAllowed", "UnsupportedOperation":
		return true
	}
	return bkStatus(err) == 501
}

// bkReadACL fetches an object's ACL; ok is false when there is nothing to
// restore later (ACLs unsupported or disabled, or only the owner's own full
// control). Any other failure (AccessDenied, ...) is returned: the ACL exists
// but is unknown, so it cannot be preserved.
func bkReadACL(ctx context.Context, c *storage.Client, bucket, key string) (*s3.GetObjectAclOutput, bool, error) {
	out, err := c.S3().GetObjectAcl(ctx, &s3.GetObjectAclInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		if bkACLUnsupported(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if out.Owner == nil {
		return nil, false, nil
	}
	for _, g := range out.Grants {
		own := g.Grantee != nil && g.Grantee.ID != nil && aws.ToString(g.Grantee.ID) == aws.ToString(out.Owner.ID)
		if !(own && g.Permission == types.PermissionFullControl) {
			return out, true, nil
		}
	}
	return nil, false, nil
}

func bkWriteACL(ctx context.Context, c *storage.Client, bucket, key string, acl *s3.GetObjectAclOutput) error {
	_, err := c.S3().PutObjectAcl(ctx, &s3.PutObjectAclInput{
		Bucket: aws.String(bucket), Key: aws.String(key),
		AccessControlPolicy: &types.AccessControlPolicy{Owner: acl.Owner, Grants: acl.Grants},
	})
	if err != nil && bkACLUnsupported(err) {
		return nil
	}
	return err
}

// ---- SetStorageClass ----------------------------------------------------------------------------

var bkClassRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,31}$`)

// SetStorageClass changes the storage class of the selected current objects
// by rewriting them in place (see the top of rewrite.go). Archived objects
// (GLACIER, DEEP_ARCHIVE without a finished restore), SSE-C objects and
// objects whose KMS key is not usable fail individually.
func (s *Service) SetStorageClass(sel Selection, class string) (string, error) {
	const op = "SetStorageClass"
	class = strings.ToUpper(strings.TrimSpace(class))
	if !bkClassRe.MatchString(class) {
		return "", invalid(op, "%q is not a storage class name", class)
	}
	scope, err := bkBuildScope(op, sel, false, false)
	if err != nil {
		return "", err
	}
	rw := &bkRewrite{
		op: op, title: bkTitle("Set storage class "+class+" on", sel),
		mutate: func(in *s3.CopyObjectInput, h *s3.HeadObjectOutput, cur string) (string, error) {
			if cur == class {
				return "already " + class, nil
			}
			in.StorageClass = types.StorageClass(class)
			return "", nil
		},
		verify: func(h *s3.HeadObjectOutput) string {
			got := string(h.StorageClass)
			if got == "" {
				got = string(types.StorageClassStandard)
			}
			if got != class {
				return "storage class is still " + got
			}
			return ""
		},
	}
	return s.bkSubmitRewrite(sel.Location, scope, rw), nil
}

// ---- SetEncryption --------------------------------------------------------------------------------

// SetEncryption re-encrypts the selected current objects by rewriting them in
// place. Mode "none" removes the explicit encryption request: a bucket default
// encryption still applies. SSE-C needs an HTTPS endpoint and a secret
// reference holding the base64 encoded 32-byte key; objects encrypted that way
// can only be read with that key afterwards.
func (s *Service) SetEncryption(sel Selection, e Encryption) (string, error) {
	const op = "SetEncryption"
	switch e.Mode {
	case "none", "AES256":
		if e.KMSKeyID != "" || e.KeyRef != "" {
			return "", invalid(op, "a KMS key or key reference does not go with mode %q", e.Mode)
		}
	case "aws:kms":
		if e.KeyRef != "" {
			return "", invalid(op, "a key reference does not go with aws:kms")
		}
	case "SSE-C":
		if e.KeyRef == "" {
			return "", invalid(op, "SSE-C needs a key reference")
		}
		if e.KMSKeyID != "" {
			return "", invalid(op, "SSE-C cannot use a KMS key")
		}
		rec, ok := s.account(sel.AccountID)
		if !ok {
			return "", opErr(op, ErrNotFound, "no account %q", sel.AccountID)
		}
		if err := bkRequireHTTPS(rec.Endpoint); err != nil {
			return "", invalid(op, "%v", err)
		}
	default:
		return "", invalid(op, "unknown encryption mode %q", e.Mode)
	}
	scope, err := bkBuildScope(op, sel, false, false)
	if err != nil {
		return "", err
	}
	var keyB64, keyMD5 string
	rw := &bkRewrite{
		op: op, title: bkTitle("Set encryption "+e.Mode+" on", sel),
		prep: func(ctx context.Context) error {
			if e.Mode != "SSE-C" {
				return nil
			}
			v, err := s.secretValue(ctx, e.KeyRef)
			if err != nil {
				return err
			}
			raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(v))
			if err != nil || len(raw) != 32 {
				return invalid(op, "secret %q must hold a base64 encoded 32-byte key", e.KeyRef)
			}
			sum := md5.Sum(raw)
			keyB64, keyMD5 = base64.StdEncoding.EncodeToString(raw), base64.StdEncoding.EncodeToString(sum[:])
			return nil
		},
		mutate: func(in *s3.CopyObjectInput, h *s3.HeadObjectOutput, _ string) (string, error) {
			cur := h.ServerSideEncryption
			switch e.Mode {
			case "none":
				if cur == "" {
					return "not encrypted", nil
				}
				in.ServerSideEncryption, in.SSEKMSKeyId, in.BucketKeyEnabled = "", nil, nil
			case "AES256":
				if cur == types.ServerSideEncryptionAes256 {
					return "already AES256", nil
				}
				in.ServerSideEncryption, in.SSEKMSKeyId, in.BucketKeyEnabled = types.ServerSideEncryptionAes256, nil, nil
			case "aws:kms":
				if cur == types.ServerSideEncryptionAwsKms && (e.KMSKeyID == "" || e.KMSKeyID == aws.ToString(h.SSEKMSKeyId)) {
					return "already aws:kms", nil
				}
				in.ServerSideEncryption = types.ServerSideEncryptionAwsKms
				in.SSEKMSKeyId = nil
				if e.KMSKeyID != "" {
					in.SSEKMSKeyId = aws.String(e.KMSKeyID)
				}
			case "SSE-C":
				in.ServerSideEncryption, in.SSEKMSKeyId, in.BucketKeyEnabled = "", nil, nil
				in.SSECustomerAlgorithm = aws.String("AES256")
				in.SSECustomerKey, in.SSECustomerKeyMD5 = aws.String(keyB64), aws.String(keyMD5)
			}
			return "", nil
		},
		// "none" is not checked: a bucket default encryption may still apply.
		verify: func(h *s3.HeadObjectOutput) string {
			switch e.Mode {
			case "AES256", "aws:kms":
				if string(h.ServerSideEncryption) != e.Mode {
					return "encryption is still " + bkSSEName(string(h.ServerSideEncryption))
				}
			case "SSE-C":
				if aws.ToString(h.SSECustomerAlgorithm) != "AES256" {
					return "the object is not encrypted with the customer key"
				}
			}
			return ""
		},
		headOpts: func(in *s3.HeadObjectInput) {
			if e.Mode == "SSE-C" {
				in.SSECustomerAlgorithm = aws.String("AES256")
				in.SSECustomerKey, in.SSECustomerKeyMD5 = aws.String(keyB64), aws.String(keyMD5)
			}
		},
	}
	if e.Mode == "none" {
		rw.verify = nil
	}
	return s.bkSubmitRewrite(sel.Location, scope, rw), nil
}

func bkSSEName(v string) string {
	if v == "" {
		return "none"
	}
	return v
}

func bkRequireHTTPS(endpoint string) error {
	if endpoint == "" {
		return nil // AWS endpoints are HTTPS
	}
	u, err := url.Parse(endpoint)
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Host == "" {
		return errors.New("SSE-C keys are only sent over HTTPS; this account's endpoint is not HTTPS")
	}
	return nil
}

// ---- SetProperties --------------------------------------------------------------------------------

// bkMaxMetadata is the S3 limit for user-defined metadata (names plus values).
const bkMaxMetadata = 2048

// bkPatch is a validated PropertiesPatch with canonical header names.
type bkPatch struct {
	setH    map[string]string
	delH    map[string]bool
	setMeta map[string]string
	delMeta map[string]bool
}

func bkIsToken(v string) bool {
	if v == "" {
		return false
	}
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', strings.ContainsRune("!#$%&'*+-.^_`|~", r):
		default:
			return false
		}
	}
	return true
}

func bkASCIIText(v string) bool {
	for i := 0; i < len(v); i++ {
		if v[i] < 0x20 || v[i] > 0x7e {
			return false
		}
	}
	return true
}

var bkEditableHeaders = map[string]bool{"Content-Type": true, "Content-Encoding": true, "Content-Language": true,
	"Content-Disposition": true, "Cache-Control": true, "Expires": true}

func bkValidatePatch(op string, p PropertiesPatch) (*bkPatch, error) {
	out := &bkPatch{setH: map[string]string{}, delH: map[string]bool{}, setMeta: map[string]string{}, delMeta: map[string]bool{}}
	header := func(name string) (string, error) {
		canon := http.CanonicalHeaderKey(strings.TrimSpace(name))
		if !bkEditableHeaders[canon] {
			return "", invalid(op, "header %q cannot be edited (only Content-Type, Content-Encoding, Content-Language, Content-Disposition, Cache-Control, Expires)", name)
		}
		return canon, nil
	}
	for name, v := range p.SetHeaders {
		canon, err := header(name)
		if err != nil {
			return nil, err
		}
		if _, dup := out.setH[canon]; dup {
			return nil, invalid(op, "header %s is given twice", canon)
		}
		if v == "" || strings.TrimSpace(v) != v {
			return nil, invalid(op, "header %s needs a value without surrounding spaces; delete it instead to clear it", canon)
		}
		if !bkASCIIText(v) {
			return nil, invalid(op, "header %s must be ASCII without control characters", canon)
		}
		switch canon {
		case "Expires":
			t, err := http.ParseTime(v)
			if err != nil {
				return nil, invalid(op, "Expires must be an HTTP date such as Mon, 02 Jan 2006 15:04:05 GMT")
			}
			v = t.UTC().Format(http.TimeFormat)
		case "Content-Type":
			if _, _, err := mime.ParseMediaType(v); err != nil {
				return nil, invalid(op, "Content-Type %q is not valid: %v", v, err)
			}
		}
		out.setH[canon] = v
	}
	for _, name := range p.DelHeaders {
		canon, err := header(name)
		if err != nil {
			return nil, err
		}
		if _, set := out.setH[canon]; set {
			return nil, invalid(op, "header %s is both set and deleted", canon)
		}
		out.delH[canon] = true
	}
	metaKey := func(k string) error {
		switch {
		case !bkIsToken(k):
			return invalid(op, "metadata key %q has characters that are not allowed in a header name", k)
		case k != strings.ToLower(k):
			return invalid(op, "metadata key %q must be lower case", k)
		case strings.HasPrefix(k, "x-amz-"):
			return invalid(op, "metadata key %q is reserved; enter the name without x-amz-meta-", k)
		case isInternalMeta(k):
			return invalid(op, "metadata keys starting with %s or %s are managed by Pail and cannot be edited", internalMetaPrefix, legacyMetaPrefix)
		}
		return nil
	}
	total := 0
	for k, v := range p.SetMetadata {
		if err := metaKey(k); err != nil {
			return nil, err
		}
		if !bkASCIIText(v) || strings.TrimSpace(v) != v {
			return nil, invalid(op, "metadata %q must be ASCII without control characters or surrounding spaces", k)
		}
		out.setMeta[k] = v
		total += len(k) + len(v)
	}
	for _, k := range p.DelMetadata {
		if err := metaKey(k); err != nil {
			return nil, err
		}
		if _, set := out.setMeta[k]; set {
			return nil, invalid(op, "metadata %q is both set and deleted", k)
		}
		out.delMeta[k] = true
	}
	if total > bkMaxMetadata {
		return nil, invalid(op, "user metadata exceeds the %d byte limit", bkMaxMetadata)
	}
	if len(out.setH)+len(out.delH)+len(out.setMeta)+len(out.delMeta) == 0 {
		return nil, invalid(op, "the patch changes nothing")
	}
	return out, nil
}

// SetProperties applies p to the selected current objects by rewriting them
// in place. Metadata keys are lower case without x-amz-meta-; names starting
// with pail- (and the legacy opens3-) are reserved for Pail's own bookkeeping. The 2 KiB limit on
// user metadata is checked again per object against the merged result.
func (s *Service) SetProperties(sel Selection, p PropertiesPatch) (string, error) {
	const op = "SetProperties"
	patch, err := bkValidatePatch(op, p)
	if err != nil {
		return "", err
	}
	scope, err := bkBuildScope(op, sel, false, false)
	if err != nil {
		return "", err
	}
	rw := &bkRewrite{
		op: op, title: bkTitle("Edit properties of", sel),
		mutate: func(in *s3.CopyObjectInput, h *s3.HeadObjectOutput, _ string) (string, error) {
			return patch.apply(in)
		},
	}
	return s.bkSubmitRewrite(sel.Location, scope, rw), nil
}

// apply edits in; the returned string is a skip reason when nothing changes.
func (p *bkPatch) apply(in *s3.CopyObjectInput) (string, error) {
	changed := false
	str := func(dst **string, name string) {
		if v, ok := p.setH[name]; ok {
			if aws.ToString(*dst) != v {
				changed = true
			}
			*dst = aws.String(v)
		} else if p.delH[name] {
			if *dst != nil && **dst != "" {
				changed = true
			}
			*dst = nil
		}
	}
	str(&in.ContentType, "Content-Type")
	str(&in.ContentEncoding, "Content-Encoding")
	str(&in.ContentLanguage, "Content-Language")
	str(&in.ContentDisposition, "Content-Disposition")
	str(&in.CacheControl, "Cache-Control")
	if v, ok := p.setH["Expires"]; ok {
		t, _ := http.ParseTime(v)
		if in.Expires == nil || !in.Expires.Equal(t) {
			changed = true
		}
		in.Expires = aws.Time(t)
	} else if p.delH["Expires"] {
		if in.Expires != nil {
			changed = true
		}
		in.Expires = nil
	}
	if in.Metadata == nil {
		in.Metadata = map[string]string{}
	}
	for k, v := range p.setMeta {
		if old, ok := in.Metadata[k]; !ok || old != v {
			changed = true
		}
		in.Metadata[k] = v
	}
	for k := range p.delMeta {
		if _, ok := in.Metadata[k]; ok {
			changed = true
			delete(in.Metadata, k)
		}
	}
	if !changed {
		return "properties already have these values", nil
	}
	total := 0
	for k, v := range in.Metadata {
		total += len(k) + len(v)
	}
	if total > bkMaxMetadata {
		return "", fmt.Errorf("user metadata would be %d bytes, above the %d byte limit", total, bkMaxMetadata)
	}
	return "", nil
}
