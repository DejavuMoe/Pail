package app

import (
	"maps"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/DejavuMoe/Pail/internal/s3test"
)

func TestMoveIdenticalBodyKeepsDifferentAttributes(t *testing.T) {
	for name, change := range map[string]func(*s3.PutObjectInput){
		"metadata":            func(in *s3.PutObjectInput) { in.Metadata = map[string]string{"only-source": "valuable"} },
		"content type":        func(in *s3.PutObjectInput) { in.ContentType = aws.String("text/source") },
		"content encoding":    func(in *s3.PutObjectInput) { in.ContentEncoding = aws.String("custom") },
		"content disposition": func(in *s3.PutObjectInput) { in.ContentDisposition = aws.String("attachment") },
		"content language":    func(in *s3.PutObjectInput) { in.ContentLanguage = aws.String("zh") },
		"cache control":       func(in *s3.PutObjectInput) { in.CacheControl = aws.String("no-cache") },
		"expires":             func(in *s3.PutObjectInput) { in.Expires = aws.Time(syBase.AddDate(10, 0, 0)) },
		"storage class":       func(in *s3.PutObjectInput) { in.StorageClass = types.StorageClassStandardIa },
		"tags":                func(in *s3.PutObjectInput) { in.Tagging = aws.String("only-source=valuable") },
	} {
		t.Run(name, func(t *testing.T) {
			e := newS3Env(t)
			e.srv.CreateBucket("bkt", false)
			in := &s3.PutObjectInput{Bucket: aws.String("bkt"), Key: aws.String("a.txt"), Body: strings.NewReader("same"), ContentType: aws.String("text/plain")}
			change(in)
			if _, err := e.sdk().PutObject(testCtx(t), in); err != nil {
				t.Fatal(err)
			}
			e.srv.PutObject("bkt", "d/a.txt", []byte("same"), "text/plain", nil)
			if name == "tags" {
				if _, err := e.sdk().PutObjectTagging(testCtx(t), &s3.PutObjectTaggingInput{Bucket: aws.String("bkt"), Key: aws.String("d/a.txt"), Tagging: &types.Tagging{TagSet: []types.Tag{{Key: aws.String("only-source"), Value: aws.String("other")}}}}); err != nil {
					t.Fatal(err)
				}
			}
			before := bkHeadInfo(t, e, "bkt", "a.txt")
			id, err := e.s.Copy(Selection{Location: e.loc("bkt", ""), Keys: []string{"a.txt"}}, e.loc("bkt", "d/"), CopyOptions{Move: true, Conflict: ConflictSkip})
			if err != nil {
				t.Fatal(err)
			}
			bkWait(t, e, id)
			if it := bkItems(e, id)["a.txt"]; it.Status != ItemSkipped {
				t.Errorf("different attributes must conflict, got %+v", it)
			}
			bkWantData(t, e, "bkt", "a.txt", "same")
			after := bkHeadInfo(t, e, "bkt", "a.txt")
			if !maps.Equal(before.Metadata, after.Metadata) || !maps.Equal(before.Tags, after.Tags) || before.ContentType != after.ContentType {
				t.Fatalf("source properties changed: before %+v, after %+v", before, after)
			}
			bkWantData(t, e, "bkt", "d/a.txt", "same")
		})
	}
}

func TestMoveKeepsSourceWhenAttributesChangeAfterCopy(t *testing.T) {
	for _, changed := range []string{"source", "destination"} {
		t.Run(changed, func(t *testing.T) {
			e := newS3Env(t)
			bkSeed(e, "bkt", false, "a.txt")
			var heads atomic.Int32
			e.srv.Inject(func(r *http.Request) *s3test.Fault {
				if r.Method == http.MethodHead && r.URL.Path == "/bkt/d/a.txt" && heads.Add(1) == 2 {
					key := "a.txt"
					if changed == "destination" {
						key = "d/a.txt"
					}
					e.srv.PutObject("bkt", key, []byte("data:a.txt"), "text/plain", map[string]string{"changed": "valuable"})
				}
				return nil
			})
			id, err := e.s.Copy(Selection{Location: e.loc("bkt", ""), Keys: []string{"a.txt"}}, e.loc("bkt", "d/"), CopyOptions{Move: true})
			if err != nil {
				t.Fatal(err)
			}
			bkWait(t, e, id)
			if it := bkItems(e, id)["a.txt"]; it.Status != ItemFailed {
				t.Errorf("attribute change must keep source, got %+v", it)
			}
			bkWantData(t, e, "bkt", "a.txt", "data:a.txt")
		})
	}
}

