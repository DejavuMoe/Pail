package storage

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

func ids(n int) []ObjectID {
	out := make([]ObjectID, n)
	for i := range out {
		out[i] = ObjectID{Key: fmt.Sprintf("key-%03d", i/3), VersionID: fmt.Sprintf("v-%04d", i)}
	}
	return out
}

func counts(rs []DeleteResult) map[DeleteStatus]int {
	m := map[DeleteStatus]int{}
	for _, r := range rs {
		m[r.Status]++
	}
	return m
}

func assertCounts(t *testing.T, rs []DeleteResult, ok, failed, unknown, unprocessed int) {
	t.Helper()
	m := counts(rs)
	if m[DeleteSucceeded] != ok || m[DeleteFailed] != failed || m[DeleteUnknown] != unknown || m[DeleteUnprocessed] != unprocessed {
		t.Fatalf("counts=%v want %d/%d/%d/%d", m, ok, failed, unknown, unprocessed)
	}
}

type xmlItem struct {
	Key       string `xml:"Key"`
	VersionID string `xml:"VersionId"`
	Code      string `xml:"Code,omitempty"`
	Message   string `xml:"Message,omitempty"`
}

func TestDeleteObjectsProtocolChunksAndOrder(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprint(partial), func(t *testing.T) {
			sel := ids(1005)
			var calls, done atomic.Int32
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				offset := int(done.Load())
				if r.Method != "POST" || r.URL.Path != "/bucket" || !r.URL.Query().Has("delete") {
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
					w.WriteHeader(400)
					return
				}
				var body struct {
					Objects []xmlItem `xml:"Object"`
					Quiet   *bool     `xml:"Quiet"`
				}
				if err := xml.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				if want := min(1000, len(sel)-offset); len(body.Objects) != want || body.Quiet == nil || *body.Quiet {
					t.Errorf("batch objects=%d quiet=%v want=%d", len(body.Objects), body.Quiet, want)
				}
				var resp struct {
					XMLName xml.Name  `xml:"DeleteResult"`
					Deleted []xmlItem `xml:"Deleted"`
					Errors  []xmlItem `xml:"Error"`
				}
				for i := len(body.Objects) - 1; i >= 0; i-- { // acknowledgements in reverse order
					v := body.Objects[i]
					if v.Key != sel[offset+i].Key || v.VersionID != sel[offset+i].VersionID {
						t.Errorf("selection changed at %d: %+v", offset+i, v)
					}
					if partial && (offset+i == 7 || offset+i == 998 || offset+i == 1002) {
						v.Code, v.Message = "AccessDenied", fmt.Sprintf("failure-%d", offset+i)
						resp.Errors = append(resp.Errors, v)
					} else {
						resp.Deleted = append(resp.Deleted, v)
					}
				}
				done.Add(int32(len(body.Objects)))
				w.Header().Set("Content-Type", "application/xml")
				_ = xml.NewEncoder(w).Encode(resp)
			})
			res := c.DeleteObjects(context.Background(), "bucket", sel)
			if calls.Load() != 2 || done.Load() != 1005 || len(res) != 1005 {
				t.Fatalf("calls=%d objects=%d results=%d", calls.Load(), done.Load(), len(res))
			}
			fails := 0
			if partial {
				fails = 3
			}
			assertCounts(t, res, 1005-fails, fails, 0, 0)
			for i, r := range res {
				if r.Key != sel[i].Key || r.VersionID != sel[i].VersionID {
					t.Fatalf("item %d lost identity/order", i)
				}
				if partial && (i == 7 || i == 998 || i == 1002) && (r.Status != DeleteFailed || r.Code != "AccessDenied" || r.Message != fmt.Sprintf("failure-%d", i)) {
					t.Fatalf("failure %d not reported: %+v", i, r)
				}
			}
		})
	}
}

func TestDeleteObjectsBatchBoundaries(t *testing.T) {
	for _, n := range []int{0, 1, 1000, 1001, 2001} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			var calls atomic.Int32
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var body struct {
					Objects []xmlItem `xml:"Object"`
				}
				_ = xml.NewDecoder(r.Body).Decode(&body)
				var resp struct {
					XMLName xml.Name  `xml:"DeleteResult"`
					Deleted []xmlItem `xml:"Deleted"`
				}
				resp.Deleted = body.Objects
				_ = xml.NewEncoder(w).Encode(resp)
			})
			res := c.DeleteObjects(context.Background(), "bucket", ids(n))
			assertCounts(t, res, n, 0, 0, 0)
			if want := (n + 999) / 1000; int(calls.Load()) != want {
				t.Fatalf("calls=%d want %d", calls.Load(), want)
			}
		})
	}
}

