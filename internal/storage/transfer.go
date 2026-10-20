package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// TransferOptions tunes Upload, Download and Copy. Progress receives cumulative
// bytes and may be called from several goroutines; it must be goroutine-safe and
// cheap. The reported value never decreases and, when Size is known, never
// exceeds it. Upload parts may be reported before the object is committed: only
// a nil error establishes completion.
type TransferOptions struct {
	PartSize    int64 // 0 = SDK default (5 MiB upload/download, 64 MiB copy)
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
type counter struct {
	total, last atomic.Int64
	size        int64
	report      func(int64)
}

func (c *counter) add(n int64) {
	if n <= 0 || c.report == nil {
		return
	}
	t := c.total.Add(n)
	if c.size > 0 && t > c.size {
		t = c.size
	}
	for {
		prev := c.last.Load()
		if t <= prev {
			return
		}
		if c.last.CompareAndSwap(prev, t) {
			c.report(t)
			return
		}
	}
}

// progressUploadClient counts successful PutObject/UploadPart calls (so retried
// parts are not double counted) and aborts with a detached context so a
// cancelled upload still cleans up its multipart session.
type progressUploadClient struct {
	*s3.Client
	counter    *counter
	input      *s3.PutObjectInput
	mu         sync.Mutex
	cleanupErr error
}

func bodySize(body io.Reader, length *int64) int64 {
	if length != nil {
		return *length
	}
	s, ok := body.(io.Seeker)
	if !ok {
		return 0
	}
	pos, e := s.Seek(0, io.SeekCurrent)
	if e != nil {
		return 0
	}
	end, e := s.Seek(0, io.SeekEnd)
	_, restore := s.Seek(pos, io.SeekStart)
	if e != nil || restore != nil {
		return 0
	}
	return end - pos
}

func (p *progressUploadClient) PutObject(ctx context.Context, in *s3.PutObjectInput, opts ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	n := bodySize(in.Body, in.ContentLength)
	out, err := p.Client.PutObject(ctx, in, opts...)
	if err == nil {
		p.counter.add(n)
	}
	return out, err
}

func (p *progressUploadClient) UploadPart(ctx context.Context, in *s3.UploadPartInput, opts ...func(*s3.Options)) (*s3.UploadPartOutput, error) {
	n := bodySize(in.Body, in.ContentLength)
	out, err := p.Client.UploadPart(ctx, in, opts...)
	if err == nil {
		p.counter.add(n)
	}
	return out, err
}

func (p *progressUploadClient) AbortMultipartUpload(ctx context.Context, in *s3.AbortMultipartUploadInput, opts ...func(*s3.Options)) (*s3.AbortMultipartUploadOutput, error) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	cp := *in
	cp.ExpectedBucketOwner = p.input.ExpectedBucketOwner
	cp.RequestPayer = p.input.RequestPayer
	out, err := p.Client.AbortMultipartUpload(cleanup, &cp, opts...)
	p.mu.Lock()
	p.cleanupErr = err
	p.mu.Unlock()
	return out, err
}

// Upload sends in.Body, using multipart for large bodies. On failure or
// cancellation the multipart session is aborted.
func (c *Client) Upload(ctx context.Context, in *s3.PutObjectInput, opts TransferOptions) (*manager.UploadOutput, error) {
	if err := opts.validate(); err != nil {
		return nil, err
	}
	if in == nil || in.Body == nil {
		return nil, errors.New("upload requires an input body")
	}
	size := bodySize(in.Body, in.ContentLength)
	api := &progressUploadClient{Client: c.sdk, counter: &counter{size: size, report: opts.Progress}, input: in}
	u := manager.NewUploader(api, func(u *manager.Uploader) {
		if opts.PartSize > 0 {
			u.PartSize = opts.PartSize
		}
		if opts.Concurrency > 0 {
			u.Concurrency = opts.Concurrency
		}
		u.LeavePartsOnError = false
		u.RequestChecksumCalculation = c.reqChecksum
	})
	out, err := u.Upload(ctx, in)
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.cleanupErr != nil {
		err = errors.Join(err, api.cleanupErr)
	}
	return out, err
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

// Download fetches an object into w with ranged parallel GETs. Progress is O(1)
// per write; bytes rewritten by a retried range are clamped by opts.Size.
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
	d := manager.NewDownloader(c.sdk, func(d *manager.Downloader) {
		if opts.PartSize > 0 {
			d.PartSize = opts.PartSize
		}
		if opts.Concurrency > 0 {
			d.Concurrency = opts.Concurrency
		}
	})
	return d.Download(ctx, w, in)
}
