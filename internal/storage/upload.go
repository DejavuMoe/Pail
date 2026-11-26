package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	tm "github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	tmtypes "github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// defaultTransferPart is the part size when the caller names none (the S3
// minimum, as with the earlier SDK manager).
const defaultTransferPart = 5 << 20

const (
	defaultUploadConcurrency = 5     // as the SDK transfer manager
	maxUploadParts           = 10000 // S3 limit
	// streamBufferBudget bounds the part buffers of an upload whose body
	// cannot be read at an offset; one part is always allowed.
	streamBufferBudget = 64 << 20
	// maxProgressStep caps one counted read, so progress moves in small steps
	// even when the transport reads in large chunks (HTTP/2).
	maxProgressStep = 64 << 10
)

// Upload sends in.Body, using multipart for bodies of at least one part. A
// body that is an io.ReaderAt and io.Seeker (a file) is read in place, one
// section per request, so memory does not grow with the part size; any other
// body is buffered one part at a time within a fixed budget. On failure or
// cancellation the multipart session is aborted.
func (c *Client) Upload(ctx context.Context, in *s3.PutObjectInput, opts TransferOptions) (*tm.UploadObjectOutput, error) {
	if err := opts.validate(); err != nil {
		return nil, err
	}
	if in == nil || in.Body == nil {
		return nil, errors.New("upload requires an input body")
	}
	u := &uploader{c: c, in: in, ctr: &counter{report: opts.Progress}, partSize: opts.PartSize, workers: opts.Concurrency}
	if u.partSize == 0 {
		u.partSize = defaultTransferPart
	}
	if u.workers == 0 {
		u.workers = defaultUploadConcurrency
	}
	if ra, off, n, ok := sectionOf(in.Body, in.ContentLength); ok {
		u.ctr.size = n
		u.fitParts(n)
		// One part or less goes up with a single PutObject.
		if n < u.partSize {
			return u.single(ctx, io.NewSectionReader(ra, off, n), n)
		}
		count := (n + u.partSize - 1) / u.partSize
		return u.multipart(ctx, n, func(_ context.Context, num int32) (*uploadPart, error) {
			start := int64(num-1) * u.partSize
			if int64(num) > count {
				return nil, nil
			}
			size := min(u.partSize, n-start)
			return &uploadPart{num: num, body: io.NewSectionReader(ra, off+start, size), size: size}, nil
		})
	}
	return u.stream(ctx)
}

// sectionOf reports whether body can be read at offsets, and the range to
// send: from the current position to the end, or ContentLength bytes.
func sectionOf(body io.Reader, length *int64) (io.ReaderAt, int64, int64, bool) {
	ra, ok := body.(io.ReaderAt)
	s, ok2 := body.(io.Seeker)
	if !ok || !ok2 {
		return nil, 0, 0, false
	}
	pos, err := s.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, 0, 0, false
	}
	if length != nil && *length >= 0 {
		return ra, pos, *length, true
	}
	end, err := s.Seek(0, io.SeekEnd)
	if _, restore := s.Seek(pos, io.SeekStart); err != nil || restore != nil || end < pos {
		return nil, 0, 0, false
	}
	return ra, pos, end - pos, true
}

type uploader struct {
	c        *Client
	in       *s3.PutObjectInput
	ctr      *counter
	partSize int64
	workers  int
}

// fitParts grows the part size so that size bytes need at most 10,000 parts,
// as the SDK transfer manager does.
func (u *uploader) fitParts(size int64) {
	if size/u.partSize >= maxUploadParts {
		u.partSize = size/maxUploadParts + 1
	}
}

// checksumAlgorithm is what the SDK transfer manager asks for: the caller's
// algorithm, else CRC32 unless checksums are only sent when required (custom
// endpoints, many of which reject or mishandle the flexible checksum headers).
func (u *uploader) checksumAlgorithm() types.ChecksumAlgorithm {
	if u.in.ChecksumAlgorithm != "" {
		return u.in.ChecksumAlgorithm
	}
	if u.c.reqChecksum != aws.RequestChecksumCalculationWhenRequired {
		return types.ChecksumAlgorithmCrc32
	}
	return ""
}

