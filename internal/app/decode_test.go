package app

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DejavuMoe/Pail/internal/rules"
)

type trResolver string

func (r trResolver) ResolvePassword(context.Context, string) (string, error) { return string(r), nil }

func trGzip(t testing.TB, s string) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := gzip.NewWriter(&b)
	zw.Write([]byte(s))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func trZip(t testing.TB, name, s string) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	w, _ := zw.Create(name)
	w.Write([]byte(s))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func trAESZip(t testing.TB, name, s, password string) []byte {
	t.Helper()
	var b bytes.Buffer
	if _, err := rules.CompressAES256ZIP(context.Background(), &b, strings.NewReader(s), name, -1, "ref", trResolver(password)); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestDownloadDecode(t *testing.T) {
	const plain = "decoded payload, decoded payload, decoded payload\n"
	gz := trGzip(t, plain)
	zp := trZip(t, "inner.txt", plain)
	aes := trAESZip(t, "inner.txt", plain, "right")
	// Auto decodes only what Pail itself compressed, which its uploads mark.
	autoMarker := map[string]string{"auto gzip": "gzip", "auto zip": "zip", "aes zip, right password": "aes-zip", "aes zip, wrong password": "aes-zip"}
	for _, tc := range []struct {
		name   string
		object []byte
		req    DownloadRequest
		secret string // stored under "zp" when non-empty
		want   string // expected local content; "" = nothing published
		errHas string
	}{
		{"raw keeps gzip bytes", gz, DownloadRequest{}, "", string(gz), ""},
		{"gzip", gz, DownloadRequest{Decode: DecodeGzip}, "", plain, ""},
		{"auto gzip", gz, DownloadRequest{Decode: DecodeAuto}, "", plain, ""},
		{"zip", zp, DownloadRequest{Decode: DecodeZip}, "", plain, ""},
		{"auto zip", zp, DownloadRequest{Decode: DecodeAuto}, "", plain, ""},
		{"auto passes plain files through", []byte(plain), DownloadRequest{Decode: DecodeAuto}, "", plain, ""},
		{"auto passthrough is not size limited", []byte(plain), DownloadRequest{Decode: DecodeAuto, MaxDecoded: 5}, "", plain, ""},
		{"gzip mode on plain data fails", []byte(plain), DownloadRequest{Decode: DecodeGzip}, "", "", "gzip"},
		{"zip mode on gzip data fails", gz, DownloadRequest{Decode: DecodeZip}, "", "", "ZIP"},
		{"aes zip, right password", aes, DownloadRequest{Decode: DecodeAuto, PasswordRef: "zp"}, "right", plain, ""},
		{"aes zip, wrong password", aes, DownloadRequest{Decode: DecodeAuto, PasswordRef: "zp"}, "wrong", "", "password"},
		{"aes zip, no password reference", aes, DownloadRequest{Decode: DecodeZip}, "", "", "encrypted"},
		{"aes zip, unknown reference", aes, DownloadRequest{Decode: DecodeZip, PasswordRef: "missing"}, "", "", "does not exist"},
		{"gzip over the limit", trGzip(t, strings.Repeat("0", 1<<20)), DownloadRequest{Decode: DecodeGzip, MaxDecoded: 1000}, "", "", "limit"},
		{"zip over the limit", trZip(t, "z", strings.Repeat("0", 1<<20)), DownloadRequest{Decode: DecodeZip, MaxDecoded: 1000}, "", "", "limit"},
		{"aes zip over the limit", trAESZip(t, "z", strings.Repeat("0", 1<<20), "right"), DownloadRequest{Decode: DecodeZip, PasswordRef: "zp", MaxDecoded: 1000}, "right", "", "limit"},
		{"exactly at the limit", trGzip(t, "12345"), DownloadRequest{Decode: DecodeGzip, MaxDecoded: 5}, "", "12345", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTrEnv(t)
			if tc.secret != "" {
				if err := e.s.SetSecret("zp", tc.secret); err != nil {
					t.Fatal(err)
				}
			}
			var meta map[string]string
			if m, ok := autoMarker[tc.name]; ok {
				meta = map[string]string{compressionMetaKey: m}
			}
			e.srv.PutObject("b", "obj.bin", tc.object, "", meta)
			dest := t.TempDir()
			tc.req.Source, tc.req.Keys, tc.req.DestDir = e.loc("b", ""), []string{"obj.bin"}, dest
			tasks := e.download(tc.req)
			dl := trKind(tasks, KindDownload)
			if len(dl) != 1 {
				t.Fatalf("%+v", tasks)
			}
			files := trTree(t, dest) // also fails on leftover temp files
			if tc.errHas != "" {
				if dl[0].Status != StatusFailed || !strings.Contains(dl[0].Err, tc.errHas) {
					t.Fatalf("want failure containing %q, got %s %q", tc.errHas, dl[0].Status, dl[0].Err)
				}
				if len(files) != 0 {
					t.Fatalf("a failed decode published %v", files)
				}
				return
			}
			if dl[0].Status != StatusSucceeded {
				t.Fatalf("%s %q", dl[0].Status, dl[0].Err)
			}
			if !trSameStrings(files, []string{"obj.bin"}) {
				t.Fatalf("files %v", files)
			}
			if got := trRead(t, filepath.Join(dest, "obj.bin")); got != tc.want {
				t.Fatalf("content %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDetectDecode(t *testing.T) {
	for _, tc := range []struct {
		data []byte
		in   DecodeMode
		want DecodeMode
	}{
		{[]byte{0x1f, 0x8b, 8}, DecodeAuto, DecodeGzip},
		{[]byte("PK\x03\x04rest"), DecodeAuto, DecodeZip},
		{[]byte("PK"), DecodeAuto, DecodeRaw},
		{nil, DecodeAuto, DecodeRaw},
		{[]byte("plain"), DecodeAuto, DecodeRaw},
		{[]byte("plain"), DecodeGzip, DecodeGzip},
		{[]byte("plain"), DecodeRaw, DecodeRaw},
	} {
		// The hints that make the magic bytes count.
		hints := decodeHints{Compression: "gzip"}
		if tc.data != nil && len(tc.data) > 0 && tc.data[0] == 'P' {
			hints = decodeHints{Compression: "aes-zip"}
		}
		got, err := detectDecode(bytes.NewReader(tc.data), int64(len(tc.data)), tc.in, hints)
		if err != nil || got != tc.want {
			t.Errorf("detectDecode(%q, %q) = %q, %v; want %q", tc.data, tc.in, got, err, tc.want)
		}
		// Without any hint Auto never decodes.
		if tc.in == DecodeAuto {
			if got, _ := detectDecode(bytes.NewReader(tc.data), int64(len(tc.data)), tc.in, decodeHints{}); got != DecodeRaw {
				t.Errorf("detectDecode(%q) without hints = %q, want raw", tc.data, got)
			}
		}
	}
}
