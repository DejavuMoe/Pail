package app

import (
	"testing"
	"time"

	"github.com/DejavuMoe/Pail/internal/rules"
)

func zipRule(pattern string) []rules.UploadRule {
	return []rules.UploadRule{{Name: "zip", KeyPattern: pattern, Compression: &rules.Compression{Method: "zip", Level: -1}}}
}

func TestSyncDecideTable(t *testing.T) {
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	meta := func(mt time.Time, size string, sha string) map[string]string {
		m := map[string]string{}
		if !mt.IsZero() {
			m[mtimeMetaKey] = mt.Format(time.RFC3339Nano)
		}
		if size != "" {
			m[syMetaSize] = size
		}
		if sha != "" {
			m[syMetaSHA] = sha
		}
		return m
	}
	l := func(size int64, mt time.Time, sha string) *syLocalInfo {
		return &syLocalInfo{Size: size, MTime: mt, SHA: sha}
	}
	r := func(size int64, mod time.Time, m map[string]string, comp bool) *syRemoteInfo {
		return &syRemoteInfo{Size: size, Modified: mod, Meta: m, Compressed: comp}
	}
	up := func(mode, cmp string) syCmp { return syCmp{Upload: true, Mode: mode, Compare: cmp} }
	down := func(mode, cmp string) syCmp { return syCmp{Mode: mode, Compare: cmp} }

	for _, tc := range []struct {
		name string
		c    syCmp
		l    *syLocalInfo
		r    *syRemoteInfo
		cp   bool
		why  string
	}{
		{"up new", up("new", "size"), l(1, base, ""), nil, true, "new"},
		{"up exists in new mode", up("new", "size"), l(1, base, ""), r(2, base, nil, false), false, "exists"},
		{"up size differs", up("new_changed", "size"), l(1, base, ""), r(2, base, nil, false), true, "size differs"},
		{"up size same", up("new_changed", "size"), l(2, base, ""), r(2, base.Add(-time.Hour), nil, false), false, "unchanged"},
		{"up time equal", up("mirror", "size_time"), l(2, base, ""), r(2, base, meta(base, "", ""), false), false, "unchanged"},
		{"up time newer", up("mirror", "size_time"), l(2, base.Add(time.Second), ""), r(2, base, meta(base, "", ""), false), true, "newer"},
		{"up time older", up("mirror", "size_time"), l(2, base.Add(-time.Second), ""), r(2, base, meta(base, "", ""), false), true, "time differs"},
		{"up no mtime meta, local after LastModified", up("mirror", "size_time"), l(2, base.Add(time.Minute), ""), r(2, base, meta(time.Time{}, "", ""), false), true, "newer"},
		{"up no mtime meta, local before LastModified", up("mirror", "size_time"), l(2, base.Add(-time.Minute), ""), r(2, base, nil, false), false, "unchanged"},
		{"up compressed uses pail-size", up("mirror", "size"), l(100, base, ""), r(7, base, meta(base, "100", ""), true), false, "unchanged"},
		{"up compressed size differs", up("mirror", "size"), l(101, base, ""), r(7, base, meta(base, "100", ""), true), true, "size differs"},
		{"up compressed without pail-size", up("mirror", "size"), l(100, base, ""), r(7, base, meta(base, "", ""), true), true, "no size metadata"},
		{"up checksum equal", up("mirror", "checksum"), l(2, base, "abc"), r(2, base, meta(base, "", "ABC"), false), false, "unchanged"},
		{"up checksum differs", up("mirror", "checksum"), l(2, base, "abc"), r(2, base, meta(base, "", "abd"), false), true, "checksum differs"},
		{"up checksum missing", up("mirror", "checksum"), l(2, base, "abc"), r(2, base, meta(base, "", ""), false), true, "no checksum metadata"},
		{"up checksum size first", up("mirror", "checksum"), l(3, base, "abc"), r(2, base, nil, false), true, "size differs"},

		{"down new", down("new_changed", "size"), nil, r(1, base, nil, false), true, "new"},
		{"down exists in new mode", down("new", "size_time"), l(1, base, ""), r(1, base.Add(time.Hour), nil, false), false, "exists"},
		{"down size differs", down("mirror", "size"), l(1, base, ""), r(2, base, nil, false), true, "size differs"},
		{"down size time ignored", down("mirror", "size"), l(2, base, ""), r(2, base.Add(time.Hour), nil, false), false, "unchanged"},
		{"down newer by LastModified", down("mirror", "size_time"), l(2, base, ""), r(2, base.Add(time.Minute), nil, false), true, "newer"},
		{"down within slack", down("mirror", "size_time"), l(2, base, ""), r(2, base.Add(syTimeSlack), nil, false), false, "unchanged"},
		{"down pail-mtime wins", down("mirror", "size_time"), l(2, base, ""), r(2, base.Add(time.Hour), meta(base, "", ""), false), false, "unchanged"},
		{"down pail-mtime newer", down("mirror", "size_time"), l(2, base, ""), r(2, base, meta(base.Add(time.Hour), "", ""), false), true, "newer"},
		{"down checksum missing", down("mirror", "checksum"), l(2, base, "abc"), r(2, base, meta(base, "", ""), false), true, "no checksum metadata"},
		{"down checksum equal", down("mirror", "checksum"), l(2, base, "abc"), r(2, base, meta(base, "", "abc"), false), false, "unchanged"},
	} {
		cp, why := syDecide(tc.c, tc.l, tc.r)
		if cp != tc.cp || why != tc.why {
			t.Errorf("%s: got (%v, %q), want (%v, %q)", tc.name, cp, why, tc.cp, tc.why)
		}
	}
}

