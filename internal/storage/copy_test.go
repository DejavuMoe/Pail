package storage

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

func TestLargeCopyRangesMetadataAndAbort(t *testing.T) {
	const size = int64(5*1024*1024*1024 + 1)
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			var mu sync.Mutex
			var ranges []string
			aborted, completed := false, false
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				q := r.URL.Query()
				w.Header().Set("Content-Type", "application/xml")
				switch {
				case r.Method == "HEAD":
					if r.URL.Path != "/source/目录/a +%?#" {
						t.Errorf("wrong source key %q", r.URL.Path)
					}
					w.Header().Set("Content-Length", fmt.Sprint(size))
					w.Header().Set("Content-Type", "text/plain")
					w.Header().Set("X-Amz-Meta-Custom", "preserved")
					w.Header().Set("ETag", `"source-etag"`)
					w.Header().Set("X-Amz-Version-Id", "pinned-version")
				case r.Method == "GET" && q.Has("tagging"):
					if q.Get("versionId") != "pinned-version" {
						t.Error("source tags not pinned")
					}
					fmt.Fprint(w, `<Tagging><TagSet><Tag><Key>hello</Key><Value>world +</Value></Tag></TagSet></Tagging>`)
				case r.Method == "POST" && q.Has("uploads"):
					if r.Header.Get("X-Amz-Meta-Custom") != "preserved" || r.Header.Get("Content-Type") != "text/plain" {
						t.Error("source metadata was not copied")
					}
					if r.Header.Get("X-Amz-Tagging") != "hello=world+%2B" {
						t.Errorf("tags not copied: %q", r.Header.Get("X-Amz-Tagging"))
					}
					if r.Header.Get("X-Amz-Server-Side-Encryption") != "AES256" || r.Header.Get("X-Amz-Storage-Class") != "STANDARD_IA" || r.Header.Get("X-Amz-Acl") != "private" {
						t.Error("destination options not retained")
					}
					fmt.Fprint(w, `<InitiateMultipartUploadResult><UploadId>copy-id</UploadId></InitiateMultipartUploadResult>`)
				case r.Method == "PUT" && q.Get("partNumber") != "":
					ranges = append(ranges, r.Header.Get("X-Amz-Copy-Source-Range"))
					if r.Header.Get("X-Amz-Copy-Source-If-Match") != `"source-etag"` {
						t.Error("source mutation protection missing")
					}
					if !strings.Contains(r.Header.Get("X-Amz-Copy-Source"), "versionId=pinned-version") {
						t.Error("copy source version not pinned")
					}
					if fail && q.Get("partNumber") == "2" {
						w.WriteHeader(403)
						fmt.Fprint(w, `<Error><Code>AccessDenied</Code><Message>denied</Message></Error>`)
						return
					}
					fmt.Fprint(w, `<CopyPartResult><ETag>part-etag</ETag></CopyPartResult>`)
				case r.Method == "POST" && q.Get("uploadId") == "copy-id":
					if r.Header.Get("If-None-Match") != "*" {
						t.Error("destination create-only condition lost")
					}
					completed = true
					b, _ := io.ReadAll(r.Body)
					if !strings.Contains(string(b), "<PartNumber>2</PartNumber>") {
						t.Error("completion missing second part")
					}
					fmt.Fprint(w, `<CompleteMultipartUploadResult><ETag>copy-etag</ETag></CompleteMultipartUploadResult>`)
				case r.Method == "DELETE" && q.Get("uploadId") == "copy-id":
					aborted = true
					w.WriteHeader(204)
				default:
					t.Errorf("unexpected copy request: %s %s", r.Method, r.URL)
				}
			})
			err := c.Copy(context.Background(), &s3.CopyObjectInput{IfNoneMatch: aws.String("*"), Bucket: aws.String("target"), Key: aws.String("copy"), CopySource: aws.String(CopySource("source", "目录/a +%?#", "")), ACL: types.ObjectCannedACLPrivate, StorageClass: types.StorageClassStandardIa, ServerSideEncryption: types.ServerSideEncryptionAes256}, size, TransferOptions{PartSize: 5 << 30, Concurrency: 1})
			if len(ranges) != 2 || ranges[0] != "bytes=0-5368709119" || ranges[1] != "bytes=5368709120-5368709120" {
				t.Fatalf("incorrect ranges: %v", ranges)
			}
			if fail {
				if err == nil || !aborted || completed {
					t.Fatalf("failed copy cleanup incorrect: %v abort=%v complete=%v", err, aborted, completed)
				}
			} else if err != nil || aborted || !completed {
				t.Fatalf("copy did not finish: %v abort=%v complete=%v", err, aborted, completed)
			}
		})
	}
}

