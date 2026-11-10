package app

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/DejavuMoe/Pail/internal/s3test"
)

// A move whose copy succeeded but whose source delete failed must finish when
// it is retried: the destination that is already there is the copy, not a
// conflict.
func TestMoveRetryAfterSourceDeleteFailureFinishesTheMove(t *testing.T) {
	failDelete := func(e *s3Env, on *atomic.Bool) {
		e.srv.Inject(func(r *http.Request) *s3test.Fault {
			if on.Load() && r.Method == http.MethodDelete {
				return &s3test.Fault{Status: 503, Code: "SlowDown", Message: "injected"}
			}
			return nil
		})
	}
	for _, policy := range []ConflictPolicy{ConflictSkip, ConflictRename, ConflictNewer, ConflictDifferentSize} {
		t.Run(string(policy), func(t *testing.T) {
			e := newS3Env(t)
			bkSeed(e, "bkt", false, "a.txt")
			var on atomic.Bool
			on.Store(true)
			failDelete(e, &on)
			id, err := e.s.Copy(Selection{Location: e.loc("bkt", ""), Keys: []string{"a.txt"}}, e.loc("bkt", "d/"), CopyOptions{Move: true, Conflict: policy})
			if err != nil {
				t.Fatal(err)
			}
			if tk := bkWait(t, e, id); tk.Status != StatusPartiallyFailed {
				t.Fatalf("first run: %v %+v", tk.Status, e.s.TaskItems(id))
			}
			on.Store(false)
			rid, err := e.s.Retry(id)
			if err != nil {
				t.Fatal(err)
			}
			bkOK(t, e, rid)
			if it := bkItems(e, rid)["a.txt"]; it.Status != ItemSucceeded {
				t.Fatalf("retried item %+v", it)
			}
			if _, ok := e.srv.Object("bkt", "a.txt"); ok {
				t.Fatal("the source is still there after the retried move")
			}
			bkWantKeys(t, bkLive(t, e, "bkt"), "d/a.txt")
		})
	}
}

func TestRenameRetryAfterSourceDeleteFailureFinishesTheRename(t *testing.T) {
	e := newS3Env(t)
	bkSeed(e, "bkt", false, "docs/old.txt")
	var on atomic.Bool
	on.Store(true)
	e.srv.Inject(func(r *http.Request) *s3test.Fault {
		if on.Load() && r.Method == http.MethodDelete {
			return &s3test.Fault{Status: 503, Code: "SlowDown", Message: "injected"}
		}
		return nil
	})
	id, err := e.s.Rename(testCtx(t), e.loc("bkt", "docs/"), "docs/old.txt", "new.txt")
	if err != nil {
		t.Fatal(err)
	}
	if tk := bkWait(t, e, id); tk.Status != StatusPartiallyFailed {
		t.Fatalf("first run: %v", tk.Status)
	}
	on.Store(false)
	rid, err := e.s.Retry(id)
	if err != nil {
		t.Fatal(err)
	}
	bkOK(t, e, rid)
	bkWantKeys(t, bkLive(t, e, "bkt"), "docs/new.txt")
}

// A destination that is byte for byte the source (same size, same content
// ETag), e.g. left by an earlier move of another session, is the copy already.
func TestMoveOntoAnIdenticalObjectFinishesTheMove(t *testing.T) {
	e := newS3Env(t)
	bkSeed(e, "bkt", false, "a.txt")
	e.srv.PutObject("bkt", "d/a.txt", []byte("data:a.txt"), "text/plain", nil)
	id, err := e.s.Copy(Selection{Location: e.loc("bkt", ""), Keys: []string{"a.txt"}}, e.loc("bkt", "d/"), CopyOptions{Move: true, Conflict: ConflictSkip})
	if err != nil {
		t.Fatal(err)
	}
	bkOK(t, e, id)
	bkWantKeys(t, bkLive(t, e, "bkt"), "d/a.txt")
}