// options are the per-call client options of every request of the upload.
// The checksum mode is set explicitly, as the transfer manager does, so the
// algorithm chosen above and the SDK behaviour agree.
func (u *uploader) options(p *partProgress) []func(*s3.Options) {
	opts := []func(*s3.Options){func(o *s3.Options) { o.RequestChecksumCalculation = u.c.reqChecksum }}
	if p != nil {
		opts = append(opts, p.wrap)
	}
	return opts
}

// track returns the progress accounting for one request body, or nil when
// nobody listens.
func (u *uploader) track(length int64) *partProgress {
	if u.ctr.report == nil {
		return nil
	}
	return &partProgress{ctr: u.ctr, length: length}
}

func (u *uploader) single(ctx context.Context, body io.ReadSeeker, n int64) (*tm.UploadObjectOutput, error) {
	put := *u.in
	put.Body = body
	put.ContentLength = aws.Int64(n)
	put.ChecksumAlgorithm = u.checksumAlgorithm()
	p := u.track(n)
	out, err := u.c.sdk.PutObject(TransferContext(ctx), &put, u.options(p)...)
	p.finish(err == nil)
	if err != nil {
		return nil, err
	}
	return &tm.UploadObjectOutput{
		Bucket: u.in.Bucket, Key: u.in.Key,
		BucketKeyEnabled:        out.BucketKeyEnabled,
		ChecksumCRC32:           out.ChecksumCRC32,
		ChecksumCRC32C:          out.ChecksumCRC32C,
		ChecksumCRC64NVME:       out.ChecksumCRC64NVME,
		ChecksumSHA1:            out.ChecksumSHA1,
		ChecksumSHA256:          out.ChecksumSHA256,
		ChecksumSHA512:          out.ChecksumSHA512,
		ChecksumType:            tmtypes.ChecksumType(out.ChecksumType),
		ContentLength:           aws.Int64(n),
		ETag:                    out.ETag,
		Expiration:              out.Expiration,
		RequestCharged:          tmtypes.RequestCharged(out.RequestCharged),
		SSECustomerAlgorithm:    out.SSECustomerAlgorithm,
		SSECustomerKeyMD5:       out.SSECustomerKeyMD5,
		SSEKMSEncryptionContext: out.SSEKMSEncryptionContext,
		SSEKMSKeyID:             out.SSEKMSKeyId,
		ServerSideEncryption:    tmtypes.ServerSideEncryption(out.ServerSideEncryption),
		Size:                    out.Size,
		VersionID:               out.VersionId,
		ResultMetadata:          out.ResultMetadata,
	}, nil
}

// uploadPart is one UploadPart body. release returns its buffer, if any.
type uploadPart struct {
	num     int32
	body    io.ReadSeeker
	size    int64
	release func()
}

func (p *uploadPart) done() {
	if p.release != nil {
		p.release()
	}
}

// multipart runs CreateMultipartUpload, UploadPart on u.workers goroutines
// for every part next yields (nil when there are no more), and
// CompleteMultipartUpload. The first error wins and stops the other parts;
// the session is then aborted with a context detached from cancellation. No
// goroutine outlives the call.
func (u *uploader) multipart(ctx context.Context, size int64, next func(context.Context, int32) (*uploadPart, error)) (out *tm.UploadObjectOutput, result error) {
	in := u.in
	algo := u.checksumAlgorithm()
	created, err := u.c.sdk.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket: in.Bucket, Key: in.Key, ACL: in.ACL, BucketKeyEnabled: in.BucketKeyEnabled,
		CacheControl: in.CacheControl, ChecksumAlgorithm: algo,
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
	}, u.options(nil)...)
	if err != nil {
		return nil, err
	}
	uploadID := created.UploadId
	if aws.ToString(uploadID) == "" {
		return nil, errors.New("service returned empty multipart upload ID")
	}
	finished := false
	defer func() {
		if finished {
			return
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_, e := u.c.sdk.AbortMultipartUpload(cleanup, &s3.AbortMultipartUploadInput{Bucket: in.Bucket, Key: in.Key, UploadId: uploadID, ExpectedBucketOwner: in.ExpectedBucketOwner, RequestPayer: in.RequestPayer})
		if e != nil {
			result = errors.Join(result, fmt.Errorf("abort multipart upload %s: %w", *uploadID, e))
		}
	}()

	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		mu       sync.Mutex
		firstErr error
		parts    []types.CompletedPart
		wg       sync.WaitGroup
	)
	fail := func(e error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = e
			cancel()
		}
		mu.Unlock()
	}
	jobs := make(chan *uploadPart)
	for range u.workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range jobs {
				if workCtx.Err() == nil {
					cp, e := u.sendPart(workCtx, uploadID, algo, p)
					if e != nil {
						fail(e)
					} else {
						mu.Lock()
						parts = append(parts, cp)
						mu.Unlock()
					}
				}
				p.done()
			}
		}()
	}