func TestSmallCopyUsesCopyObject(t *testing.T) {
	var progress int64
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" || r.Header.Get("X-Amz-Copy-Source") == "" || r.URL.Query().Has("uploadId") {
			t.Errorf("unexpected %s %s", r.Method, r.URL)
		}
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, `<CopyObjectResult><ETag>"e"</ETag></CopyObjectResult>`)
	})
	err := c.Copy(context.Background(), &s3.CopyObjectInput{Bucket: aws.String("b"), Key: aws.String("k"), CopySource: aws.String("b/src")}, 10, TransferOptions{Progress: func(n int64) { progress = n }})
	if err != nil || progress != 10 {
		t.Fatalf("%v %d", err, progress)
	}
}

// Providers without tagging support (not implemented) must not fail a multipart copy.
func TestCopyTaggingIsBestEffort(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		code   string
	}{{"not-implemented", 501, "NotImplemented"}, {"x-not-implemented", 501, "XNotImplemented"}, {"unsupported-operation", 400, "UnsupportedOperation"}} {
		t.Run(tc.name, func(t *testing.T) {
			const size = 5*1024*1024*1024 + 1
			completed := false
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				w.Header().Set("Content-Type", "application/xml")
				switch {
				case r.Method == "HEAD":
					w.Header().Set("Content-Length", fmt.Sprint(size))
					w.Header().Set("ETag", `"etag"`)
				case r.Method == "GET" && q.Has("tagging"):
					w.WriteHeader(tc.status)
					fmt.Fprintf(w, `<Error><Code>%s</Code><Message>no tagging</Message></Error>`, tc.code)
				case r.Method == "POST" && q.Has("uploads"):
					if r.Header.Get("X-Amz-Tagging") != "" {
						t.Error("tags sent although unreadable")
					}
					fmt.Fprint(w, `<InitiateMultipartUploadResult><UploadId>id</UploadId></InitiateMultipartUploadResult>`)
				case r.Method == "PUT":
					fmt.Fprint(w, `<CopyPartResult><ETag>p</ETag></CopyPartResult>`)
				case r.Method == "POST":
					completed = true
					fmt.Fprint(w, `<CompleteMultipartUploadResult><ETag>x</ETag></CompleteMultipartUploadResult>`)
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL)
				}
			})
			err := c.Copy(context.Background(), &s3.CopyObjectInput{Bucket: aws.String("b"), Key: aws.String("k"), CopySource: aws.String("b/s")}, size, TransferOptions{PartSize: 5 << 30, Concurrency: 1})
			if err != nil || !completed {
				t.Fatalf("copy failed without tagging support: %v completed=%v", err, completed)
			}
		})
	}
}

