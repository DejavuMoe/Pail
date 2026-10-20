package app

import (
	"context"
	"io"
	"net"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/DejavuMoe/Pail/internal/storage"
)

const (
	defaultMaxKeys = 1000
	// maxPresignTTL is the S3 limit for SigV4 presigned URLs.
	maxPresignTTL = 7 * 24 * time.Hour
)

// checkLocation validates a Location; requireBucket is false for account-level calls.
func checkLocation(op string, loc Location, requireBucket bool) error {
	if loc.AccountID == "" {
		return invalid(op, "account is required")
	}
	if requireBucket && loc.Bucket == "" {
		return invalid(op, "bucket is required")
	}
	if loc.Prefix != "" && !strings.HasSuffix(loc.Prefix, "/") {
		return invalid(op, "prefix %q must be empty or end with \"/\"", loc.Prefix)
	}
	return nil
}

// bucketClient validates loc and returns the account's S3 client.
func (s *Service) bucketClient(ctx context.Context, op string, loc Location) (*storage.Client, error) {
	if err := checkLocation(op, loc, true); err != nil {
		return nil, err
	}
	c, err := s.storageClient(ctx, loc.AccountID)
	if err != nil {
		return nil, classify(op, err)
	}
	return c, nil
}

// Buckets lists the account's buckets with their regions where the provider
// reports them. Regions are remembered for PublicURL.
func (s *Service) Buckets(ctx context.Context, accountID string) ([]Bucket, error) {
	const op = "Buckets"
	if err := checkLocation(op, Location{AccountID: accountID}, false); err != nil {
		return nil, err
	}
	c, err := s.storageClient(ctx, accountID)
	if err != nil {
		return nil, classify(op, err)
	}
	bs, err := c.ListBuckets(ctx)
	if err != nil {
		return nil, classify(op, err)
	}
	out := make([]Bucket, len(bs))
	s.mu.Lock()
	for i, b := range bs {
		out[i] = Bucket{Name: b.Name, Region: b.Region, Created: b.CreationDate}
		if b.Region != "" {
			s.bucketRegion[accountID+"/"+b.Name] = b.Region
		}
	}
	s.mu.Unlock()
	return out, nil
}

// List returns one page of loc. Without Flat the listing uses the "/" delimiter:
// sub-folders come back as Folder entries (Key ends with "/", Name without it).
// The placeholder object whose key equals the prefix itself is omitted. In flat
// mode zero-byte "folder/" placeholder objects are reported as Folder entries
// so that nothing in the bucket is hidden. Order is the service's (UTF-8
// binary); sorting is the UI's job.
func (s *Service) List(ctx context.Context, loc Location, opt ListOptions) (Page, error) {
	const op = "List"
	c, err := s.bucketClient(ctx, op, loc)
	if err != nil {
		return Page{}, err
	}
	max := opt.MaxKeys
	if max <= 0 {
		max = defaultMaxKeys
	}
	in := &s3.ListObjectsV2Input{
		Bucket:       aws.String(loc.Bucket),
		MaxKeys:      aws.Int32(max),
		EncodingType: types.EncodingTypeUrl, // keys may contain characters XML cannot carry
	}
	if loc.Prefix != "" {
		in.Prefix = aws.String(loc.Prefix)
	}
	if !opt.Flat {
		in.Delimiter = aws.String("/")
	}
	if opt.Token != "" {
		in.ContinuationToken = aws.String(opt.Token)
	}
	out, err := c.S3().ListObjectsV2(ctx, in)
	if err != nil {
		return Page{}, classify(op, err)
	}
	// Decode only when the service says it encoded; some S3-compatible
	// servers ignore EncodingType and return plain keys.
	decode := func(v string) string { return v }
	if out.EncodingType == types.EncodingTypeUrl {
		decode = func(v string) string {
			if d, err := url.QueryUnescape(v); err == nil {
				return d
			}
			return v
		}
	}
	page := Page{Entries: make([]Entry, 0, len(out.CommonPrefixes)+len(out.Contents))}
	for _, cp := range out.CommonPrefixes {
		key := decode(aws.ToString(cp.Prefix))
		page.Entries = append(page.Entries, Entry{Key: key, Name: entryName(key), Folder: true})
	}
	for _, o := range out.Contents {
		key := decode(aws.ToString(o.Key))
		if key == loc.Prefix {
			continue // the folder's own placeholder object
		}
		e := Entry{
			Key: key, Name: entryName(key), Folder: strings.HasSuffix(key, "/"),
			Size: aws.ToInt64(o.Size), Modified: aws.ToTime(o.LastModified), ETag: aws.ToString(o.ETag),
			StorageClass: string(o.StorageClass),
		}
		if e.StorageClass == "" {
			e.StorageClass = string(types.ObjectStorageClassStandard)
		}
		page.Entries = append(page.Entries, e)
	}
	if aws.ToBool(out.IsTruncated) {
		page.NextToken = aws.ToString(out.NextContinuationToken)
	}
	return page, nil
}

