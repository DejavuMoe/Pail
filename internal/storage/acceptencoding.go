package storage

import (
	"context"

	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// The S3 client sends "Accept-Encoding: identity" and SigV4 signs it. Proxies
// such as Cloudflare rewrite that header on the way to the origin, so every
// request then fails with SignatureDoesNotMatch. These two finalize steps take
// the header out of the request while it is signed and put it back afterwards:
// it is still sent, just not signed.

type acceptEncodingKey struct{}

type unsignAcceptEncoding struct{}

func (unsignAcceptEncoding) ID() string { return "PailUnsignAcceptEncoding" }

func (unsignAcceptEncoding) HandleFinalize(ctx context.Context, in middleware.FinalizeInput, next middleware.FinalizeHandler) (middleware.FinalizeOutput, middleware.Metadata, error) {
	if req, ok := in.Request.(*smithyhttp.Request); ok {
		if v := req.Header.Get("Accept-Encoding"); v != "" {
			req.Header.Del("Accept-Encoding")
			ctx = context.WithValue(ctx, acceptEncodingKey{}, v)
		}
	}
	return next.HandleFinalize(ctx, in)
}

type restoreAcceptEncoding struct{}

func (restoreAcceptEncoding) ID() string { return "PailRestoreAcceptEncoding" }

func (restoreAcceptEncoding) HandleFinalize(ctx context.Context, in middleware.FinalizeInput, next middleware.FinalizeHandler) (middleware.FinalizeOutput, middleware.Metadata, error) {
	if v, ok := ctx.Value(acceptEncodingKey{}).(string); ok {
		if req, ok := in.Request.(*smithyhttp.Request); ok {
			req.Header.Set("Accept-Encoding", v)
		}
	}
	return next.HandleFinalize(ctx, in)
}

// leaveAcceptEncodingUnsigned is an s3.Options.APIOptions entry. Stacks without
// a "Signing" step (anonymous access) are left alone.
func leaveAcceptEncodingUnsigned(stack *middleware.Stack) error {
	if _, ok := stack.Finalize.Get("Signing"); !ok {
		return nil
	}
	if err := stack.Finalize.Insert(unsignAcceptEncoding{}, "Signing", middleware.Before); err != nil {
		return err
	}
	return stack.Finalize.Insert(restoreAcceptEncoding{}, "Signing", middleware.After)
}
