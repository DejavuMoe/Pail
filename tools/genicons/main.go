// Command genicons renders assets/brand/pail-icon.svg into PNG, ICO and
// ICNS files and into Windows resource objects (.syso) for cmd/pail-desktop.
//
// The SVG only uses rounded rectangles and circles, so the shapes are drawn
// here directly with analytic edge coverage (antialiased) instead of pulling
// in a general SVG renderer. If the SVG changes, update drawIcon to match.
//
// Run from the repository root: go run ./tools/genicons
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"

	"github.com/tc-hib/winres"
	"github.com/tc-hib/winres/version"
)

var pngSizes = []int{16, 24, 32, 48, 64, 128, 256, 512, 1024}
var icoSizes = []int{16, 24, 32, 48, 64, 128, 256}

type shape struct {
	// rounded rectangle (r > 0 or circle when circle is true), in 256 space
	x, y, w, h, r float64
	circle        bool
	stroke        float64 // 0 = filled
	col           [3]float64
	alpha         float64
}

func hex(c uint32) [3]float64 {
	return [3]float64{float64(c >> 16 & 255), float64(c >> 8 & 255), float64(c & 255)}
}

// shapes mirrors assets/brand/pail-icon.svg, back to front.
var shapes = []shape{
	{x: 0, y: 0, w: 256, h: 256, r: 56, col: hex(0x1D1C1A), alpha: 1},
	{x: 1.5, y: 1.5, w: 253, h: 253, r: 54.5, stroke: 3, col: hex(0xFFFFFF), alpha: 0.09},
	{x: 44, y: 61, w: 168, h: 40, r: 20, col: hex(0x86A8D8), alpha: 1},
	{x: 58, y: 108, w: 140, h: 40, r: 20, col: hex(0x5C82BA), alpha: 1},
	{x: 72, y: 155, w: 112, h: 40, r: 20, col: hex(0x34598F), alpha: 1},
	{x: 184, y: 75, w: 12, h: 12, r: 6, circle: true, col: hex(0xF3F1EC), alpha: 1},
	{x: 170, y: 122, w: 12, h: 12, r: 6, circle: true, col: hex(0xF3F1EC), alpha: 0.85},
	{x: 156, y: 169, w: 12, h: 12, r: 6, circle: true, col: hex(0xF3F1EC), alpha: 0.7},
}

// signed distance (negative inside) to a rounded rectangle.
func sdRoundRect(px, py float64, s shape) float64 {
	cx, cy := s.x+s.w/2, s.y+s.h/2
	qx := math.Abs(px-cx) - (s.w/2 - s.r)
	qy := math.Abs(py-cy) - (s.h/2 - s.r)
	return math.Hypot(math.Max(qx, 0), math.Max(qy, 0)) + math.Min(math.Max(qx, qy), 0) - s.r
}

func coverage(sd, px float64) float64 {
	return math.Min(1, math.Max(0, 0.5-sd/px))
}

// drawIcon rasterizes the icon at size x size pixels.
func drawIcon(size int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	scale := 256 / float64(size) // SVG units per pixel
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			x, y := (float64(px)+0.5)*scale, (float64(py)+0.5)*scale
			var r, g, b, a float64 // premultiplied, 0..255 / 0..1
			for _, s := range shapes {
				sd := sdRoundRect(x, y, s)
				if s.stroke > 0 {
					sd = math.Abs(sd) - s.stroke/2
				}
				cv := coverage(sd, scale) * s.alpha
				if cv <= 0 {
					continue
				}
				r = s.col[0]*cv + r*(1-cv)
				g = s.col[1]*cv + g*(1-cv)
				b = s.col[2]*cv + b*(1-cv)
				a = cv + a*(1-cv)
			}
			if a <= 0 {
				continue
			}
			img.SetNRGBA(px, py, color.NRGBA{
				R: uint8(math.Round(r / a)), G: uint8(math.Round(g / a)), B: uint8(math.Round(b / a)), A: uint8(math.Round(a * 255)),
			})
		}
	}
	return img
}

func encodePNG(img image.Image) []byte {
	var b bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&b, img); err != nil {
		panic(err)
	}
	return b.Bytes()
}

func buildICO(pngs map[int][]byte, sizes []int) []byte {
	var b bytes.Buffer
	le := binary.LittleEndian
	binary.Write(&b, le, [3]uint16{0, 1, uint16(len(sizes))})
	off := 6 + 16*len(sizes)
	for _, s := range sizes {
		d := pngs[s]
		dim := byte(s)
		if s >= 256 {
			dim = 0
		}
		b.Write([]byte{dim, dim, 0, 0})
		binary.Write(&b, le, [2]uint16{1, 32}) // planes, bpp
		binary.Write(&b, le, [2]uint32{uint32(len(d)), uint32(off)})
		off += len(d)
	}
	for _, s := range sizes {
		b.Write(pngs[s])
	}
	return b.Bytes()
}

func buildICNS(pngs map[int][]byte) []byte {
	entries := []struct {
		typ  string
		size int
	}{{"ic07", 128}, {"ic08", 256}, {"ic09", 512}, {"ic10", 1024}, {"ic11", 32}, {"ic12", 64}, {"ic13", 256}, {"ic14", 512}}
	var body bytes.Buffer
	for _, e := range entries {
		d := pngs[e.size]
		body.WriteString(e.typ)
		binary.Write(&body, binary.BigEndian, uint32(8+len(d)))
		body.Write(d)
	}
	var b bytes.Buffer
	b.WriteString("icns")
	binary.Write(&b, binary.BigEndian, uint32(8+body.Len()))
	b.Write(body.Bytes())
	return b.Bytes()
}

const manifestXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0">
  <assemblyIdentity type="win32" name="moe.dejavu.pail" version="1.0.0.0" processorArchitecture="*"/>
  <description>Pail</description>
  <compatibility xmlns="urn:schemas-microsoft-com:compatibility.v1">
    <application>
      <supportedOS Id="{8e0f7a12-bfb3-4fe8-b9a5-48fd50a15a9a}"/>
      <supportedOS Id="{1f676c76-80e1-4239-95bb-83d0f6d0da78}"/>
      <supportedOS Id="{4a2f28e3-53b9-4441-ba9c-d69d4a4a6e38}"/>
      <supportedOS Id="{35138b9a-5d96-4fbd-8e2d-a2440225f93a}"/>
    </application>
  </compatibility>
  <trustInfo xmlns="urn:schemas-microsoft-com:asm.v3">
    <security>
      <requestedPrivileges>
        <requestedExecutionLevel level="asInvoker" uiAccess="false"/>
      </requestedPrivileges>
    </security>
  </trustInfo>
  <application xmlns="urn:schemas-microsoft-com:asm.v3">
    <windowsSettings>
      <dpiAware xmlns="http://schemas.microsoft.com/SMI/2005/WindowsSettings">true/pm</dpiAware>
      <dpiAwareness xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">PerMonitorV2,PerMonitor</dpiAwareness>
      <activeCodePage xmlns="http://schemas.microsoft.com/SMI/2019/WindowsSettings">UTF-8</activeCodePage>
    </windowsSettings>
  </application>
</assembly>
`

func buildSyso(pngs map[int][]byte, ver [4]uint16) (map[winres.Arch][]byte, error) {
	var imgs []image.Image
	for _, s := range icoSizes {
		img, err := png.Decode(bytes.NewReader(pngs[s]))
		if err != nil {
			return nil, err
		}
		imgs = append(imgs, img)
	}
	icon, err := winres.NewIconFromImages(imgs)
	if err != nil {
		return nil, err
	}
	rs := &winres.ResourceSet{}
	if err := rs.SetIcon(winres.Name("APP"), icon); err != nil {
		return nil, err
	}
	if err := rs.Set(winres.RT_MANIFEST, winres.ID(1), winres.LCIDDefault, []byte(manifestXML)); err != nil {
		return nil, err
	}
	var vi version.Info
	vi.FileVersion, vi.ProductVersion = ver, ver
	vs := fmt.Sprintf("%d.%d.%d.%d", ver[0], ver[1], ver[2], ver[3])
	for k, v := range map[string]string{
		version.ProductName:      "Pail",
		version.FileDescription:  "Pail",
		version.CompanyName:      "Dejavu Moe",
		version.FileVersion:      vs,
		version.ProductVersion:   vs,
		version.InternalName:     "pail-desktop",
		version.OriginalFilename: "pail-desktop.exe",
		version.LegalCopyright:   "© 2026 Dejavu Moe. MIT License.",
	} {
		if err := vi.Set(version.LangDefault, k, v); err != nil {
			return nil, err
		}
	}
	rs.SetVersionInfo(vi)
	out := map[winres.Arch][]byte{}
	for _, arch := range []winres.Arch{winres.ArchAMD64, winres.ArchARM64} {
		var b bytes.Buffer
		if err := rs.WriteObject(&b, arch); err != nil {
			return nil, err
		}
		out[arch] = b.Bytes()
	}
	return out, nil
}

func write(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	fmt.Printf("wrote %s (%d bytes)\n", path, len(data))
	return os.WriteFile(path, data, 0o644)
}

func main() {
	out := flag.String("out", "assets/brand/generated", "output directory for PNG, ICO and ICNS files")
	sysoDir := flag.String("syso", "cmd/pail-desktop", "directory for rsrc_windows_*.syso (empty: skip)")
	flag.Parse()
	ver := [4]uint16{0, 1, 0, 0}

	pngs := map[int][]byte{}
	for _, s := range pngSizes {
		pngs[s] = encodePNG(drawIcon(s))
		if err := write(filepath.Join(*out, fmt.Sprintf("pail-%d.png", s)), pngs[s]); err != nil {
			fatal(err)
		}
	}
	if err := write(filepath.Join(*out, "pail.ico"), buildICO(pngs, icoSizes)); err != nil {
		fatal(err)
	}
	if err := write(filepath.Join(*out, "pail.icns"), buildICNS(pngs)); err != nil {
		fatal(err)
	}
	if *sysoDir != "" {
		// The window icon embedded by cmd/pail-desktop (go:embed cannot reach assets/).
		if err := write(filepath.Join(*sysoDir, "appicon.png"), pngs[256]); err != nil {
			fatal(err)
		}
		objs, err := buildSyso(pngs, ver)
		if err != nil {
			fatal(err)
		}
		for arch, data := range objs {
			if err := write(filepath.Join(*sysoDir, "rsrc_windows_"+string(arch)+".syso"), data); err != nil {
				fatal(err)
			}
		}
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "genicons:", err)
	os.Exit(1)
}
