package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Copy copies one object server-side. CopyObject is used up to 5 GiB; larger
// objects (and any copy with destination preconditions, which only
// CompleteMultipartUpload enforces reliably) use multipart UploadPartCopy pinned
// to the source ETag and version. size is the source size from a fresh listing.
// Metadata follows the COPY/REPLACE directive. Tags are copied in the multipart
// path; only a service that does not implement tagging (NotImplemented /
// unsupported) skips them. Any other failure to read them, AccessDenied
// included, fails the copy before an upload is created.
func (c *Client) Copy(ctx context.Context, in *s3.CopyObjectInput, size int64, opts TransferOptions) error {
	if in == nil || in.CopySource == nil || in.Bucket == nil || in.Key == nil {
		return errors.New("copy requires source, destination bucket and key")
	}
	if size < 0 || size > 5*1024*1024*1024*1024 {
		return errors.New("copy size must be between 0 and 5 TiB")
	}
	if err := opts.validate(); err != nil {
		return err
	}
	if size <= 5*1024*1024*1024 && in.IfMatch == nil && in.IfNoneMatch == nil {
		_, err := c.sdk.CopyObject(ctx, in)
		if err == nil && opts.Progress != nil {
			opts.Progress(size)
		}
		return err
	}
	if in.MetadataDirective != "" && in.MetadataDirective != types.MetadataDirectiveCopy && in.MetadataDirective != types.MetadataDirectiveReplace {
		return errors.New("metadata directive must be COPY or REPLACE")
	}
	if in.TaggingDirective != "" && in.TaggingDirective != types.TaggingDirectiveCopy && in.TaggingDirective != types.TaggingDirectiveReplace {
		return errors.New("tagging directive must be COPY or REPLACE")
	}
	if in.AnnotationDirective != "" && in.AnnotationDirective != "EXCLUDE" {
		return errors.New("multipart copy cannot copy object annotations; use explicit EXCLUDE or a separate annotation operation")
	}
	return c.copyMultipart(ctx, in, size, opts)
}

