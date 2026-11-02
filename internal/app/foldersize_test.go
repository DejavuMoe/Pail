package app

import (
	"context"
	"testing"
)

func TestFolderSizeCountsEverythingNested(t *testing.T) {
	e := newS3Env(t)
	e.srv.CreateBucket("b", false)
	objs := map[string]int{
		"a/":              0, // the folder's own placeholder
		"a/1.txt":         10,
		"a/sub/":          0,
		"a/sub/deep.bin":  300,
		"a/sub/x/y/z.txt": 7,
		"ab.txt":          1000, // shares the prefix "a" but is not below "a/"
		"b/other":         50,
		"a b/ü ñ+%.txt":   9, // keys the listing has to URL-decode
	}
	for k, n := range objs {
		e.srv.PutObject("b", k, make([]byte, n), "", nil)
	}
	bkSetPage(t, 2)
	var steps []Estimate
	est, err := e.s.FolderSize(testCtx(t), e.loc("b", "a/"), func(p Estimate) { steps = append(steps, p) })
	if err != nil {
		t.Fatal(err)
	}
	if est != (Estimate{Objects: 5, Bytes: 317}) {
		t.Fatalf("a/: %+v", est)
	}
	// A running total after each page of two keys, ending with the result.
	if len(steps) != 3 || steps[0].Objects != 2 || steps[len(steps)-1] != est {
		t.Fatalf("progress %+v", steps)
	}
	if est, err := e.s.FolderSize(testCtx(t), e.loc("b", "a b/"), nil); err != nil || est != (Estimate{Objects: 1, Bytes: 9}) {
		t.Fatalf("encoded: %+v %v", est, err)
	}
	if est, err := e.s.FolderSize(testCtx(t), e.loc("b", ""), nil); err != nil || est.Objects != len(objs) || est.Bytes != 1376 {
		t.Fatalf("bucket: %+v %v", est, err)
	}
	if est, err := e.s.FolderSize(testCtx(t), e.loc("b", "none/"), nil); err != nil || est != (Estimate{}) {
		t.Fatalf("empty: %+v %v", est, err)
	}
}

func TestFolderSizeErrors(t *testing.T) {
	e := newS3Env(t)
	e.srv.CreateBucket("b", false)
	e.srv.PutObject("b", "a/1", []byte("x"), "", nil)
	if _, err := e.s.FolderSize(testCtx(t), e.loc("b", "a"), nil); !IsKind(err, ErrInvalid) {
		t.Fatalf("prefix without slash: %v", err)
	}
	if _, err := e.s.FolderSize(testCtx(t), e.loc("nope", "a/"), nil); !IsKind(err, ErrNotFound) {
		t.Fatalf("missing bucket: %v", err)
	}
	ctx, cancel := context.WithCancel(testCtx(t))
	cancel()
	if _, err := e.s.FolderSize(ctx, e.loc("b", "a/"), nil); !IsKind(err, ErrCanceled) {
		t.Fatalf("canceled: %v", err)
	}
}
