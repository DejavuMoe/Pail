package app

import "context"

// FolderSize totals the current objects below loc.Prefix, a folder ("a/b/")
// or, empty, the whole bucket: everything nested under it, folder
// placeholders included. S3 keeps no folder sizes, so this lists the whole
// prefix, a page of up to 1,000 keys per request, and takes as long as the
// folder is large; unlike Estimate it does not stop at a limit, and ends
// only with the listing or ctx. progress, when not nil, receives the running
// total after every page, on the calling goroutine. Older versions, delete
// markers and incomplete multipart uploads are not counted.
func (s *Service) FolderSize(ctx context.Context, loc Location, progress func(Estimate)) (Estimate, error) {
	const op = "FolderSize"
	c, err := s.bucketClient(ctx, op, loc)
	if err != nil {
		return Estimate{}, err
	}
	var est Estimate
	err = bkListObjects(c, loc.Bucket)(ctx, bkScan{Prefix: loc.Prefix}, func(page []bkObj, _ string) error {
		for _, o := range page {
			est.Objects++
			est.Bytes += o.Size
		}
		if progress != nil && len(page) > 0 {
			progress(est)
		}
		return ctx.Err()
	})
	if err != nil {
		return Estimate{}, classify(op, err)
	}
	return est, nil
}
