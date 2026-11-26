package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/aws/aws-sdk-go-v2/aws"
	tm "github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	tmtypes "github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// TransferOptions tunes Upload, Download and Copy. Progress receives cumulative
// bytes and may be called from several goroutines; it must be goroutine-safe and
// cheap. The reported value never decreases and, when the size is known (always
// for uploads of seekable bodies; Size for downloads), never exceeds it. Upload
// progress follows the request bodies as the HTTP client sends them, in steps of
// at most 64 KiB; bytes of a failed attempt are not counted again when it is
// retried. Upload parts may be reported before the object is committed: only a
// nil error establishes completion.
type TransferOptions struct {
	PartSize    int64 // 0 = default (5 MiB upload/download, 64 MiB copy)
	Concurrency int   // 0 = default
	Size        int64 // expected total bytes (downloads); 0 = unknown
	Progress    func(int64)
}

func (o TransferOptions) validate() error {
	if o.PartSize != 0 && (o.PartSize < 5*1024*1024 || o.PartSize > 5*1024*1024*1024) {
		return errors.New("part size must be between 5 MiB and 5 GiB")
	}
	if o.Concurrency < 0 || o.Concurrency > 128 {
		return errors.New("concurrency must be between 0 (default) and 128")
	}
	return nil
}

// counter turns concurrent byte increments into a monotonic, clamped total.
// sub takes back bytes of a failed attempt without reporting a lower value:
// the report resumes once the running total passes what was already shown.
type counter struct {
	total, last atomic.Int64
	size        int64
	report      func(int64)
	// mu orders the reports: the value is chosen and handed to report in one
	// step, so that two parts cannot deliver theirs in the wrong order.
	mu sync.Mutex
}

func (c *counter) add(n int64) {
	if n <= 0 || c.report == nil {
		return
	}
	t := c.total.Add(n)
	if c.size > 0 && t > c.size {
		t = c.size
	}
	if t <= c.last.Load() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if t > c.last.Load() {
		c.last.Store(t)
		c.report(t)
	}
}

func (c *counter) sub(n int64) {
	if n > 0 && c.report != nil {
		c.total.Add(-n)
	}
}

// UploadBytes puts a small in-memory object.
func (c *Client) UploadBytes(ctx context.Context, bucket, key, contentType string, data []byte) (*s3.PutObjectOutput, error) {
	in := &s3.PutObjectInput{Bucket: &bucket, Key: &key, Body: bytes.NewReader(data)}
	if contentType != "" {
		in.ContentType = &contentType
	}
	return c.sdk.PutObject(ctx, in)
}

type progressWriter struct {
	w io.WriterAt
	c *counter
}

func (p *progressWriter) WriteAt(b []byte, off int64) (int, error) {
	n, err := p.w.WriteAt(b, off)
	p.c.add(int64(n))
	return n, err
}

// errRangeIgnored reports a service that answered a ranged GET with the whole
// object and no Content-Range header.
var errRangeIgnored = errors.New("the service ignored the Range header")

// rangeCheckClient stops a ranged download at the first response that carries
// more bytes than the range asked for. Without it the SDK writes the first part
// from that response, hits the end of its buffer and reports a bare EOF.
type rangeCheckClient struct{ *s3.Client }

func (r rangeCheckClient) GetObject(ctx context.Context, in *s3.GetObjectInput, opts ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	out, err := r.Client.GetObject(ctx, in, opts...)
	if err != nil || in.Range == nil || out.ContentRange != nil || out.ContentLength == nil {
		return out, err
	}
	var start, end int64
	if _, e := fmt.Sscanf(*in.Range, "bytes=%d-%d", &start, &end); e != nil || *out.ContentLength <= end-start+1 {
		return out, err
	}
	out.Body.Close()
	return nil, errRangeIgnored
}

// Download fetches an object into w with ranged parallel GETs. Progress is O(1)
// per write; bytes rewritten by a retried range are clamped by opts.Size. A
// service that ignores Range is read once, over a single connection.
func (c *Client) Download(ctx context.Context, in *s3.GetObjectInput, w io.WriterAt, opts TransferOptions) (int64, error) {
	if err := opts.validate(); err != nil {
		return 0, err
	}
	if in == nil || w == nil {
		return 0, errors.New("download requires input and writer")
	}
	if opts.Progress != nil {
		w = &progressWriter{w: w, c: &counter{size: opts.Size, report: opts.Progress}}
	}
	d := tm.New(rangeCheckClient{c.sdk}, func(o *tm.Options) {
		o.PartSizeBytes = defaultTransferPart
		if opts.PartSize > 0 {
			o.PartSizeBytes = opts.PartSize
		}
		if opts.Concurrency > 0 {
			o.Concurrency = opts.Concurrency
		}
		// Byte ranges work on every S3-compatible service; part-number GETs
		// do not. The app verifies downloads itself, so the GETs do not ask
		// for checksums the service might not keep.
		o.GetObjectType = tmtypes.GetObjectType(tmtypes.GetObjectRanges)
		o.DisableChecksumValidation = true
	})
	out, err := d.DownloadObject(TransferContext(ctx), downloadInput(in, w))
	var n int64
	if out != nil {
		n = aws.ToInt64(out.ContentLength)
	}
	if errors.Is(err, errRangeIgnored) {
		// A canceled download is reported as canceled, whichever response was
		// in flight when it happened.
		if ctx.Err() != nil {
			return n, context.Cause(ctx)
		}
		if in.Range == nil {
			return c.downloadWhole(ctx, in, w)
		}
	}
	return n, err
}

// downloadInput maps a GetObject request to the transfer manager's input.
func downloadInput(in *s3.GetObjectInput, w io.WriterAt) *tm.DownloadObjectInput {
	return &tm.DownloadObjectInput{
		Bucket: in.Bucket, Key: in.Key, WriterAt: w,
		ExpectedBucketOwner:        in.ExpectedBucketOwner,
		IfMatch:                    in.IfMatch,
		IfModifiedSince:            in.IfModifiedSince,
		IfNoneMatch:                in.IfNoneMatch,
		IfUnmodifiedSince:          in.IfUnmodifiedSince,
		Range:                      in.Range,
		RequestPayer:               tmtypes.RequestPayer(in.RequestPayer),
		ResponseCacheControl:       in.ResponseCacheControl,
		ResponseContentDisposition: in.ResponseContentDisposition,
		ResponseContentEncoding:    in.ResponseContentEncoding,
		ResponseContentLanguage:    in.ResponseContentLanguage,
		ResponseContentType:        in.ResponseContentType,
		ResponseExpires:            in.ResponseExpires,
		SSECustomerAlgorithm:       in.SSECustomerAlgorithm,
		SSECustomerKey:             in.SSECustomerKey,
		SSECustomerKeyMD5:          in.SSECustomerKeyMD5,
		VersionID:                  in.VersionId,
	}
}

// downloadWhole streams the object with one unranged GET.
func (c *Client) downloadWhole(ctx context.Context, in *s3.GetObjectInput, w io.WriterAt) (int64, error) {
	out, err := c.sdk.GetObject(TransferContext(ctx), in)
	if err != nil {
		return 0, err
	}
	defer out.Body.Close()
	n, err := io.Copy(io.NewOffsetWriter(w, 0), out.Body)
	if err == nil && out.ContentLength != nil && n != *out.ContentLength {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}