// An existing destination that only has the source's name is still a conflict:
// the retry shortcut needs the same size and the same ETag.
func TestMoveOntoADifferentObjectStillConflicts(t *testing.T) {
	e := newS3Env(t)
	bkSeed(e, "bkt", false, "a.txt")
	e.srv.PutObject("bkt", "d/a.txt", []byte("other content"), "text/plain", nil)
	id, err := e.s.Copy(Selection{Location: e.loc("bkt", ""), Keys: []string{"a.txt"}}, e.loc("bkt", "d/"), CopyOptions{Move: true, Conflict: ConflictSkip})
	if err != nil {
		t.Fatal(err)
	}
	bkWait(t, e, id)
	if it := bkItems(e, id)["a.txt"]; it.Status != ItemSkipped {
		t.Fatalf("item %+v", it)
	}
	bkWantData(t, e, "bkt", "a.txt", "data:a.txt")
	bkWantData(t, e, "bkt", "d/a.txt", "other content")

	// Same size, different content: also a conflict.
	e.srv.PutObject("bkt", "d/a.txt", []byte("data:a.txx"), "text/plain", nil)
	id, _ = e.s.Copy(Selection{Location: e.loc("bkt", ""), Keys: []string{"a.txt"}}, e.loc("bkt", "d/"), CopyOptions{Move: true, Conflict: ConflictSkip})
	bkWait(t, e, id)
	if it := bkItems(e, id)["a.txt"]; it.Status != ItemSkipped {
		t.Fatalf("same size item %+v", it)
	}
	bkWantData(t, e, "bkt", "a.txt", "data:a.txt")
}

// The check before the delete compares the ETag as well as the size: a
// destination that was replaced by another object of the same size is not the
// copy, so the source stays.
func TestMoveKeepsSourceWhenDestinationETagDiffersAfterCopy(t *testing.T) {
	e := newS3Env(t)
	bkSeed(e, "bkt", false, "m.txt")
	var destHeads, deletes atomic.Int32
	e.srv.Inject(func(r *http.Request) *s3test.Fault {
		switch {
		case r.Method == http.MethodHead && r.URL.Path == "/bkt/dst/m.txt":
			// HEAD 1 is the conflict check; HEAD 2 verifies the copy: another
			// writer replaced the destination with the same number of bytes.
			if destHeads.Add(1) == 2 {
				e.srv.PutObject("bkt", "dst/m.txt", []byte("data:m.txX"), "text/plain", nil)
			}
		case r.Method == http.MethodDelete && r.URL.Path == "/bkt/m.txt":
			deletes.Add(1)
		}
		return nil
	})
	id, err := e.s.Copy(Selection{Location: e.loc("bkt", ""), Keys: []string{"m.txt"}}, e.loc("bkt", "dst/"), CopyOptions{Move: true})
	if err != nil {
		t.Fatal(err)
	}
	tk := bkWait(t, e, id)
	it := bkItems(e, id)["m.txt"]
	if tk.Status != StatusPartiallyFailed || it.Status != ItemFailed || !strings.Contains(it.Err, "does not match the source") {
		t.Fatalf("task %v item %+v", tk.Status, it)
	}
	if deletes.Load() != 0 {
		t.Fatal("the source was deleted although the destination had other content")
	}
	bkWantData(t, e, "bkt", "m.txt", "data:m.txt")
}

func TestETagComparableRules(t *testing.T) {
	for name, c := range map[string]struct {
		a      string
		aKeyed bool
		b      string
		bKeyed bool
		want   bool
	}{
		"equal":            {`"abc"`, false, `"abc"`, false, true},
		"quotes ignored":   {`"abc"`, false, `abc`, false, true},
		"different":        {`"abc"`, false, `"abd"`, false, false},
		"multipart source": {`"abc-3"`, false, `"abc-3"`, false, false},
		"multipart dest":   {`"abc"`, false, `"abc-1"`, false, false},
		"kms source":       {`"abc"`, true, `"abc"`, false, false},
		"kms dest":         {`"abc"`, false, `"abc"`, true, false},
		"empty":            {``, false, ``, false, false},
	} {
		if got := sameObjectETag(c.a, c.aKeyed, c.b, c.bKeyed); got != c.want {
			t.Errorf("%s: got %v want %v", name, got, c.want)
		}
	}
}