func sourceLocation(source string) (bucket, key, version string, err error) {
	path, query, _ := strings.Cut(strings.TrimPrefix(source, "/"), "?")
	path, err = url.PathUnescape(path)
	if err != nil {
		return "", "", "", err
	}
	bucket, key, ok := strings.Cut(path, "/")
	if !ok || bucket == "" || key == "" || strings.HasPrefix(bucket, "arn:") {
		return "", "", "", errors.New("multipart copy requires a bucket/key copy source")
	}
	q, e := url.ParseQuery(query)
	if e != nil {
		return "", "", "", e
	}
	return bucket, key, q.Get("versionId"), nil
}
func (c *Client) copyMultipart(ctx context.Context, in *s3.CopyObjectInput, size int64, opts TransferOptions) (result error) {
	bucket, key, version, err := sourceLocation(aws.ToString(in.CopySource))
	if err != nil {
		return err
	}
	headInput := &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), IfMatch: in.CopySourceIfMatch, IfNoneMatch: in.CopySourceIfNoneMatch, IfModifiedSince: in.CopySourceIfModifiedSince, IfUnmodifiedSince: in.CopySourceIfUnmodifiedSince, SSECustomerAlgorithm: in.CopySourceSSECustomerAlgorithm, SSECustomerKey: in.CopySourceSSECustomerKey, SSECustomerKeyMD5: in.CopySourceSSECustomerKeyMD5, ExpectedBucketOwner: in.ExpectedSourceBucketOwner, RequestPayer: in.RequestPayer}
	if version != "" {
		headInput.VersionId = aws.String(version)
	}
	head, err := c.sdk.HeadObject(ctx, headInput)
	if err != nil {
		return fmt.Errorf("inspect copy source: %w", err)
	}
	if head.ContentLength == nil || *head.ContentLength != size {
		return errors.New("copy source size changed; refresh the object before copying")
	}
	if version == "" && aws.ToString(head.VersionId) != "" && aws.ToString(head.VersionId) != "null" {
		version = *head.VersionId
	}
	source := CopySource(bucket, key, version)
	match := head.ETag
	if match == nil {
		return errors.New("copy source did not return an ETag for consistency protection")
	}
	create := s3.CreateMultipartUploadInput{
		Bucket: in.Bucket, Key: in.Key, ACL: in.ACL, BucketKeyEnabled: in.BucketKeyEnabled,
		CacheControl: in.CacheControl, ChecksumAlgorithm: in.ChecksumAlgorithm,
		ContentDisposition: in.ContentDisposition, ContentEncoding: in.ContentEncoding,
		ContentLanguage: in.ContentLanguage, ContentType: in.ContentType,
		ExpectedBucketOwner: in.ExpectedBucketOwner, Expires: in.Expires,
		GrantFullControl: in.GrantFullControl, GrantRead: in.GrantRead,
		GrantReadACP: in.GrantReadACP, GrantWriteACP: in.GrantWriteACP, Metadata: in.Metadata,
		ObjectLockEventHold: in.ObjectLockEventHold, ObjectLockEventHoldDurationDays: in.ObjectLockEventHoldDurationDays,
		ObjectLockEventHoldDurationYears: in.ObjectLockEventHoldDurationYears,
		ObjectLockLegalHoldStatus:        in.ObjectLockLegalHoldStatus, ObjectLockMode: in.ObjectLockMode,
		ObjectLockRetainUntilDate: in.ObjectLockRetainUntilDate, RequestPayer: in.RequestPayer,
		SSECustomerAlgorithm: in.SSECustomerAlgorithm, SSECustomerKey: in.SSECustomerKey, SSECustomerKeyMD5: in.SSECustomerKeyMD5,
		SSEKMSEncryptionContext: in.SSEKMSEncryptionContext, SSEKMSKeyId: in.SSEKMSKeyId,
		ServerSideEncryption: in.ServerSideEncryption, StorageClass: in.StorageClass, Tagging: in.Tagging,
		WebsiteRedirectLocation: in.WebsiteRedirectLocation,
	}
	if in.MetadataDirective == "" || in.MetadataDirective == types.MetadataDirectiveCopy {
		create.Metadata = head.Metadata
		create.CacheControl = head.CacheControl
		create.ContentDisposition = head.ContentDisposition
		create.ContentEncoding = head.ContentEncoding
		create.ContentLanguage = head.ContentLanguage
		create.ContentType = head.ContentType
		create.Expires = head.Expires
	}
	var sourceTags *s3.GetObjectTaggingInput
	var tagString string
	if in.TaggingDirective == "" || in.TaggingDirective == types.TaggingDirectiveCopy {
		tagIn := &s3.GetObjectTaggingInput{Bucket: aws.String(bucket), Key: aws.String(key), ExpectedBucketOwner: in.ExpectedSourceBucketOwner, RequestPayer: in.RequestPayer}
		if version != "" {
			tagIn.VersionId = aws.String(version)
		}
		tags, e := c.sdk.GetObjectTagging(ctx, tagIn)
		switch {
		case e == nil:
			sourceTags = tagIn
			tagString = encodeTags(tags.TagSet)
			if tagString != "" {
				create.Tagging = aws.String(tagString)
			}
		case !tagsUnsupported(e):
			return newTagReadError("cannot read source tags", e)
		}
	}
	started, err := c.sdk.CreateMultipartUpload(ctx, &create)
	if err != nil {
		return err
	}
	uploadID := started.UploadId
	if aws.ToString(uploadID) == "" {
		return errors.New("service returned empty multipart upload ID")
	}
	finished := false
	defer func() {
		if finished {
			return
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_, e := c.sdk.AbortMultipartUpload(cleanup, &s3.AbortMultipartUploadInput{Bucket: in.Bucket, Key: in.Key, UploadId: uploadID, ExpectedBucketOwner: in.ExpectedBucketOwner, RequestPayer: in.RequestPayer})
		if e != nil {
			result = errors.Join(result, fmt.Errorf("abort multipart copy %s: %w", *uploadID, e))
		}
	}()
	partSize, err := copyPartSize(size, opts.PartSize)
	if err != nil {
		return err
	}
	count := int((size + partSize - 1) / partSize)
	if count == 0 {
		count = 1
	}
	parts := make([]types.CompletedPart, count)
	workers := opts.Concurrency
	if workers == 0 {
		workers = 4
	}
	if workers > count {
		workers = count
	}
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	var done int64
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				start := int64(i) * partSize
				end := start + partSize - 1
				if end >= size {
					end = size - 1
				}
				var completed types.CompletedPart
				var e error
				number := aws.Int32(int32(i + 1))
				if size == 0 {
					// HEAD already confirmed the empty source snapshot. An empty
					// uploaded part preserves zero-byte objects on services that
					// reject UploadPartCopy for an empty source.
					out, uploadErr := c.sdk.UploadPart(workCtx, &s3.UploadPartInput{Bucket: in.Bucket, Key: in.Key, UploadId: uploadID, PartNumber: number, Body: bytes.NewReader(nil), ContentLength: aws.Int64(0), ChecksumAlgorithm: create.ChecksumAlgorithm, SSECustomerAlgorithm: in.SSECustomerAlgorithm, SSECustomerKey: in.SSECustomerKey, SSECustomerKeyMD5: in.SSECustomerKeyMD5, ExpectedBucketOwner: in.ExpectedBucketOwner, RequestPayer: in.RequestPayer})
					e = uploadErr
					if e == nil {
						completed = types.CompletedPart{PartNumber: number, ETag: out.ETag, ChecksumCRC64NVME: out.ChecksumCRC64NVME, ChecksumCRC32: out.ChecksumCRC32, ChecksumCRC32C: out.ChecksumCRC32C, ChecksumSHA1: out.ChecksumSHA1, ChecksumSHA256: out.ChecksumSHA256}
					}
				} else {
					var sourceRange *string
					if count > 1 {
						sourceRange = aws.String(fmt.Sprintf("bytes=%d-%d", start, end))
					}
					out, copyErr := c.sdk.UploadPartCopy(workCtx, &s3.UploadPartCopyInput{Bucket: in.Bucket, Key: in.Key, UploadId: uploadID, PartNumber: number, CopySource: aws.String(source), CopySourceRange: sourceRange, CopySourceIfMatch: match, CopySourceIfNoneMatch: in.CopySourceIfNoneMatch, CopySourceIfModifiedSince: in.CopySourceIfModifiedSince, CopySourceIfUnmodifiedSince: in.CopySourceIfUnmodifiedSince, CopySourceSSECustomerAlgorithm: in.CopySourceSSECustomerAlgorithm, CopySourceSSECustomerKey: in.CopySourceSSECustomerKey, CopySourceSSECustomerKeyMD5: in.CopySourceSSECustomerKeyMD5, SSECustomerAlgorithm: in.SSECustomerAlgorithm, SSECustomerKey: in.SSECustomerKey, SSECustomerKeyMD5: in.SSECustomerKeyMD5, ExpectedBucketOwner: in.ExpectedBucketOwner, ExpectedSourceBucketOwner: in.ExpectedSourceBucketOwner, RequestPayer: in.RequestPayer})
					e = copyErr
					if e == nil && out.CopyPartResult != nil {
						part := out.CopyPartResult
						completed = types.CompletedPart{PartNumber: number, ETag: part.ETag, ChecksumCRC64NVME: part.ChecksumCRC64NVME, ChecksumCRC32: part.ChecksumCRC32, ChecksumCRC32C: part.ChecksumCRC32C, ChecksumSHA1: part.ChecksumSHA1, ChecksumSHA256: part.ChecksumSHA256}
					}
				}
				mu.Lock()
				if e == nil && completed.ETag == nil {
					e = errors.New("copy part returned no ETag")
				}
				if e != nil {
					if firstErr == nil {
						firstErr = e
						cancel()
					}
					mu.Unlock()
					return
				}
				parts[i] = completed
				done += end - start + 1
				if opts.Progress != nil {
					opts.Progress(done)
				}
				mu.Unlock()
			}
		}()
	}