// entryName is the last path segment of key, without a trailing "/".
func entryName(key string) string {
	return path.Base(strings.TrimSuffix(key, "/"))
}

// headerNames are the editable standard headers in the order Head reports them.
var headerNames = [...]string{"Content-Type", "Content-Encoding", "Content-Language", "Content-Disposition", "Cache-Control", "Expires"}

// Head returns the properties of one object (or version). Tags are fetched
// best effort: when unsupported or denied, Tags is nil and TagsErr says why.
// ReadOnlyReason is set for SSE-C objects and for KMS-encrypted objects whose
// decryption cannot be verified (a one-byte ranged read is attempted), because
// editing properties means rewriting the object with CopyObject.
func (s *Service) Head(ctx context.Context, loc Location, key, versionID string) (ObjectInfo, error) {
	const op = "Head"
	if key == "" {
		return ObjectInfo{}, invalid(op, "key is required")
	}
	c, err := s.bucketClient(ctx, op, loc)
	if err != nil {
		return ObjectInfo{}, err
	}
	in := &s3.HeadObjectInput{Bucket: aws.String(loc.Bucket), Key: aws.String(key)}
	if versionID != "" {
		in.VersionId = aws.String(versionID)
	}
	h, err := c.S3().HeadObject(ctx, in)
	if err != nil {
		return ObjectInfo{}, classify(op, err)
	}
	info := ObjectInfo{
		Key: key, VersionID: aws.ToString(h.VersionId), ETag: aws.ToString(h.ETag),
		Size: aws.ToInt64(h.ContentLength), Modified: aws.ToTime(h.LastModified),
		StorageClass: string(h.StorageClass), ContentType: aws.ToString(h.ContentType),
		SSE: string(h.ServerSideEncryption), KMSKeyID: aws.ToString(h.SSEKMSKeyId),
		Restore: aws.ToString(h.Restore), LockMode: string(h.ObjectLockMode),
		RetainUntil: aws.ToTime(h.ObjectLockRetainUntilDate), LegalHold: h.ObjectLockLegalHoldStatus == types.ObjectLockLegalHoldStatusOn,
		Metadata: make(map[string]string, len(h.Metadata)),
	}
	if info.VersionID == "" {
		info.VersionID = versionID
	}
	if info.StorageClass == "" {
		info.StorageClass = string(types.StorageClassStandard)
	}
	for k, v := range h.Metadata {
		info.Metadata[strings.ToLower(k)] = v
	}
	expires := aws.ToString(h.ExpiresString)
	if expires == "" && h.Expires != nil {
		expires = h.Expires.UTC().Format(time.RFC1123)
	}
	info.Headers = []Header{
		{headerNames[0], aws.ToString(h.ContentType)},
		{headerNames[1], aws.ToString(h.ContentEncoding)},
		{headerNames[2], aws.ToString(h.ContentLanguage)},
		{headerNames[3], aws.ToString(h.ContentDisposition)},
		{headerNames[4], aws.ToString(h.CacheControl)},
		{headerNames[5], expires},
	}

	// Tags and the KMS probe are independent: run them concurrently.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		tin := &s3.GetObjectTaggingInput{Bucket: aws.String(loc.Bucket), Key: aws.String(key)}
		if versionID != "" {
			tin.VersionId = aws.String(versionID)
		}
		out, err := c.S3().GetObjectTagging(ctx, tin)
		if err != nil {
			info.TagsErr = classify("Tags", err)
			return
		}
		info.Tags = make(map[string]string, len(out.TagSet))
		for _, t := range out.TagSet {
			info.Tags[aws.ToString(t.Key)] = aws.ToString(t.Value)
		}
	}()
	switch {
	case h.SSECustomerAlgorithm != nil && *h.SSECustomerAlgorithm != "":
		info.ReadOnlyReason = "the object is encrypted with a customer-provided key (SSE-C); its properties cannot be edited without that key"
	case h.ServerSideEncryption == types.ServerSideEncryptionAwsKms || h.ServerSideEncryption == types.ServerSideEncryptionAwsKmsDsse:
		info.ReadOnlyReason = s.kmsReadOnlyReason(ctx, c, loc.Bucket, key, versionID, info.Size)
	}
	wg.Wait()
	return info, nil
}

