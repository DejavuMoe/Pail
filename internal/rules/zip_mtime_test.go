package rules

import (
	"archive/zip"
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func zipMemberTime(t *testing.T, archive []byte) time.Time {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil || len(zr.File) != 1 {
		t.Fatalf("read archive: %v", err)
	}
	return zr.File[0].Modified
}

// A member carries the date of the file it was made from, never the 1979 of an
// empty header.
func TestZIPMemberCarriesTheSourceModificationTime(t *testing.T) {
	want := time.Date(2021, 3, 4, 5, 6, 8, 0, time.UTC)
	compress := map[string]func(modified time.Time) []byte{
		"zip": func(modified time.Time) []byte {
			var out bytes.Buffer
			if _, err := CompressZIP(context.Background(), &out, strings.NewReader("data"), "a.txt", modified, 1); err != nil {
				t.Fatal(err)
			}
			return out.Bytes()
		},
		"aes zip": func(modified time.Time) []byte {
			var out bytes.Buffer
			if _, err := CompressAES256ZIP(context.Background(), &out, strings.NewReader("data"), "a.txt", modified, 1, "ref", testZIPPassword); err != nil {
				t.Fatal(err)
			}
			return out.Bytes()
		},
	}
	near := func(a, b time.Time) bool { d := a.Sub(b); return d > -3*time.Second && d < 3*time.Second }
	for name, c := range compress {
		t.Run(name, func(t *testing.T) {
			if got := zipMemberTime(t, c(want)); !near(got, want) {
				t.Errorf("modified = %v, want %v", got, want)
			}
			// A local time zone does not shift the stamp.
			if got := zipMemberTime(t, c(want.In(time.FixedZone("x", 8*3600)))); !near(got, want) {
				t.Errorf("modified (+08:00) = %v, want %v", got, want)
			}
			// Unknown: now.
			if got := zipMemberTime(t, c(time.Time{})); !near(got, time.Now()) {
				t.Errorf("zero time gave %v, want about now", got)
			}
			// Out of range for an MS-DOS date: clamped, not wrapped.
			if got := zipMemberTime(t, c(time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC))); got.Year() != 1980 {
				t.Errorf("1970 gave %v, want 1980", got)
			}
			if got := zipMemberTime(t, c(time.Date(2300, 1, 1, 0, 0, 0, 0, time.UTC))); got.Year() != 2105 {
				t.Errorf("2300 gave %v, want 2105", got)
			}
		})
	}
}
