package storage

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// ObjectID names one object or one specific version.
type ObjectID struct{ Key, VersionID string }

// DeleteStatus is the per-item outcome of DeleteObjects.
type DeleteStatus string

const (
	DeleteSucceeded   DeleteStatus = "succeeded"   // service acknowledged exactly this key/version
	DeleteFailed      DeleteStatus = "failed"      // service rejected it; safe to offer a retry
	DeleteUnknown     DeleteStatus = "unknown"     // outcome ambiguous; inspect before retrying
	DeleteUnprocessed DeleteStatus = "unprocessed" // request was never sent
)

// DeleteResult is the outcome for the input item at the same index.
type DeleteResult struct {
	Key, VersionID string
	Status         DeleteStatus
	Code, Message  string
}

const deleteChunk = 1000

// DeleteObjects deletes ids in chunks of 1000 and returns one result per input,
// in input order. Requests use the standard SDK retryer, so a replayed
// idempotent delete is possible; acknowledgements that are missing, repeated or
// unrequested therefore yield DeleteUnknown, never DeleteSucceeded. After a
// context cancellation the remaining chunks are DeleteUnprocessed. A chunk the
// service refused with a 4xx answer is DeleteFailed (nothing was deleted); a
// transport error, timeout or 5xx leaves it DeleteUnknown. Neither stops later
// chunks unless the context is done.
func (c *Client) DeleteObjects(ctx context.Context, bucket string, ids []ObjectID) []DeleteResult {
	res := make([]DeleteResult, len(ids))
	for i, id := range ids {
		res[i] = DeleteResult{Key: id.Key, VersionID: id.VersionID, Status: DeleteUnprocessed, Message: "request not sent"}
	}
	stopped := false
	for start := 0; start < len(ids); start += deleteChunk {
		end := min(start+deleteChunk, len(ids))
		chunk := res[start:end]
		if stopped || ctx.Err() != nil {
			break
		}
		if bucket == "" {
			setAll(chunk, DeleteFailed, "InvalidArgument", "bucket is required")
			continue
		}
		// Unique, non-empty identities, preserving order.
		index := map[ObjectID][]int{}
		var objs []types.ObjectIdentifier
		for i, id := range ids[start:end] {
			if id.Key == "" {
				chunk[i].Status, chunk[i].Code, chunk[i].Message = DeleteFailed, "InvalidArgument", "empty key"
				continue
			}
			if _, seen := index[id]; !seen {
				o := types.ObjectIdentifier{Key: aws.String(id.Key)}
				if id.VersionID != "" {
					o.VersionId = aws.String(id.VersionID)
				}
				objs = append(objs, o)
			}
			index[id] = append(index[id], i)
		}
		if len(objs) == 0 {
			continue
		}
		out, err := c.sdk.DeleteObjects(ctx, &s3.DeleteObjectsInput{Bucket: aws.String(bucket), Delete: &types.Delete{Objects: objs, Quiet: aws.Bool(false)}})
		if err != nil {
			status, code, msg := DeleteUnknown, "RequestOutcomeUnknown", err.Error()
			if refused, rcode, rmsg := definitiveRefusal(err); refused {
				// The service answered the whole request with a 4xx: it was
				// not executed, nothing was deleted.
				status, code, msg = DeleteFailed, rcode, rmsg
			}
			for _, idx := range index {
				for _, i := range idx {
					chunk[i].Status, chunk[i].Code, chunk[i].Message = status, code, msg
				}
			}
			stopped = ctx.Err() != nil
			continue
		}
		reconcileDelete(chunk, index, out)
	}
	return res
}

// definitiveRefusal reports whether err is the service's own 4xx answer to a
// DeleteObjects request (403, 400 MalformedXML, 404 NoSuchBucket, ...), which
// proves that nothing was deleted. Transport errors, timeouts (408) and 5xx
// answers are not definitive: the request may have been executed.
func definitiveRefusal(err error) (bool, string, string) {
	var re *awshttp.ResponseError
	if !errors.As(err, &re) {
		return false, "", ""
	}
	status := re.HTTPStatusCode()
	if status < 400 || status >= 500 || status == 408 {
		return false, "", ""
	}
	code, msg := ErrorCode(err), err.Error()
	var api smithy.APIError
	if errors.As(err, &api) && api.ErrorMessage() != "" {
		msg = api.ErrorMessage()
	}
	if code == "" {
		code = fmt.Sprintf("HTTP%d", status)
	}
	return true, code, msg
}

func setAll(rs []DeleteResult, st DeleteStatus, code, msg string) {
	for i := range rs {
		rs[i].Status, rs[i].Code, rs[i].Message = st, code, msg
	}
}

// reconcileDelete maps acknowledgements onto items. Missing or duplicated
// acknowledgements stay unknown even if one claims success; unrequested pairs
// are ignored (and make nothing succeed).
func reconcileDelete(chunk []DeleteResult, index map[ObjectID][]int, out *s3.DeleteObjectsOutput) {
	type ack struct {
		status     DeleteStatus
		code, mess string
	}
	acks := map[ObjectID][]ack{}
	if out != nil {
		for _, d := range out.Deleted {
			id := ObjectID{aws.ToString(d.Key), aws.ToString(d.VersionId)}
			acks[id] = append(acks[id], ack{status: DeleteSucceeded})
		}
		for _, e := range out.Errors {
			id := ObjectID{aws.ToString(e.Key), aws.ToString(e.VersionId)}
			acks[id] = append(acks[id], ack{DeleteFailed, aws.ToString(e.Code), aws.ToString(e.Message)})
		}
	}
	for id, idx := range index {
		a := acks[id]
		for _, i := range idx {
			switch len(a) {
			case 1:
				chunk[i].Status, chunk[i].Code, chunk[i].Message = a[0].status, a[0].code, a[0].mess
			case 0:
				chunk[i].Status, chunk[i].Code, chunk[i].Message = DeleteUnknown, "MissingResult", "response omitted this key and version ID; inspect before retrying"
			default:
				chunk[i].Status, chunk[i].Code, chunk[i].Message = DeleteUnknown, "DuplicateResult", "response repeated this key and version ID; inspect before retrying"
			}
		}
	}
}

// ProbeConditionalWrites reports whether the service honours If-None-Match:* on
// PutObject. It writes (and removes) one empty probe object, so call it only
// after the user has accepted writes to the bucket; on a versioned bucket the
// removal leaves a delete marker. A service that rejects the header as
// unimplemented reports false without error.
func (c *Client) ProbeConditionalWrites(ctx context.Context, bucket string) (bool, error) {
	var rnd [6]byte
	_, _ = rand.Read(rnd[:])
	key := ".pail-probe-" + hex.EncodeToString(rnd[:])
	put := func() error {
		_, err := c.sdk.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), Body: bytes.NewReader(nil), IfNoneMatch: aws.String("*")})
		return err
	}
	if err := put(); err != nil {
		if ErrorCode(err) == "NotImplemented" {
			return false, nil
		}
		return false, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_, _ = c.sdk.DeleteObject(cleanup, &s3.DeleteObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	}()
	err := put()
	switch {
	case err == nil:
		return false, nil // second create-only write succeeded: header ignored
	case ErrorCode(err) == "PreconditionFailed" || isStatus(err, 412):
		return true, nil
	case ErrorCode(err) == "NotImplemented":
		return false, nil
	}
	return false, err
}

func isStatus(err error, code int) bool {
	var re *awshttp.ResponseError
	return errors.As(err, &re) && re.HTTPStatusCode() == code
}
