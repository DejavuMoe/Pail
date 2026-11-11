package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/DejavuMoe/Pail/internal/storage"
)

// bkStreamCopy copies one object between two accounts: a GET from the source
// guarded by the listed ETag (and version), streamed into an upload to the
// destination with the source's content headers, metadata, storage class and
// tags. The body must have exactly the listed size; anything else (a proxy
// that decompressed it, a truncated response) fails the item and the upload
// is aborted, so a partial object is never committed.
func (s *Service) bkStreamCopy(ctx context.Context, src, dst *storage.Client, p *bkCopyPlan, o bkObj, head *s3.HeadObjectOutput,
	destKey, ifNoneMatch, ifMatch string, progress func(int64)) error {
	if head == nil {
		return errors.New("the source object was not read")
	}
	if aws.ToString(head.SSECustomerAlgorithm) != "" {
		return errors.New("objects encrypted with a customer key (SSE-C) cannot be copied between accounts")
	}
	tags, err := bkCopyTags(ctx, src, p.src.Bucket, o.Key, o.VersionID)
	if err != nil {
		return err
	}
	in := &s3.GetObjectInput{Bucket: aws.String(p.src.Bucket), Key: aws.String(o.Key), IfMatch: aws.String(o.ETag)}
	if o.VersionID != "" {
		in.VersionId = aws.String(o.VersionID)
	}
	out, err := src.S3().GetObject(ctx, in)
	if err != nil {
		return err
	}
	defer out.Body.Close()
	if n := aws.ToInt64(out.ContentLength); out.ContentLength != nil && n != o.Size {
		return fmt.Errorf("the source returned %d bytes, the object has %d", n, o.Size)
	}
	put := &s3.PutObjectInput{
		Bucket: aws.String(p.dest.Bucket), Key: aws.String(destKey),
		Body:               &exactReader{r: out.Body, left: o.Size},
		ContentLength:      aws.Int64(o.Size),
		ContentType:        head.ContentType,
		ContentEncoding:    head.ContentEncoding,
		ContentDisposition: head.ContentDisposition,
		ContentLanguage:    head.ContentLanguage,
		CacheControl:       head.CacheControl,
		Expires:            head.Expires,
		Metadata:           head.Metadata,
		StorageClass:       bkCopyClass(o.StorageClass),
	}
	if head.WebsiteRedirectLocation != nil {
		put.WebsiteRedirectLocation = head.WebsiteRedirectLocation
	}
	if len(tags) > 0 {
		put.Tagging = aws.String(encodeTags(tags))
	}
	if ifNoneMatch != "" {
		put.IfNoneMatch = aws.String(ifNoneMatch)
	}
	if ifMatch != "" {
		put.IfMatch = aws.String(ifMatch)
	}
	_, err = dst.Upload(ctx, put, s.bkTransferOpts(progress))
	return err
}

// encodeTags is the x-amz-tagging form of tags, in key order.
func encodeTags(tags map[string]string) string {
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	v := url.Values{}
	for _, k := range keys {
		v.Set(k, tags[k])
	}
	return v.Encode()
}

// exactReader passes exactly left bytes from r: a shorter body is an
// io.ErrUnexpectedEOF, a longer one an error, never a silently different
// object.
type exactReader struct {
	r    io.Reader
	left int64
}

func (e *exactReader) Read(p []byte) (int, error) {
	if e.left <= 0 {
		var one [1]byte
		if n, _ := e.r.Read(one[:]); n > 0 {
			return 0, errors.New("the source sent more bytes than the object has")
		}
		return 0, io.EOF
	}
	if int64(len(p)) > e.left {
		p = p[:e.left]
	}
	n, err := e.r.Read(p)
	e.left -= int64(n)
	if err == io.EOF {
		if e.left > 0 {
			return n, io.ErrUnexpectedEOF
		}
		if n > 0 {
			return n, nil
		}
	}
	return n, err
}
