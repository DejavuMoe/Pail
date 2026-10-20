package app

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// This file implements the single-object attributes: tags and ACLs
// (APP_API.md section 6). All of them accept a version ID in the ObjectRef.

// bkStatus returns the HTTP status carried by err, or 0.
func bkStatus(err error) int {
	var hs interface{ HTTPStatusCode() int }
	if errors.As(err, &hs) {
		return hs.HTTPStatusCode()
	}
	return 0
}

const (
	bkMaxTags      = 10
	bkMaxTagKey    = 128
	bkMaxTagValue  = 256
	bkMaxObjectKey = 1024
)

func bkValidTags(op string, tags map[string]string) error {
	if len(tags) > bkMaxTags {
		return invalid(op, "an object can have at most %d tags", bkMaxTags)
	}
	for k, v := range tags {
		switch {
		case k == "":
			return invalid(op, "a tag key must not be empty")
		case utf8.RuneCountInString(k) > bkMaxTagKey:
			return invalid(op, "tag key %.20q... is longer than %d characters", k, bkMaxTagKey)
		case utf8.RuneCountInString(v) > bkMaxTagValue:
			return invalid(op, "the value of tag %q is longer than %d characters", k, bkMaxTagValue)
		case !utf8.ValidString(k) || !utf8.ValidString(v):
			return invalid(op, "tag %q is not valid UTF-8", k)
		case strings.HasPrefix(strings.ToLower(k), "aws:"):
			return invalid(op, "tag keys starting with aws: are reserved")
		}
	}
	return nil
}

func bkRefCheck(op string, ref ObjectRef) error {
	if err := bkValidKey(op, ref.Key); err != nil {
		return err
	}
	return nil
}

// SetTags replaces the tag set of an object (or version). An empty map removes
// all tags.
func (s *Service) SetTags(ctx context.Context, loc Location, ref ObjectRef, tags map[string]string) error {
	const op = "SetTags"
	if err := bkRefCheck(op, ref); err != nil {
		return err
	}
	if err := bkValidTags(op, tags); err != nil {
		return err
	}
	c, err := s.bucketClient(ctx, op, loc)
	if err != nil {
		return err
	}
	if len(tags) == 0 {
		in := &s3.DeleteObjectTaggingInput{Bucket: aws.String(loc.Bucket), Key: aws.String(ref.Key)}
		if ref.VersionID != "" {
			in.VersionId = aws.String(ref.VersionID)
		}
		_, err = c.S3().DeleteObjectTagging(ctx, in)
		return classify(op, err)
	}
	set := make([]types.Tag, 0, len(tags))
	for k, v := range tags {
		set = append(set, types.Tag{Key: aws.String(k), Value: aws.String(v)})
	}
	in := &s3.PutObjectTaggingInput{Bucket: aws.String(loc.Bucket), Key: aws.String(ref.Key), Tagging: &types.Tagging{TagSet: set}}
	if ref.VersionID != "" {
		in.VersionId = aws.String(ref.VersionID)
	}
	_, err = c.S3().PutObjectTagging(ctx, in)
	return classify(op, err)
}

// bkACLErr maps "ACLs are disabled on this bucket" (bucket owner enforced)
// to an Unsupported error; everything else is classified normally.
func bkACLErr(op string, err error) error {
	if err == nil {
		return nil
	}
	if bkACLUnsupported(err) {
		ce := classify(op, err).(*OpError)
		if ce.Kind != ErrUnsupported {
			cp := *ce
			cp.Kind = ErrUnsupported
			cp.Message = "ACLs are not available for this bucket or provider (" + ce.Message + ")"
			return &cp
		}
		return ce
	}
	return classify(op, err)
}

