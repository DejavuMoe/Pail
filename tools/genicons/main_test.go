package main

import (
	"image/color"
	"testing"
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