produce:
	for num := int32(1); ; num++ {
		p, e := next(workCtx, num)
		if e != nil {
			fail(e)
			break
		}
		if p == nil {
			break
		}
		if num > maxUploadParts {
			p.done()
			fail(fmt.Errorf("the upload needs more than %d parts; use a larger part size", maxUploadParts))
			break
		}
		select {
		case jobs <- p:
		case <-workCtx.Done():
			p.done()
			break produce
		}
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Parts must be listed in PartNumber order.
	sort.Slice(parts, func(i, j int) bool { return aws.ToInt32(parts[i].PartNumber) < aws.ToInt32(parts[j].PartNumber) })
	complete := &s3.CompleteMultipartUploadInput{
		Bucket: in.Bucket, Key: in.Key, UploadId: uploadID,
		ChecksumCRC32: in.ChecksumCRC32, ChecksumCRC32C: in.ChecksumCRC32C, ChecksumCRC64NVME: in.ChecksumCRC64NVME,
		ChecksumSHA1: in.ChecksumSHA1, ChecksumSHA256: in.ChecksumSHA256,
		ExpectedBucketOwner: in.ExpectedBucketOwner,
		// Conditional writes are enforced at commit (If-None-Match: * for the
		// skip and rename policies, If-Match for overwrite checks).
		IfMatch: in.IfMatch, IfNoneMatch: in.IfNoneMatch,
		MultipartUpload:      &types.CompletedMultipartUpload{Parts: parts},
		RequestPayer:         in.RequestPayer,
		SSECustomerAlgorithm: in.SSECustomerAlgorithm, SSECustomerKey: in.SSECustomerKey, SSECustomerKeyMD5: in.SSECustomerKeyMD5,
	}
	if size > 0 {
		complete.MpuObjectSize = aws.Int64(size)
	}
	done, err := u.c.sdk.CompleteMultipartUpload(ctx, complete, u.options(nil)...)
	if err != nil {
		return nil, err
	}
	finished = true
	tmParts := make([]tmtypes.CompletedPart, len(parts))
	for i, p := range parts {
		tmParts[i] = tmtypes.CompletedPart{ChecksumCRC32: p.ChecksumCRC32, ChecksumCRC32C: p.ChecksumCRC32C, ChecksumSHA1: p.ChecksumSHA1, ChecksumSHA256: p.ChecksumSHA256, ChecksumSHA512: p.ChecksumSHA512, ETag: p.ETag, PartNumber: p.PartNumber}
	}
	return &tm.UploadObjectOutput{
		Bucket: in.Bucket, Key: done.Key, UploadID: uploadID, CompletedParts: tmParts,
		BucketKeyEnabled:     done.BucketKeyEnabled,
		ChecksumCRC32:        done.ChecksumCRC32,
		ChecksumCRC32C:       done.ChecksumCRC32C,
		ChecksumCRC64NVME:    done.ChecksumCRC64NVME,
		ChecksumSHA1:         done.ChecksumSHA1,
		ChecksumSHA256:       done.ChecksumSHA256,
		ChecksumSHA512:       done.ChecksumSHA512,
		ChecksumType:         tmtypes.ChecksumType(done.ChecksumType),
		ContentLength:        aws.Int64(size),
		ETag:                 done.ETag,
		Expiration:           done.Expiration,
		Location:             done.Location,
		RequestCharged:       tmtypes.RequestCharged(done.RequestCharged),
		SSEKMSKeyID:          done.SSEKMSKeyId,
		ServerSideEncryption: tmtypes.ServerSideEncryption(done.ServerSideEncryption),
		VersionID:            done.VersionId,
		ResultMetadata:       done.ResultMetadata,
	}, nil
}