func TestMoveRetryKeepsSourceWhenAttributesChanged(t *testing.T) {
	e := newS3Env(t)
	bkSeed(e, "bkt", false, "a.txt")
	e.srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.Method == http.MethodDelete {
			return &s3test.Fault{Status: 403, Code: "AccessDenied", Message: "injected"}
		}
		return nil
	})
	id, err := e.s.Copy(Selection{Location: e.loc("bkt", ""), Keys: []string{"a.txt"}}, e.loc("bkt", "d/"), CopyOptions{Move: true})
	if err != nil {
		t.Fatal(err)
	}
	if tk := bkWait(t, e, id); tk.Status != StatusPartiallyFailed {
		t.Fatalf("first run: %+v", tk)
	}
	e.srv.Inject(nil)
	e.srv.PutObject("bkt", "a.txt", []byte("data:a.txt"), "text/plain", map[string]string{"new": "valuable"})
	rid, err := e.s.Retry(id)
	if err != nil {
		t.Fatal(err)
	}
	bkWait(t, e, rid)
	if it := bkItems(e, rid)["a.txt"]; it.Status == ItemSucceeded {
		t.Errorf("retry deleted an uncopied attribute: %+v", it)
	}
	bkWantData(t, e, "bkt", "a.txt", "data:a.txt")
}

func TestMoveIdenticalAttributesAndRetryPreserveObject(t *testing.T) {
	for _, retry := range []bool{false, true} {
		name := "existing copy"
		if retry {
			name = "delete retry"
		}
		t.Run(name, func(t *testing.T) {
			e := newS3Env(t)
			e.srv.CreateBucket("bkt", false)
			_, err := e.sdk().PutObject(testCtx(t), &s3.PutObjectInput{Bucket: aws.String("bkt"), Key: aws.String("a.txt"), Body: strings.NewReader("same"),
				ContentType: aws.String("text/source"), CacheControl: aws.String("no-cache"), Metadata: map[string]string{"source": "valuable"}, Tagging: aws.String("owner=me"), StorageClass: types.StorageClassStandardIa})
			if err != nil {
				t.Fatal(err)
			}
			if retry {
				e.srv.Inject(func(r *http.Request) *s3test.Fault {
					if r.Method == http.MethodDelete {
						return &s3test.Fault{Status: 403, Code: "AccessDenied", Message: "injected"}
					}
					return nil
				})
			} else if _, err := e.sdk().CopyObject(testCtx(t), &s3.CopyObjectInput{Bucket: aws.String("bkt"), Key: aws.String("d/a.txt"), CopySource: aws.String("bkt/a.txt"), StorageClass: types.StorageClassStandardIa}); err != nil {
				t.Fatal(err)
			}
			id, err := e.s.Copy(Selection{Location: e.loc("bkt", ""), Keys: []string{"a.txt"}}, e.loc("bkt", "d/"), CopyOptions{Move: true})
			if err != nil {
				t.Fatal(err)
			}
			if retry {
				if tk := bkWait(t, e, id); tk.Status != StatusPartiallyFailed {
					t.Fatalf("fixture must fail source deletion: %+v", tk)
				}
				e.srv.Inject(nil)
				id, err = e.s.Retry(id)
				if err != nil {
					t.Fatal(err)
				}
			}
			bkOK(t, e, id)
			bkWantKeys(t, bkLive(t, e, "bkt"), "d/a.txt")
			h := bkHeadInfo(t, e, "bkt", "d/a.txt")
			if h.ContentType != "text/source" || h.Metadata["source"] != "valuable" || h.Tags["owner"] != "me" || bkHeader(h, "Cache-Control") != "no-cache" || h.StorageClass != "STANDARD_IA" {
				t.Fatalf("moved properties differ: %+v", h)
			}
		})
	}
}

func TestMoveIdenticalObjectRequiresReadableTags(t *testing.T) {
	e := newS3Env(t)
	bkSeed(e, "bkt", false, "a.txt")
	e.srv.PutObject("bkt", "d/a.txt", []byte("data:a.txt"), "text/plain", nil)
	e.srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.URL.Query().Has("tagging") {
			return &s3test.Fault{Status: 403, Code: "AccessDenied", Message: "injected"}
		}
		return nil
	})
	id, err := e.s.Copy(Selection{Location: e.loc("bkt", ""), Keys: []string{"a.txt"}}, e.loc("bkt", "d/"), CopyOptions{Move: true})
	if err != nil {
		t.Fatal(err)
	}
	if tk := bkWait(t, e, id); tk.Status != StatusPartiallyFailed {
		t.Fatalf("unverifiable tags must not authorize deletion: %+v", tk)
	}
	bkWantData(t, e, "bkt", "a.txt", "data:a.txt")
}