func TestCopyAbortsWhenSourceTagsChange(t *testing.T) {
	const size = 5*1024*1024*1024 + 1
	var tags atomic.Int32
	var aborted atomic.Bool
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		w.Header().Set("Content-Type", "application/xml")
		switch {
		case r.Method == "HEAD":
			w.Header().Set("Content-Length", fmt.Sprint(size))
			w.Header().Set("ETag", `"etag"`)
		case r.Method == "GET" && q.Has("tagging"):
			fmt.Fprintf(w, `<Tagging><TagSet><Tag><Key>revision</Key><Value>%d</Value></Tag></TagSet></Tagging>`, tags.Add(1))
		case r.Method == "POST" && q.Has("uploads"):
			fmt.Fprint(w, `<InitiateMultipartUploadResult><UploadId>tag-id</UploadId></InitiateMultipartUploadResult>`)
		case r.Method == "PUT":
			fmt.Fprint(w, `<CopyPartResult><ETag>part-etag</ETag></CopyPartResult>`)
		case r.Method == "DELETE":
			aborted.Store(true)
			w.WriteHeader(204)
		default:
			t.Errorf("changed tags should prevent completion: %s %s", r.Method, r.URL)
		}
	})
	err := c.Copy(context.Background(), &s3.CopyObjectInput{Bucket: aws.String("b"), Key: aws.String("k"), CopySource: aws.String("b/s")}, size, TransferOptions{PartSize: 5 << 30, Concurrency: 1})
	if err == nil || !strings.Contains(err.Error(), "tags changed") || !aborted.Load() {
		t.Fatalf("tag race not detected/aborted: %v", err)
	}
}

func TestZeroByteMultipartCopy(t *testing.T) {
	var emptyParts, completed int
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		w.Header().Set("Content-Type", "application/xml")
		switch {
		case r.Method == "HEAD":
			w.Header().Set("Content-Length", "0")
			w.Header().Set("ETag", `"empty"`)
		case r.Method == "GET" && q.Has("tagging"):
			fmt.Fprint(w, `<Tagging><TagSet/></Tagging>`)
		case r.Method == "POST" && q.Has("uploads"):
			fmt.Fprint(w, `<InitiateMultipartUploadResult><UploadId>z</UploadId></InitiateMultipartUploadResult>`)
		case r.Method == "PUT" && q.Get("partNumber") == "1":
			if r.Header.Get("X-Amz-Copy-Source") != "" {
				t.Error("zero-byte source must not use UploadPartCopy")
			}
			emptyParts++
			w.Header().Set("ETag", `"p"`)
		case r.Method == "POST":
			completed++
			fmt.Fprint(w, `<CompleteMultipartUploadResult><ETag>x</ETag></CompleteMultipartUploadResult>`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
		}
	})
	// A destination condition forces the multipart path even for tiny objects.
	err := c.Copy(context.Background(), &s3.CopyObjectInput{Bucket: aws.String("b"), Key: aws.String("k"), CopySource: aws.String("b/s"), IfNoneMatch: aws.String("*")}, 0, TransferOptions{})
	if err != nil || emptyParts != 1 || completed != 1 {
		t.Fatalf("%v parts=%d completed=%d", err, emptyParts, completed)
	}
}

func TestCopyPartSizeScalesPast10kParts(t *testing.T) {
	got, err := copyPartSize(1<<40, 0) // 1 TiB / 64 MiB = 16384 parts
	if err != nil || (1<<40+got-1)/got > 10000 || got <= 64<<20 {
		t.Fatalf("part size %d err %v", got, err)
	}
	if got, _ = copyPartSize(100, 0); got != 64<<20 {
		t.Fatalf("small copy part size %d", got)
	}
	if _, err = copyPartSize(5<<40, 0); err != nil {
		t.Fatalf("5 TiB should fit: %v", err)
	}
}

func TestCopyRejectsInvalidWithoutNetwork(t *testing.T) {
	c := &Client{}
	in := func() *s3.CopyObjectInput {
		return &s3.CopyObjectInput{Bucket: aws.String("b"), Key: aws.String("k"), CopySource: aws.String("b/s")}
	}
	if err := c.Copy(context.Background(), in(), 5<<40+1, TransferOptions{}); err == nil {
		t.Fatal("size over 5 TiB accepted")
	}
	if err := c.Copy(context.Background(), in(), -1, TransferOptions{}); err == nil {
		t.Fatal("negative size accepted")
	}
	if err := c.Copy(context.Background(), &s3.CopyObjectInput{}, 1, TransferOptions{}); err == nil {
		t.Fatal("empty input accepted")
	}
	bad := in()
	bad.MetadataDirective = "MERGE"
	if err := c.Copy(context.Background(), bad, 6<<30, TransferOptions{}); err == nil {
		t.Fatal("bad directive accepted")
	}
}

