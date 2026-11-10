package main

import (
	"bytes"
	"image/color"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf16"
)

func near(c color.NRGBA, r, g, b uint8, a uint8) bool {
	d := func(x, y uint8) int {
		if x > y {
			return int(x - y)
		}
		return int(y - x)
	}
	return d(c.R, r) <= 2 && d(c.G, g) <= 2 && d(c.B, b) <= 2 && d(c.A, a) <= 1
}

func TestDrawIconPixels(t *testing.T) {
	img := drawIcon(64)
	if c := img.NRGBAAt(0, 0); c.A != 0 {
		t.Errorf("tile corner should be transparent, got %v", c)
	}
	if c := img.NRGBAAt(8, 32); !near(c, 0x1D, 0x1C, 0x1A, 255) {
		t.Errorf("tile body = %v, want #1D1C1A", c)
	}
	// top bar centre: x=128/4, y=81/4
	if c := img.NRGBAAt(32, 20); !near(c, 0x86, 0xA8, 0xD8, 255) {
		t.Errorf("top bar = %v, want #86A8D8", c)
	}
	if c := img.NRGBAAt(32, 33); !near(c, 0x5C, 0x82, 0xBA, 255) {
		t.Errorf("middle bar = %v, want #5C82BA", c)
	}
	if c := img.NRGBAAt(32, 45); !near(c, 0x34, 0x59, 0x8F, 255) {
		t.Errorf("bottom bar = %v, want #34598F", c)
	}
}

func TestDrawMacIconGrid(t *testing.T) {
	img := drawMacIcon(1024)
	// Outside the 824px tile (100px margin) only a faint shadow may appear.
	for _, p := range [][2]int{{50, 512}, {512, 50}, {974, 512}} {
		if c := img.NRGBAAt(p[0], p[1]); c.A > 8 {
			t.Errorf("margin pixel %v = %v, want (nearly) transparent", p, c)
		}
	}
	if c := img.NRGBAAt(0, 0); c.A != 0 {
		t.Errorf("canvas corner = %v, want transparent", c)
	}
	// Just inside the tile edge, the tile colour.
	if c := img.NRGBAAt(120, 512); !near(c, 0x1D, 0x1C, 0x1A, 255) {
		t.Errorf("tile body = %v, want #1D1C1A", c)
	}
	// The shadow sits below the tile.
	if below, above := img.NRGBAAt(512, 930).A, img.NRGBAAt(512, 94).A; below <= above {
		t.Errorf("shadow alpha below %d should exceed above %d", below, above)
	}
}

func TestContainers(t *testing.T) {
	pngs := map[int][]byte{}
	for _, s := range pngSizes {
		pngs[s] = []byte{1, 2, 3}
	}
	ico := buildICO(pngs, icoSizes)
	if ico[2] != 1 || ico[4] != byte(len(icoSizes)) {
		t.Fatalf("bad ico header % x", ico[:6])
	}
	icns := buildICNS(pngs)
	if string(icns[:4]) != "icns" || len(icns) != 8+8*(8+3) {
		t.Fatalf("bad icns: len %d", len(icns))
	}
}
func TestParseVersion(t *testing.T) {
	for in, want := range map[string][4]uint16{
		"0.0.0":          {0, 0, 0, 0},
		"1.2.3":          {1, 2, 3, 0},
		"0.5.3-rc.1":     {0, 5, 3, 0},
		"2.10.65535+b.7": {2, 10, 65535, 0},
	} {
		got, err := parseVersion(in)
		if err != nil || got != want {
			t.Errorf("parseVersion(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "1.2", "v1.2.3", "1.2.x", "1.2.65536", "-1.2.3", "1.2.3.4"} {
		if _, err := parseVersion(in); err == nil {
			t.Errorf("parseVersion(%q) accepted", in)
		}
	}
}

func TestSysoCarriesVersion(t *testing.T) {
	pngs := map[int][]byte{}
	for _, s := range icoSizes {
		pngs[s] = encodePNG(drawIcon(s))
	}
	objs, err := buildSyso(pngs, [4]uint16{3, 4, 5, 0})
	if err != nil {
		t.Fatal(err)
	}
	want := make([]byte, 0, 16)
	for _, u := range utf16.Encode([]rune("3.4.5.0")) {
		want = append(want, byte(u), byte(u>>8))
	}
	for arch, data := range objs {
		if !bytes.Contains(data, want) {
			t.Errorf("%s resource does not contain version 3.4.5.0", arch)
		}
	}
}

func TestFindRootWalksUpToGoMod(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module github.com/DejavuMoe/Pail\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(root, "tools", "genicons")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := findRoot(deep)
	if err != nil || got != root {
		t.Fatalf("findRoot = %q, %v; want %q", got, err, root)
	}
	if _, err := findRoot(t.TempDir()); err == nil {
		t.Fatal("found a root outside the repository")
	}
}