// ACL returns the access control list of an object (or version). Owner is the
// canonical user ID of the owner; Canned is empty on reads.
func (s *Service) ACL(ctx context.Context, loc Location, ref ObjectRef) (ACL, error) {
	const op = "ACL"
	if err := bkRefCheck(op, ref); err != nil {
		return ACL{}, err
	}
	c, err := s.bucketClient(ctx, op, loc)
	if err != nil {
		return ACL{}, err
	}
	in := &s3.GetObjectAclInput{Bucket: aws.String(loc.Bucket), Key: aws.String(ref.Key)}
	if ref.VersionID != "" {
		in.VersionId = aws.String(ref.VersionID)
	}
	out, err := c.S3().GetObjectAcl(ctx, in)
	if err != nil {
		return ACL{}, bkACLErr(op, err)
	}
	var acl ACL
	if out.Owner != nil {
		acl.Owner = aws.ToString(out.Owner.ID)
	}
	for _, g := range out.Grants {
		if g.Grantee == nil {
			continue
		}
		gr := Grant{Type: string(g.Grantee.Type), Permission: string(g.Permission)}
		switch g.Grantee.Type {
		case types.TypeGroup:
			gr.Grantee = aws.ToString(g.Grantee.URI)
		case types.TypeAmazonCustomerByEmail:
			gr.Grantee = aws.ToString(g.Grantee.EmailAddress)
		default:
			gr.Grantee = aws.ToString(g.Grantee.ID)
		}
		acl.Grants = append(acl.Grants, gr)
	}
	return acl, nil
}

var bkCannedACLs = map[string]types.ObjectCannedACL{
	"private": types.ObjectCannedACLPrivate, "public-read": types.ObjectCannedACLPublicRead,
	"public-read-write": types.ObjectCannedACLPublicReadWrite, "authenticated-read": types.ObjectCannedACLAuthenticatedRead,
	"aws-exec-read": types.ObjectCannedACLAwsExecRead, "bucket-owner-read": types.ObjectCannedACLBucketOwnerRead,
	"bucket-owner-full-control": types.ObjectCannedACLBucketOwnerFullControl,
}

var bkPermissions = map[string]types.Permission{
	"READ": types.PermissionRead, "WRITE": types.PermissionWrite, "READ_ACP": types.PermissionReadAcp,
	"WRITE_ACP": types.PermissionWriteAcp, "FULL_CONTROL": types.PermissionFullControl,
}

// SetACL replaces the ACL of an object (or version). A non-empty acl.Canned
// takes precedence over acl.Grants. With grants, acl.Owner (the canonical ID)
// is used when given; otherwise the current owner is read first.
func (s *Service) SetACL(ctx context.Context, loc Location, ref ObjectRef, acl ACL) error {
	const op = "SetACL"
	if err := bkRefCheck(op, ref); err != nil {
		return err
	}
	in := &s3.PutObjectAclInput{Bucket: aws.String(loc.Bucket), Key: aws.String(ref.Key)}
	if ref.VersionID != "" {
		in.VersionId = aws.String(ref.VersionID)
	}
	var grants []types.Grant
	if acl.Canned != "" {
		canned, ok := bkCannedACLs[acl.Canned]
		if !ok {
			return invalid(op, "unknown canned ACL %q", acl.Canned)
		}
		in.ACL = canned
	} else {
		if len(acl.Grants) == 0 {
			return invalid(op, "an ACL needs a canned name or at least one grant")
		}
		for _, g := range acl.Grants {
			perm, ok := bkPermissions[g.Permission]
			if !ok {
				return invalid(op, "unknown permission %q", g.Permission)
			}
			if g.Grantee == "" {
				return invalid(op, "a grant needs a grantee")
			}
			ge := &types.Grantee{Type: types.Type(g.Type)}
			switch g.Type {
			case "CanonicalUser":
				ge.ID = aws.String(g.Grantee)
			case "Group":
				ge.URI = aws.String(g.Grantee)
			case "AmazonCustomerByEmail":
				ge.EmailAddress = aws.String(g.Grantee)
			default:
				return invalid(op, "unknown grantee type %q", g.Type)
			}
			grants = append(grants, types.Grant{Grantee: ge, Permission: perm})
		}
	}
	c, err := s.bucketClient(ctx, op, loc)
	if err != nil {
		return err
	}
	if grants != nil {
		owner := acl.Owner
		if owner == "" {
			cur, err := s.ACL(ctx, loc, ref)
			if err != nil {
				return classify(op, err)
			}
			owner = cur.Owner
		}
		in.AccessControlPolicy = &types.AccessControlPolicy{Owner: &types.Owner{ID: aws.String(owner)}, Grants: grants}
	}
	_, err = c.S3().PutObjectAcl(ctx, in)
	return bkACLErr(op, err)
}