func TestDeleteObjectsInvalidAndDuplicateInput(t *testing.T) {
	var sent []xmlItem
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Objects []xmlItem `xml:"Object"`
		}
		_ = xml.NewDecoder(r.Body).Decode(&body)
		sent = body.Objects
		fmt.Fprint(w, `<DeleteResult><Deleted><Key>a</Key><VersionId>1</VersionId></Deleted></DeleteResult>`)
	})
	res := c.DeleteObjects(context.Background(), "bucket", []ObjectID{{"a", "1"}, {"", "x"}, {"a", "1"}})
	if len(sent) != 1 {
		t.Fatalf("duplicates or empty keys were sent: %+v", sent)
	}
	if res[0].Status != DeleteSucceeded || res[2].Status != DeleteSucceeded || res[1].Status != DeleteFailed || res[1].Code != "InvalidArgument" {
		t.Fatalf("%+v", res)
	}
	assertCounts(t, (&Client{}).DeleteObjects(context.Background(), "", ids(2)), 0, 2, 0, 0)
	if r := (&Client{}).DeleteObjects(context.Background(), "b", nil); len(r) != 0 {
		t.Fatal("empty input produced results")
	}
}

func TestDeleteObjectsCancellation(t *testing.T) {
	t.Run("before", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		assertCounts(t, (&Client{}).DeleteObjects(ctx, "b", ids(2005)), 0, 0, 0, 2005)
	})
	t.Run("during", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var calls atomic.Int32
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			n := calls.Add(1)
			var body struct {
				Objects []xmlItem `xml:"Object"`
			}
			_ = xml.NewDecoder(r.Body).Decode(&body)
			if n == 2 {
				cancel()
				return
			}
			var resp struct {
				XMLName xml.Name  `xml:"DeleteResult"`
				Deleted []xmlItem `xml:"Deleted"`
			}
			resp.Deleted = body.Objects
			_ = xml.NewEncoder(w).Encode(resp)
		})
		res := c.DeleteObjects(ctx, "b", ids(3005))
		assertCounts(t, res, 1000, 0, 1000, 1005)
		if res[1500].Status != DeleteUnknown || res[2500].Status != DeleteUnprocessed {
			t.Fatalf("wrong chunk statuses: %v %v", res[1500].Status, res[2500].Status)
		}
	})
}

// A chunk the service refused with a 4xx is definitively failed (nothing was
// deleted); later chunks are still attempted when not cancelled.
func TestDeleteObjectsChunkErrorContinues(t *testing.T) {
	var calls atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Objects []xmlItem `xml:"Object"`
		}
		_ = xml.NewDecoder(r.Body).Decode(&body)
		if calls.Add(1) == 1 {
			w.WriteHeader(403)
			fmt.Fprint(w, `<Error><Code>AccessDenied</Code><Message>no</Message></Error>`)
			return
		}
		var resp struct {
			XMLName xml.Name  `xml:"DeleteResult"`
			Deleted []xmlItem `xml:"Deleted"`
		}
		resp.Deleted = body.Objects
		_ = xml.NewEncoder(w).Encode(resp)
	})
	res := c.DeleteObjects(context.Background(), "b", ids(1500))
	assertCounts(t, res, 500, 1000, 0, 0)
	if res[0].Status != DeleteFailed || res[0].Code != "AccessDenied" || !strings.Contains(res[0].Message, "no") {
		t.Fatalf("%+v", res[0])
	}
}

// Transient failures use the standard SDK retryer (3 attempts) for DeleteObjects too.
func TestDeleteObjectsRetriesTransientErrors(t *testing.T) {
	var calls atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if calls.Add(1) < 3 {
			w.WriteHeader(503)
			fmt.Fprint(w, `<Error><Code>SlowDown</Code><Message>retry</Message></Error>`)
			return
		}
		fmt.Fprint(w, `<DeleteResult><Deleted><Key>key</Key><VersionId>v</VersionId></Deleted></DeleteResult>`)
	})
	res := c.DeleteObjects(context.Background(), "b", []ObjectID{{"key", "v"}})
	assertCounts(t, res, 1, 0, 0, 0)
	if calls.Load() != 3 {
		t.Fatalf("attempts=%d, want 3", calls.Load())
	}
	calls.Store(-100)
	c = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(503)
		fmt.Fprint(w, `<Error><Code>SlowDown</Code></Error>`)
	})
	calls.Store(0)
	assertCounts(t, c.DeleteObjects(context.Background(), "b", ids(1)), 0, 0, 1, 0)
	if calls.Load() != 3 {
		t.Fatalf("persistent failure attempts=%d, want 3", calls.Load())
	}
}