send:
	for i := 0; i < count; i++ {
		select {
		case jobs <- i:
		case <-workCtx.Done():
			break send
		}
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	// S3 exposes no conditional GetObjectTagging. Recheck before commit to
	// catch changes during transfer; this cannot eliminate the final TOCTOU window.
	if sourceTags != nil {
		tags, e := c.sdk.GetObjectTagging(ctx, sourceTags)
		if e != nil && !tagsUnsupported(e) {
			return newTagReadError("cannot recheck source tags", e)
		}
		if e == nil && encodeTags(tags.TagSet) != tagString {
			return errors.New("source tags changed during copy; retry after changes stop")
		}
	}
	_, err = c.sdk.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{IfMatch: in.IfMatch, IfNoneMatch: in.IfNoneMatch, MpuObjectSize: aws.Int64(size), Bucket: in.Bucket, Key: in.Key, UploadId: uploadID, MultipartUpload: &types.CompletedMultipartUpload{Parts: parts}, SSECustomerAlgorithm: in.SSECustomerAlgorithm, SSECustomerKey: in.SSECustomerKey, SSECustomerKeyMD5: in.SSECustomerKeyMD5, ExpectedBucketOwner: in.ExpectedBucketOwner, RequestPayer: in.RequestPayer})
	if err != nil {
		return err
	}
	finished = true
	return nil
}