// kmsReadOnlyReason checks that the caller can decrypt a KMS object by reading
// one byte. It returns "" when decryption works.
func (s *Service) kmsReadOnlyReason(ctx context.Context, c *storage.Client, bucket, key, versionID string, size int64) string {
	if size == 0 {
		return "" // nothing to decrypt, and a Range request on an empty object is rejected
	}
	in := &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), Range: aws.String("bytes=0-0")}
	if versionID != "" {
		in.VersionId = aws.String(versionID)
	}
	out, err := c.S3().GetObject(ctx, in)
	if err != nil {
		return "the object is KMS-encrypted and decryption could not be verified: " + classify("Head", err).Error()
	}
	io.Copy(io.Discard, io.LimitReader(out.Body, 1))
	out.Body.Close()
	return ""
}

// Preview reads up to maxBytes from the start of an object. truncated reports
// that the object is larger. It uses a ranged GET and also copes with servers
// that ignore Range.
func (s *Service) Preview(ctx context.Context, loc Location, key, versionID string, maxBytes int64) (data []byte, truncated bool, err error) {
	const op = "Preview"
	if key == "" {
		return nil, false, invalid(op, "key is required")
	}
	if maxBytes <= 0 {
		return nil, false, invalid(op, "maxBytes must be positive")
	}
	c, err := s.bucketClient(ctx, op, loc)
	if err != nil {
		return nil, false, err
	}
	in := &s3.GetObjectInput{Bucket: aws.String(loc.Bucket), Key: aws.String(key), Range: aws.String("bytes=0-" + strconv.FormatInt(maxBytes-1, 10))}
	if versionID != "" {
		in.VersionId = aws.String(versionID)
	}
	out, err := c.S3().GetObject(ctx, in)
	if err != nil {
		if storage.ErrorCode(err) == "InvalidRange" {
			return []byte{}, false, nil // empty object
		}
		return nil, false, classify(op, err)
	}
	defer out.Body.Close()
	data, err = io.ReadAll(io.LimitReader(out.Body, maxBytes+1))
	if err != nil {
		return nil, false, classify(op, err)
	}
	if int64(len(data)) > maxBytes {
		return data[:maxBytes], true, nil
	}
	if total, ok := contentRangeTotal(aws.ToString(out.ContentRange)); ok && total > int64(len(data)) {
		truncated = true
	}
	return data, truncated, nil
}

// contentRangeTotal parses the total of "bytes 0-99/1234".
func contentRangeTotal(v string) (int64, bool) {
	i := strings.LastIndexByte(v, '/')
	if i < 0 {
		return 0, false
	}
	n, err := strconv.ParseInt(v[i+1:], 10, 64)
	return n, err == nil
}