// sendPart uploads one part. SSE-C keys must accompany every part; the part
// checksums S3 returns go back in the CompleteMultipartUpload request, as the
// transfer manager sends them, when the upload asked for an algorithm.
func (u *uploader) sendPart(ctx context.Context, uploadID *string, algo types.ChecksumAlgorithm, p *uploadPart) (types.CompletedPart, error) {
	in := u.in
	pp := u.track(p.size)
	out, err := u.c.sdk.UploadPart(TransferContext(ctx), &s3.UploadPartInput{
		Bucket: in.Bucket, Key: in.Key, UploadId: uploadID, PartNumber: aws.Int32(p.num),
		Body: p.body, ContentLength: aws.Int64(p.size), ChecksumAlgorithm: algo,
		ExpectedBucketOwner: in.ExpectedBucketOwner, RequestPayer: in.RequestPayer,
		SSECustomerAlgorithm: in.SSECustomerAlgorithm, SSECustomerKey: in.SSECustomerKey, SSECustomerKeyMD5: in.SSECustomerKeyMD5,
	}, u.options(pp)...)
	pp.finish(err == nil)
	if err != nil {
		return types.CompletedPart{}, err
	}
	cp := types.CompletedPart{PartNumber: aws.Int32(p.num), ETag: out.ETag}
	// Without an algorithm on the upload, a checksum the service volunteers
	// stays out of the Complete request: the session has no checksum type
	// to match it against.
	if algo != "" {
		cp.ChecksumCRC32, cp.ChecksumCRC32C = out.ChecksumCRC32, out.ChecksumCRC32C
		cp.ChecksumSHA1, cp.ChecksumSHA256, cp.ChecksumSHA512 = out.ChecksumSHA1, out.ChecksumSHA256, out.ChecksumSHA512
	}
	return cp, nil
}

// stream uploads a body that can only be read once, in order. Parts are read
// into buffers that are allocated on demand and reused; at most
// streamBufferBudget of them exist (one part when the part size alone is
// larger), which also bounds how many parts are in flight.
func (u *uploader) stream(ctx context.Context) (*tm.UploadObjectOutput, error) {
	size := int64(-1)
	if u.in.ContentLength != nil && *u.in.ContentLength >= 0 {
		size = *u.in.ContentLength
		u.ctr.size = size
		u.fitParts(size)
	}
	body := u.in.Body
	if size >= 0 {
		body = io.LimitReader(body, size)
	}
	// The first read decides between one PutObject and a multipart upload,
	// so a body known to be small never gets a full part buffer.
	firstCap := u.partSize
	if size >= 0 && size < firstCap {
		firstCap = size
	}
	first := make([]byte, firstCap)
	n, err := io.ReadFull(body, first)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, err
	}
	if int64(n) < u.partSize {
		if size >= 0 && int64(n) != size {
			return nil, fmt.Errorf("the body ended after %d of %d bytes", n, size)
		}
		return u.single(ctx, bytes.NewReader(first[:n]), int64(n))
	}
	bufs := max(1, min(int(streamBufferBudget/u.partSize), u.workers+1))
	u.workers = max(1, min(u.workers, bufs-1))
	pool := &partPool{size: u.partSize, free: make(chan []byte, bufs), left: bufs - 1}
	pending := first
	read := int64(n)
	return u.multipart(ctx, size, func(ctx context.Context, num int32) (*uploadPart, error) {
		buf := pending
		pending = nil
		if buf == nil {
			var err error
			if buf, err = pool.get(ctx); err != nil {
				return nil, err
			}
			n, err := io.ReadFull(body, buf)
			if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
				pool.put(buf)
				return nil, err
			}
			if n == 0 {
				pool.put(buf)
				if size >= 0 && read != size {
					return nil, fmt.Errorf("the body ended after %d of %d bytes", read, size)
				}
				return nil, nil
			}
			read += int64(n)
			buf = buf[:n]
		}
		return &uploadPart{num: num, body: bytes.NewReader(buf), size: int64(len(buf)), release: func() { pool.put(buf[:cap(buf)]) }}, nil
	})
}

