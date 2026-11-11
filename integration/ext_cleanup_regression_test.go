package integration_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DejavuMoe/Pail/internal/s3test"
	"github.com/DejavuMoe/Pail/internal/storage"
)

func TestCleanupReportsUninspectableLists(t *testing.T) {
	for _, api := range []string{"uploads", "versions"} {
		for _, failAt := range []int{1, 2, 3} {
			if api == "versions" && failAt == 3 {
				continue
			}
			for _, tc := range []struct {
				code    string
				status  int
				wantErr bool
			}{
				{"AccessDenied", http.StatusForbidden, true},
				{"InternalError", http.StatusInternalServerError, true},
				{"NoSuchBucket", http.StatusNotFound, true},
				{"NotImplemented", http.StatusNotImplemented, false},
				{"UnsupportedOperation", http.StatusBadRequest, false},
				{"NotImplemented", http.StatusForbidden, true},
				{"MethodNotAllowed", http.StatusMethodNotAllowed, false},
				{"GatewayQuirk", http.StatusMethodNotAllowed, false},
				{"SomethingUnknown", http.StatusNotImplemented, false},
				{"Unauthorized", http.StatusUnauthorized, true},
				{"MethodNotAllowed", http.StatusForbidden, true},
			} {
				t.Run(fmt.Sprintf("%s/%s-%d/attempt-%d", api, tc.code, tc.status, failAt), func(t *testing.T) {
					calls := 0
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.Method != http.MethodGet || r.URL.Query().Get("prefix") != "pail-e2e/cleanup-regression/" {
							t.Errorf("unexpected cleanup request: %s %s", r.Method, r.URL)
						}
						w.Header().Set("Content-Type", "application/xml")
						if r.URL.Query().Has(api) {
							calls++
							if calls >= failAt {
								w.WriteHeader(tc.status)
								fmt.Fprintf(w, "<Error><Code>%s</Code><Message>fixture list failure</Message></Error>", tc.code)
								return
							}
						}
						switch {
						case r.URL.Query().Has("versions"):
							fmt.Fprint(w, `<ListVersionsResult><IsTruncated>false</IsTruncated></ListVersionsResult>`)
						case r.URL.Query().Has("uploads"):
							fmt.Fprint(w, `<ListMultipartUploadsResult><IsTruncated>false</IsTruncated></ListMultipartUploadsResult>`)
						default:
							fmt.Fprint(w, `<ListBucketResult><IsTruncated>false</IsTruncated><KeyCount>0</KeyCount></ListBucketResult>`)
						}
					}))
					defer srv.Close()
					sdk, err := storage.New(context.Background(), storage.Config{Endpoint: srv.URL, Region: "us-east-1", UsePathStyle: true, Anonymous: true})
					if err != nil {
						t.Fatal(err)
					}
					left, err := extSweep(context.Background(), sdk, "audit", "pail-e2e/cleanup-regression/")
					if calls < failAt || (err != nil) != tc.wantErr {
						t.Fatalf("calls=%d left=%v err=%v; want error=%v", calls, left, err, tc.wantErr)
					}
					if tc.wantErr && !strings.Contains(err.Error(), tc.code) {
						t.Fatalf("lost list failure: %v", err)
					}
				})
			}
		}
	}
}

func TestCleanupUnsupportedAPIsStillRemoveCurrentObjects(t *testing.T) {
	srv := s3test.New()
	defer srv.Close()
	srv.CreateBucket("audit", false)
	const prefix = "pail-e2e/cleanup-regression/"
	srv.PutObject("audit", prefix+"ours", []byte("remove"), "text/plain", nil)
	srv.PutObject("audit", "outside", []byte("keep"), "text/plain", nil)
	srv.Inject(func(r *http.Request) *s3test.Fault {
		if r.URL.Query().Has("versions") || r.URL.Query().Has("uploads") {
			return &s3test.Fault{Status: http.StatusNotImplemented, Code: "NotImplemented"}
		}
		return nil
	})
	sdk, err := storage.New(context.Background(), storage.Config{Endpoint: srv.URL, Region: "us-east-1", UsePathStyle: true, AccessKeyID: "test", SecretAccessKey: "test"})
	if err != nil {
		t.Fatal(err)
	}
	left, err := extSweep(context.Background(), sdk, "audit", prefix)
	if err != nil || len(left) != 0 {
		t.Fatalf("fallback cleanup: left=%v err=%v", left, err)
	}
	if _, ok := srv.Object("audit", prefix+"ours"); ok {
		t.Error("fallback left current object behind")
	}
	if data, ok := srv.Object("audit", "outside"); !ok || string(data) != "keep" {
		t.Error("fallback changed an object outside the run prefix")
	}
}

func TestCleanupRejectsIncompleteMultipartEnumeration(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		switch {
		case r.URL.Query().Has("uploads"):
			calls++
			fmt.Fprint(w, `<ListMultipartUploadsResult><IsTruncated>true</IsTruncated><NextKeyMarker>next</NextKeyMarker></ListMultipartUploadsResult>`)
		case r.URL.Query().Has("versions"):
			fmt.Fprint(w, `<ListVersionsResult><IsTruncated>false</IsTruncated></ListVersionsResult>`)
		default:
			fmt.Fprint(w, `<ListBucketResult><IsTruncated>false</IsTruncated></ListBucketResult>`)
		}
	}))
	defer srv.Close()
	sdk, err := storage.New(context.Background(), storage.Config{Endpoint: srv.URL, Region: "us-east-1", UsePathStyle: true, Anonymous: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = extSweep(context.Background(), sdk, "audit", "pail-e2e/cleanup-regression/")
	if err == nil || !strings.Contains(err.Error(), "cleanup pages") || calls > 101 {
		t.Fatalf("incomplete pagination: calls=%d err=%v", calls, err)
	}
}