// PresignURL returns a signed URL valid for ttl (at most 7 days). method is
// GET, HEAD or PUT (PUT takes no versionID).
func (s *Service) PresignURL(ctx context.Context, loc Location, key, versionID, method string, ttl time.Duration) (string, error) {
	const op = "PresignURL"
	if key == "" {
		return "", invalid(op, "key is required")
	}
	if ttl <= 0 || ttl > maxPresignTTL {
		return "", invalid(op, "expiry must be between 1 second and 7 days")
	}
	method = strings.ToUpper(method)
	if method == "" {
		method = "GET"
	}
	switch method {
	case "GET", "HEAD", "PUT":
	default:
		return "", invalid(op, "method must be GET, HEAD or PUT")
	}
	if method == "PUT" && versionID != "" {
		return "", invalid(op, "a PUT URL cannot target a version")
	}
	c, err := s.bucketClient(ctx, op, loc)
	if err != nil {
		return "", err
	}
	p, err := c.Presign(ctx, method, loc.Bucket, key, versionID, ttl)
	if err != nil {
		return "", classify(op, err)
	}
	return p.URL, nil
}

// PublicURL returns the URL under which the object is reachable if it is
// public. No request is made and no permissions are checked.
//
//   - AWS: https://<bucket>.s3.<region>.amazonaws.com/<key>; buckets whose name
//     contains a dot would fail TLS validation, so they use path-style.
//   - Custom endpoint: path-style when the account says so (or the host is an
//     IP address / localhost), otherwise <scheme>://<bucket>.<host>/<key>.
//
// The region is the one learnt by Buckets, else the account's, else us-east-1.
// It returns "" for an unknown account.
func (s *Service) PublicURL(loc Location, key string) string {
	s.mu.Lock()
	rec, ok := s.accountLocked(loc.AccountID)
	region := s.bucketRegion[loc.AccountID+"/"+loc.Bucket]
	s.mu.Unlock()
	if !ok || loc.Bucket == "" {
		return ""
	}
	if region == "" {
		region = rec.Region
	}
	if region == "" {
		region = "us-east-1"
	}
	esc := escapeKey(key)
	if rec.Endpoint == "" { // AWS
		domain := "amazonaws.com"
		if strings.HasPrefix(region, "cn-") {
			domain = "amazonaws.com.cn"
		}
		host := "s3." + region + "." + domain
		if strings.Contains(loc.Bucket, ".") {
			return "https://" + host + "/" + loc.Bucket + "/" + esc
		}
		return "https://" + loc.Bucket + "." + host + "/" + esc
	}
	u, err := url.Parse(rec.Endpoint)
	if err != nil {
		return ""
	}
	base := strings.TrimRight(u.Path, "/")
	hostname := u.Hostname()
	if rec.PathStyle || net.ParseIP(hostname) != nil || hostname == "localhost" {
		return u.Scheme + "://" + u.Host + base + "/" + loc.Bucket + "/" + esc
	}
	host := loc.Bucket + "." + hostname
	if p := u.Port(); p != "" {
		host += ":" + p
	}
	return u.Scheme + "://" + host + base + "/" + esc
}

// escapeKey percent-encodes everything except unreserved characters and "/".
func escapeKey(key string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(key); i++ {
		c := key[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '.', c == '_', c == '~', c == '/':
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
	}
	return b.String()
}

// CreateFolder creates the zero-byte placeholder object "<prefix><name>/".
// name may carry one trailing "/" but no "/" inside, and may not be "." or "..".
func (s *Service) CreateFolder(ctx context.Context, loc Location, name string) error {
	const op = "CreateFolder"
	name = strings.TrimSuffix(name, "/")
	switch {
	case name == "" || name == "." || name == "..":
		return invalid(op, "folder name %q is not valid", name)
	case strings.Contains(name, "/"):
		return invalid(op, "folder name must not contain \"/\"; create folders one level at a time")
	case strings.ContainsAny(name, "\x00\r\n"):
		return invalid(op, "folder name contains control characters")
	}
	c, err := s.bucketClient(ctx, op, loc)
	if err != nil {
		return err
	}
	if _, err := c.UploadBytes(ctx, loc.Bucket, loc.Prefix+name+"/", "application/x-directory", nil); err != nil {
		return classify(op, err)
	}
	return nil
}