// Tags that cannot be read for any reason other than "unsupported" must fail
// the copy before an upload is created: the caller (Move) would otherwise
// delete a source whose tags were not carried over.
func TestFxCopyFailsWhenSourceTagsUnreadable(t *testing.T) {
	const size = 5*1024*1024*1024 + 1
	for _, tc := range []struct {
		name   string
		status int
		code   string
	}{{"access-denied", 403, "AccessDenied"}, {"internal-error-400", 400, "InvalidRequest"}, {"no-such-key", 404, "NoSuchKey"}} {
		t.Run(tc.name, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				w.Header().Set("Content-Type", "application/xml")
				switch {
				case r.Method == "HEAD":
					w.Header().Set("Content-Length", fmt.Sprint(size))
					w.Header().Set("ETag", `"etag"`)
				case r.Method == "GET" && q.Has("tagging"):
					w.WriteHeader(tc.status)
					fmt.Fprintf(w, `<Error><Code>%s</Code><Message>no tagging</Message></Error>`, tc.code)
				default:
					t.Errorf("nothing but the tag read may follow: %s %s", r.Method, r.URL)
				}
			})
			err := c.Copy(context.Background(), &s3.CopyObjectInput{Bucket: aws.String("b"), Key: aws.String("k"), CopySource: aws.String("b/s")}, size, TransferOptions{PartSize: 5 << 30, Concurrency: 1})
			if err == nil || !strings.Contains(err.Error(), "cannot read source tags: "+tc.code) {
				t.Fatalf("want a clear tag error naming %s, got %v", tc.code, err)
			}
			if ErrorCode(err) != tc.code {
				t.Fatalf("ErrorCode = %q, the service's code must stay visible", ErrorCode(err))
			}
		})
	}
}

// Tags readable at first but denied at the final recheck also fail (and abort).
func TestFxCopyFailsWhenTagRecheckDenied(t *testing.T) {
	const size = 5*1024*1024*1024 + 1
	var gets atomic.Int32
	var aborted, completed atomic.Bool
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		w.Header().Set("Content-Type", "application/xml")
		switch {
		case r.Method == "HEAD":
			w.Header().Set("Content-Length", fmt.Sprint(size))
			w.Header().Set("ETag", `"etag"`)
		case r.Method == "GET" && q.Has("tagging"):
			if gets.Add(1) == 1 {
				fmt.Fprint(w, `<Tagging><TagSet><Tag><Key>k</Key><Value>v</Value></Tag></TagSet></Tagging>`)
				return
			}
			w.WriteHeader(403)
			fmt.Fprint(w, `<Error><Code>AccessDenied</Code><Message>no</Message></Error>`)
		case r.Method == "POST" && q.Has("uploads"):
			fmt.Fprint(w, `<InitiateMultipartUploadResult><UploadId>id</UploadId></InitiateMultipartUploadResult>`)
		case r.Method == "PUT":
			fmt.Fprint(w, `<CopyPartResult><ETag>p</ETag></CopyPartResult>`)
		case r.Method == "DELETE":
			aborted.Store(true)
			w.WriteHeader(204)
		case r.Method == "POST":
			completed.Store(true)
			fmt.Fprint(w, `<CompleteMultipartUploadResult><ETag>x</ETag></CompleteMultipartUploadResult>`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
		}
	})
	err := c.Copy(context.Background(), &s3.CopyObjectInput{Bucket: aws.String("b"), Key: aws.String("k"), CopySource: aws.String("b/s")}, size, TransferOptions{PartSize: 5 << 30, Concurrency: 1})
	if err == nil || !strings.Contains(err.Error(), "AccessDenied") || completed.Load() || !aborted.Load() {
		t.Fatalf("err=%v completed=%v aborted=%v", err, completed.Load(), aborted.Load())
	}
}