func TestSyncNeedsHead(t *testing.T) {
	l := &syLocalInfo{Size: 5}
	same := &syRemoteInfo{Size: 5}
	other := &syRemoteInfo{Size: 6}
	comp := &syRemoteInfo{Size: 3, Compressed: true}
	for _, tc := range []struct {
		name string
		c    syCmp
		l    *syLocalInfo
		r    *syRemoteInfo
		want bool
	}{
		{"up new", syCmp{Upload: true, Mode: "new_changed", Compare: "checksum"}, l, nil, false},
		{"up mode new", syCmp{Upload: true, Mode: "new", Compare: "checksum"}, l, same, false},
		{"up size compare", syCmp{Upload: true, Mode: "mirror", Compare: "size"}, l, same, false},
		{"up size differs", syCmp{Upload: true, Mode: "mirror", Compare: "size_time"}, l, other, false},
		{"up size_time same size", syCmp{Upload: true, Mode: "mirror", Compare: "size_time"}, l, same, true},
		{"up compressed size compare", syCmp{Upload: true, Mode: "mirror", Compare: "size"}, l, comp, true},
		{"down size compare", syCmp{Mode: "mirror", Compare: "size"}, l, same, false},
		{"down size_time", syCmp{Mode: "mirror", Compare: "size_time"}, l, other, true},
		{"down new file", syCmp{Mode: "new", Compare: "size_time"}, nil, same, true},
		{"down mode new exists", syCmp{Mode: "new", Compare: "size_time"}, l, same, false},
	} {
		if got := syNeedsHead(tc.c, tc.l, tc.r); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestSyncExcluder(t *testing.T) {
	ex := newSyExcluder([]string{"*.log", "build/", "docs/*.tmp", "*/cache/"})
	for rel, want := range map[string]string{
		"a.log":              "a.log",
		"x/y/a.log":          "x/y/a.log",
		"build/out.bin":      "build/",
		"build/sub/deep.txt": "build/",
		"src/build/out.bin":  "", // "build/" is anchored at the root
		"docs/a.tmp":         "docs/a.tmp",
		"docs/a.txt":         "",
		"src/cache/x":        "src/cache/",
		"cache/x":            "", // "*/cache/" needs a parent
		"keep.txt":           "",
		"logs.txt":           "",
	} {
		got, ok := ex.path(rel)
		if (got != "") != ok || got != want {
			t.Errorf("%s: got %q (%v), want %q", rel, got, ok, want)
		}
	}
	if !ex.dir("build") || ex.dir("src") || !ex.dir("a/cache") {
		t.Error("dir matching")
	}
	if got, ok := newSyExcluder(nil).path("a"); ok || got != "" {
		t.Error("no patterns exclude nothing")
	}
}