func encodeTags(set []types.Tag) string {
	q := url.Values{}
	for _, tag := range set {
		q.Set(aws.ToString(tag.Key), aws.ToString(tag.Value))
	}
	return q.Encode()
}

// tagsUnsupported reports whether a tagging call failed because the provider
// lacks the API; tags are then skipped instead of failing the copy. AccessDenied
// and every other error are NOT "unsupported": the tags exist but cannot be
// read, and a copy without them would lose data (a Move would then delete the
// only copy that has them).
func tagsUnsupported(err error) bool {
	switch ErrorCode(err) {
	case "NotImplemented", "XNotImplemented", "MethodNotAllowed", "UnsupportedOperation":
		return true
	}
	return isStatus(err, 501) || isStatus(err, 405)
}

// tagReadError is a failed tag read; Unwrap keeps the service's error code
// (storage.ErrorCode, app classification) visible.
type tagReadError struct {
	what string
	code string
	err  error
}

func newTagReadError(what string, err error) error {
	return &tagReadError{what: what, code: ErrorCode(err), err: err}
}

func (e *tagReadError) Error() string {
	if e.code != "" {
		return e.what + ": " + e.code
	}
	return e.what + ": " + e.err.Error()
}

func (e *tagReadError) Unwrap() error { return e.err }

// smithy.APIError, so that the code and this message (not just the inner
// service message) are what callers classifying the error see.
func (e *tagReadError) ErrorCode() string             { return e.code }
func (e *tagReadError) ErrorMessage() string          { return e.Error() }
func (e *tagReadError) ErrorFault() smithy.ErrorFault { return smithy.FaultUnknown }

// copyPartSize picks the UploadPartCopy part size: the requested size (default
// 64 MiB), scaled up so that no more than 10,000 parts are needed.
func copyPartSize(size, requested int64) (int64, error) {
	partSize := requested
	if partSize == 0 {
		partSize = 64 * 1024 * 1024
	}
	if (size+partSize-1)/partSize > 10000 {
		partSize = (size + 9999) / 10000
	}
	if partSize > 5*1024*1024*1024 {
		return 0, errors.New("copy part size exceeds 5 GiB")
	}
	return partSize, nil
}
