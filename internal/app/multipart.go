package app

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/DejavuMoe/Pail/internal/storage"
)

// MultipartUploads lists every unfinished multipart upload under loc.Prefix.
func (s *Service) MultipartUploads(ctx context.Context, loc Location) ([]MultipartUpload, error) {
	const op = "MultipartUploads"
	c, err := s.bucketClient(ctx, op, loc)
	if err != nil {
		return nil, err
	}
	out, err := listMultipart(ctx, c, loc.Bucket, loc.Prefix)
	if err != nil || len(out) > 0 || loc.Prefix == "" {
		return out, classify(op, err)
	}
	// MinIO only honours Prefix when it names a whole object key, so an empty
	// answer for a folder is not trusted: list the bucket and filter here.
	all, err := listMultipart(ctx, c, loc.Bucket, "")
	if err != nil {
		return nil, classify(op, err)
	}
	for _, u := range all {
		if strings.HasPrefix(u.Key, loc.Prefix) {
			out = append(out, u)
		}
	}
	return out, nil
}

func listMultipart(ctx context.Context, c *storage.Client, bucket, prefix string) ([]MultipartUpload, error) {
	in := &s3.ListMultipartUploadsInput{Bucket: aws.String(bucket), EncodingType: types.EncodingTypeUrl}
	if prefix != "" {
		in.Prefix = aws.String(prefix)
	}
	var out []MultipartUpload
	for {
		page, err := c.S3().ListMultipartUploads(ctx, in)
		if err != nil {
			return nil, err
		}
		dec := bmDecoder(page.EncodingType)
		for _, u := range page.Uploads {
			mu := MultipartUpload{Key: dec(aws.ToString(u.Key)), UploadID: aws.ToString(u.UploadId),
				Initiated: aws.ToTime(u.Initiated), StorageClass: string(u.StorageClass)}
			if mu.StorageClass == "" {
				mu.StorageClass = string(types.StorageClassStandard)
			}
			if u.Initiator != nil {
				mu.Initiator = aws.ToString(u.Initiator.DisplayName)
				if mu.Initiator == "" {
					mu.Initiator = aws.ToString(u.Initiator.ID)
				}
			}
			out = append(out, mu)
		}
		if !aws.ToBool(page.IsTruncated) || (page.NextKeyMarker == nil && page.NextUploadIdMarker == nil) {
			return out, nil
		}
		// The response was URL-encoded; request markers must be raw.
		in.KeyMarker, in.UploadIdMarker = nil, page.NextUploadIdMarker
		if page.NextKeyMarker != nil {
			in.KeyMarker = aws.String(dec(aws.ToString(page.NextKeyMarker)))
		}
	}
}

// AbortMultipartUploads aborts the given uploads in a background task with one
// item per upload. The upload ID travels in ItemResult.VersionID. An upload that
// no longer exists counts as succeeded.
func (s *Service) AbortMultipartUploads(loc Location, uploads []MultipartUpload) (string, error) {
	const op = "AbortMultipartUploads"
	if err := checkLocation(op, loc, true); err != nil {
		return "", err
	}
	if len(uploads) == 0 {
		return "", invalid(op, "no uploads selected")
	}
	for _, u := range uploads {
		if u.Key == "" || u.UploadID == "" {
			return "", invalid(op, "every upload needs a key and an upload ID")
		}
	}
	return s.submit(bmAbortSpec(s, loc, append([]MultipartUpload(nil), uploads...))), nil
}

func bmAbortSpec(s *Service, loc Location, uploads []MultipartUpload) taskSpec {
	title := fmt.Sprintf("Abort %d multipart upload(s)", len(uploads))
	return taskSpec{
		Kind: KindOther, Title: title, Detail: "s3://" + loc.Bucket + "/" + loc.Prefix,
		Work: func(ctx context.Context, r *run) error {
			r.SetTotal(0, len(uploads))
			c, err := s.bucketClient(ctx, "AbortMultipartUploads", loc)
			if err != nil {
				for _, u := range uploads {
					r.Item(ItemResult{Key: u.Key, VersionID: u.UploadID, Status: ItemUnprocessed, Err: err.Error()})
				}
				return err
			}
			workers := s.Settings().BulkWorkers
			if workers < 1 {
				workers = 1
			}
			ch := make(chan MultipartUpload)
			var wg sync.WaitGroup
			for i := 0; i < workers; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for u := range ch {
						it := ItemResult{Key: u.Key, VersionID: u.UploadID, Status: ItemSucceeded}
						_, err := c.S3().AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
							Bucket: aws.String(loc.Bucket), Key: aws.String(u.Key), UploadId: aws.String(u.UploadID)})
						switch {
						case err == nil:
						case storage.ErrorCode(err) == "NoSuchUpload":
							it.Err = "already gone"
						case ctx.Err() != nil:
							it.Status, it.Err = ItemUnprocessed, "canceled"
						default:
							it.Status, it.Err = ItemFailed, classify("AbortMultipartUploads", err).Error()
						}
						r.Item(it)
					}
				}()
			}
			for _, u := range uploads {
				if ctx.Err() != nil {
					r.Item(ItemResult{Key: u.Key, VersionID: u.UploadID, Status: ItemUnprocessed, Err: "canceled"})
					continue
				}
				select {
				case ch <- u:
				case <-ctx.Done():
					r.Item(ItemResult{Key: u.Key, VersionID: u.UploadID, Status: ItemUnprocessed, Err: "canceled"})
				}
			}
			close(ch)
			wg.Wait()
			return ctx.Err()
		},
		Retry: func(failed []ItemResult) (taskSpec, bool) {
			ups := make([]MultipartUpload, len(failed))
			for i, f := range failed {
				ups[i] = MultipartUpload{Key: f.Key, UploadID: f.VersionID}
			}
			return bmAbortSpec(s, loc, ups), true
		},
	}
}