// partPool hands out part buffers, allocating up to left more before
// waiting for one to come back.
type partPool struct {
	size int64
	free chan []byte
	mu   sync.Mutex
	left int
}

func (p *partPool) get(ctx context.Context) ([]byte, error) {
	select {
	case b := <-p.free:
		return b, nil
	default:
	}
	p.mu.Lock()
	if p.left > 0 {
		p.left--
		p.mu.Unlock()
		return make([]byte, p.size), nil
	}
	p.mu.Unlock()
	select {
	case b := <-p.free:
		return b, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *partPool) put(b []byte) {
	if int64(cap(b)) != p.size {
		return // the first part, sized to the body
	}
	select {
	case p.free <- b:
	default:
	}
}

// partProgress feeds the request body bytes of one PutObject or UploadPart
// call to the transfer counter as the HTTP client reads them. Counting at the
// HTTP layer, rather than on the input body, skips the read the SDK makes to
// hash the payload before sending it. Each HTTP attempt counts at most length
// bytes (aws-chunked framing makes the wire body longer); a failed attempt's
// bytes are taken back, and reads that arrive after that are ignored, so a
// retried part is counted once. A successful call tops the part up to length.
type partProgress struct {
	ctr    *counter
	length int64
	mu     sync.Mutex
	cur    *attempt
}

type attempt struct {
	n    int64
	dead bool
}

// wrap is a per-call client option that counts through o.HTTPClient.
func (p *partProgress) wrap(o *s3.Options) {
	o.HTTPClient = &progressClient{base: o.HTTPClient, p: p}
}

func (p *partProgress) start() *attempt {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cur != nil {
		p.dropLocked(p.cur)
	}
	p.cur = &attempt{}
	return p.cur
}

func (p *partProgress) dropLocked(a *attempt) {
	if !a.dead {
		a.dead = true
		p.ctr.sub(a.n)
	}
}

func (p *partProgress) drop(a *attempt) {
	p.mu.Lock()
	p.dropLocked(a)
	p.mu.Unlock()
}

func (p *partProgress) count(a *attempt, n int) {
	p.mu.Lock()
	d := min(int64(n), p.length-a.n)
	if a.dead || d <= 0 {
		p.mu.Unlock()
		return
	}
	a.n += d
	p.mu.Unlock()
	// Outside the lock: a drop in between subtracts a.n, which already holds
	// d, so the running total still comes out right.
	p.ctr.add(d)
}

// finish settles the call: on success the part counts in full, otherwise the
// last attempt is taken back. A nil receiver (no progress listener) is a no-op.
func (p *partProgress) finish(ok bool) {
	if p == nil {
		return
	}
	p.mu.Lock()
	credit := int64(0)
	if ok {
		credit = p.length
		if p.cur != nil && !p.cur.dead {
			credit -= p.cur.n
			p.cur.dead = true
		}
	} else if p.cur != nil {
		p.dropLocked(p.cur)
	}
	p.mu.Unlock()
	p.ctr.add(credit)
}

type progressClient struct {
	base s3.HTTPClient
	p    *partProgress
}

func (h *progressClient) Do(req *http.Request) (*http.Response, error) {
	if req.Body == nil || req.Body == http.NoBody {
		return h.base.Do(req)
	}
	a := h.p.start()
	r := req.WithContext(req.Context())
	r.Body = &countingBody{ReadCloser: req.Body, p: h.p, a: a}
	resp, err := h.base.Do(r)
	if err != nil || resp.StatusCode < 200 || resp.StatusCode > 299 {
		h.p.drop(a)
	}
	return resp, err
}

type countingBody struct {
	io.ReadCloser
	p *partProgress
	a *attempt
}

func (b *countingBody) Read(buf []byte) (int, error) {
	if len(buf) > maxProgressStep {
		buf = buf[:maxProgressStep]
	}
	n, err := b.ReadCloser.Read(buf)
	if n > 0 {
		b.p.count(b.a, n)
	}
	return n, err
}