func TestReconcileMalformedResponses(t *testing.T) {
	for _, mode := range []string{"nil", "missing", "duplicate-success", "duplicate-error", "contradictory", "wrong-version", "missing-version", "unexpected"} {
		t.Run(mode, func(t *testing.T) {
			sel := ids(5)
			chunk := make([]DeleteResult, len(sel))
			index := map[ObjectID][]int{}
			out := &s3.DeleteObjectsOutput{}
			for i, id := range sel {
				chunk[i] = DeleteResult{Key: id.Key, VersionID: id.VersionID}
				index[id] = []int{i}
				out.Deleted = append(out.Deleted, types.DeletedObject{Key: aws.String(id.Key), VersionId: aws.String(id.VersionID)})
			}
			first := out.Deleted[0]
			switch mode {
			case "nil":
				out = nil
			case "missing":
				out.Deleted = out.Deleted[1:]
			case "duplicate-success":
				out.Deleted = append(out.Deleted, first)
			case "duplicate-error":
				out.Deleted = out.Deleted[1:]
				e := types.Error{Key: first.Key, VersionId: first.VersionId, Code: aws.String("AccessDenied")}
				out.Errors = []types.Error{e, e}
			case "contradictory":
				out.Errors = []types.Error{{Key: first.Key, VersionId: first.VersionId, Code: aws.String("AccessDenied")}}
			case "wrong-version":
				out.Deleted[0].VersionId = aws.String("not-selected")
			case "missing-version":
				out.Deleted[0].VersionId = nil
			case "unexpected":
				out.Deleted = append(out.Deleted, types.DeletedObject{Key: aws.String("unselected"), VersionId: aws.String("v")})
			}
			reconcileDelete(chunk, index, out)
			switch mode {
			case "nil":
				assertCounts(t, chunk, 0, 0, 5, 0)
			case "unexpected":
				assertCounts(t, chunk, 5, 0, 0, 0) // extra ack is ignored, never invents an item
			default:
				assertCounts(t, chunk, 4, 0, 1, 0)
				if chunk[0].Status != DeleteUnknown {
					t.Fatalf("ambiguous item became success/failure: %+v", chunk[0])
				}
			}
		})
	}
}

func TestDeleteWithoutVersionIDAcknowledged(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<DeleteResult><Deleted><Key>plain</Key></Deleted></DeleteResult>`)
	})
	assertCounts(t, c.DeleteObjects(context.Background(), "b", []ObjectID{{Key: "plain"}}), 1, 0, 0, 0)
}

func TestProbeConditionalWrites(t *testing.T) {
	for _, tc := range []struct {
		name string
		h    func(n int, w http.ResponseWriter)
		want bool
	}{
		{"honored", func(n int, w http.ResponseWriter) {
			if n == 2 {
				w.WriteHeader(412)
				fmt.Fprint(w, `<Error><Code>PreconditionFailed</Code></Error>`)
			}
		}, true},
		{"ignored", func(n int, w http.ResponseWriter) {}, false},
		{"not-implemented", func(n int, w http.ResponseWriter) {
			w.WriteHeader(501)
			fmt.Fprint(w, `<Error><Code>NotImplemented</Code></Error>`)
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var puts atomic.Int32
			var deleted atomic.Bool
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case "PUT":
					if r.Header.Get("If-None-Match") != "*" || !strings.HasPrefix(r.URL.Path, "/bucket/.pail-probe-") {
						t.Errorf("bad probe request %s %v", r.URL, r.Header)
					}
					tc.h(int(puts.Add(1)), w)
				case "DELETE":
					deleted.Store(true)
					w.WriteHeader(204)
				}
			})
			got, err := c.ProbeConditionalWrites(context.Background(), "bucket")
			if err != nil || got != tc.want {
				t.Fatalf("got %v err %v", got, err)
			}
			if tc.name != "not-implemented" && !deleted.Load() {
				t.Fatal("probe object not removed")
			}
		})
	}
}

// 4xx answers to the whole request mean nothing was deleted (failed); 5xx and
// transport errors leave the outcome open (unknown).
func TestFxDeleteObjectsWholeRequestRefusal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		code   string
		want   DeleteStatus
	}{
		{"403", 403, "AccessDenied", DeleteFailed},
		{"400", 400, "MalformedXML", DeleteFailed},
		{"404", 404, "NoSuchBucket", DeleteFailed},
		{"500", 500, "InternalError", DeleteUnknown},
		{"503", 503, "SlowDown", DeleteUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.WriteHeader(tc.status)
				fmt.Fprintf(w, `<Error><Code>%s</Code><Message>injected</Message></Error>`, tc.code)
			})
			res := c.DeleteObjects(context.Background(), "b", ids(3))
			for i, r := range res {
				if r.Status != tc.want {
					t.Fatalf("item %d: %+v, want %s", i, r, tc.want)
				}
				if tc.want == DeleteFailed && (r.Code != tc.code || !strings.Contains(r.Message, "injected")) {
					t.Fatalf("item %d lost the service's code/message: %+v", i, r)
				}
			}
		})
	}
}

func TestFxDeleteObjectsTransportErrorIsUnknown(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		panic(http.ErrAbortHandler) // drop the connection without an answer
	})
	res := c.DeleteObjects(context.Background(), "b", ids(2))
	assertCounts(t, res, 0, 0, 2, 0)
}
