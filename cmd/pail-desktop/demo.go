package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math"
	"math/rand/v2"
	"os"
	"strings"
	"sync"
	"time"

	core "github.com/DejavuMoe/Pail/internal/app"
	"github.com/DejavuMoe/Pail/internal/s3test"
)

// demoAccountName is the one account of the demo environment.
const demoAccountName = "Demo · in-memory S3"

// demoEnv is the --demo environment: an in-memory S3 server (internal/s3test),
// seeded with a realistic bucket layout, and a real application Service that
// talks to it with a throw-away config directory and an in-memory keyring.
type demoEnv struct {
	Srv     *s3test.Server
	Svc     *core.Service
	Dir     string
	Account core.Account
	owned   bool // Dir was created here and is removed by Close
}

// newDemoEnv builds the environment in dir ("" creates a temporary directory
// that Close removes). With seed false only the server, the service and the
// account exist, with no buckets.
func newDemoEnv(dir string, seed bool) (*demoEnv, error) {
	d := &demoEnv{Dir: dir}
	if dir == "" {
		tmp, err := os.MkdirTemp("", "pail-demo-*")
		if err != nil {
			return nil, err
		}
		d.Dir, d.owned = tmp, true
	}
	d.Srv = s3test.New()
	svc, err := core.New(core.Options{ConfigDir: d.Dir, Keyring: core.NewMemoryKeyring()})
	if err != nil {
		d.Close()
		return nil, err
	}
	d.Svc = svc
	acct, err := svc.SaveAccount(core.Account{
		Name: demoAccountName, Provider: core.ProviderS3, Endpoint: d.Srv.URL, Region: "us-east-1",
		PathStyle: true, Credentials: core.CredStatic, AccessKeyID: "demo",
	}, &core.Secrets{SecretAccessKey: "demo-secret"})
	if err != nil {
		d.Close()
		return nil, err
	}
	d.Account = acct
	if seed {
		if err := d.seed(); err != nil {
			d.Close()
			return nil, err
		}
	}
	return d, nil
}

// Close stops the server and the service and removes a temporary directory.
func (d *demoEnv) Close() {
	if d.Svc != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = d.Svc.Shutdown(ctx)
		cancel()
	}
	if d.Srv != nil {
		d.Srv.Close()
	}
	if d.owned {
		_ = os.RemoveAll(d.Dir)
	}
}

type demoBucket struct{ name, region string }

var demoBuckets = []demoBucket{
	{"acme-assets", "us-east-1"},
	{"acme-backups", "us-west-2"},
	{"acme-logs", "eu-west-1"},
	{"acme-analytics", "us-east-1"},
	{"acme-website", "us-east-1"},
	{"acme-media-archive", "ap-southeast-2"},
	{demoVersioned, "us-east-1"},
}

// demoVersioned is the bucket with versioning on: notes.txt has three
// versions, config/app.yaml two, and old-plan.txt was deleted.
const demoVersioned = "acme-versioned"

const (
	clsStd  = "STANDARD"
	clsIA   = "STANDARD_IA"
	clsGla  = "GLACIER"
	clsDeep = "DEEP_ARCHIVE"
	clsIT   = "INTELLIGENT_TIERING"
)

// seed creates the buckets through the Service (so they carry regions) and
// fills them directly in the server. Bodies stay small: sizes in the table
// are real, just modest.
func (d *demoEnv) seed() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, b := range demoBuckets {
		if err := d.Svc.CreateBucket(ctx, d.Account.ID, b.name, b.region, false); err != nil {
			return fmt.Errorf("create demo bucket %s: %w", b.name, err)
		}
	}
	rng := rand.New(rand.NewPCG(7, 42))
	put := func(bucket, key string, body []byte, class string) {
		ct := contentTypeFor(key)
		d.Srv.PutObject(bucket, key, body, ct, nil)
		if class != "" && class != clsStd {
			d.Srv.SetStorageClass(bucket, key, class)
		}
	}
	filler := func(key string, n int) []byte {
		line := "Pail demo object " + key + "\n"
		return []byte(strings.Repeat(line, n/len(line)+1)[:n])
	}
	const a = "acme-assets"
	put(a, "README.txt", []byte(readmeText), clsStd)
	d.Srv.PutObject(a, "manifest.json", []byte(manifestJSON), "application/json", map[string]string{
		"build-id": "b-20261018.4", "uploaded-by": "ci-deploy", "source-sha": "9f2c1ae",
	})
	put(a, "tmp/", nil, clsStd)
	for key, body := range demoImages() {
		put(a, key, body, clsStd)
	}
	for _, n := range []string{"Inter-Regular.woff2", "Inter-SemiBold.woff2", "JetBrainsMono.ttf"} {
		put(a, "assets/fonts/"+n, filler(n, 40_000+rng.IntN(60_000)), clsIA)
	}
	put(a, "assets/video/launch-teaser.mp4", filler("teaser", 180_000), clsStd)
	put(a, "assets/video/onboarding-walkthrough.mov", filler("walkthrough", 240_000), clsIA)
	put(a, "assets/video/customer-story.webm", filler("story", 96_000), clsIT)
	put(a, "assets/audio/podcast-ep12.mp3", filler("podcast", 120_000), clsStd)
	put(a, "assets/icons/sprite.svg", []byte(spriteSVG), clsStd)
	for i, n := range []string{"2026-01", "2026-02", "2026-03", "2026-04", "2026-05", "2026-06", "2026-07", "2026-08", "2026-09", "2026-10"} {
		put(a, "backups/2026/db-"+n+".sql.gz", filler(n, 3_000+i*900+rng.IntN(900)), clsGla)
		put(a, "backups/2026/files-"+n+".tar.zst", filler(n, 6_000+i*700), clsDeep)
	}
	put(a, "backups/2025/db-2025-12.sql.gz", filler("2025-12", 5_400), clsDeep)
	for i := 0; i < 5000; i++ {
		put(a, fmt.Sprintf("logs/access/access-%04d.log.gz", i), filler("access", 32+rng.IntN(960)), clsStd)
	}
	for i := 0; i < 12; i++ {
		put(a, fmt.Sprintf("logs/app/app-2026-10-%02d.jsonl", i+1), []byte(jsonLines(i)), clsStd)
	}
	for _, q := range []string{"2026-Q1", "2026-Q2", "2026-Q3"} {
		put(a, "reports/"+q+"-financials.pdf", filler(q, 14_000+rng.IntN(8_000)), clsStd)
		put(a, "reports/"+q+"-kpis.xlsx", filler(q, 9_000), clsStd)
	}
	put(a, "reports/customers-export.csv", []byte(customersCSV), clsIA)
	put(a, "reports/exports/2026-10-17.csv", []byte(customersCSV), clsStd)
	put(a, "website/index.html", []byte(indexHTML), clsStd)
	put(a, "website/404.html", []byte("<!doctype html><title>Not found</title><h1>404</h1>\n"), clsStd)
	put(a, "website/robots.txt", []byte("User-agent: *\nDisallow:\n"), clsStd)
	put(a, "website/css/app.3f9a1c.css", filler("css", 8_000), clsStd)
	put(a, "website/js/app.8be201.js", filler("js", 22_000), clsStd)
	put(a, "website/js/vendor.77a0d4.js", filler("vendor", 31_000), clsStd)
	put(a, "website/img/favicon.ico", filler("ico", 1_500), clsStd)

	lite := func(bucket string, names ...string) {
		for _, n := range names {
			put(bucket, n, filler(n, 600+rng.IntN(9_000)), clsStd)
		}
	}
	lite("acme-backups", "nightly/2026-10-17.tar.zst", "nightly/2026-10-18.tar.zst", "weekly/2026-W41.tar.zst", "restore-test.log")
	lite("acme-logs", "cloudtrail/2026/10/18/events-0001.json.gz", "cloudtrail/2026/10/18/events-0002.json.gz", "alb/2026/10/18/access.log.gz")
	lite("acme-analytics", "events/2026-10-18.parquet", "events/2026-10-17.parquet", "dashboards/revenue.json", "README.md")
	lite("acme-website", "index.html", "about.html", "styles.css", "app.js", "favicon.ico")
	lite("acme-media-archive", "2024/shoot-a/IMG_0001.jpg", "2024/shoot-a/IMG_0002.jpg", "2024/shoot-b/clip.mp4")

	d.Srv.CreateBucket(demoVersioned, true) // turns versioning on
	const v = demoVersioned
	for i, body := range []string{"Draft notes\n", "Draft notes\n- ship the beta\n", "Draft notes\n- ship the beta\n- write the docs\n"} {
		d.Srv.PutObject(v, "notes.txt", []byte(body), "text/plain; charset=utf-8", map[string]string{"revision": fmt.Sprint(i + 1)})
	}
	d.Srv.PutObject(v, "config/app.yaml", []byte("debug: true\n"), "application/yaml", nil)
	d.Srv.PutObject(v, "config/app.yaml", []byte("debug: false\nregion: us-east-1\n"), "application/yaml", nil)
	d.Srv.PutObject(v, "old-plan.txt", []byte("The plan we dropped.\n"), "text/plain; charset=utf-8", nil)
	d.Srv.PutObject(v, "report.pdf", filler("report", 12_000), "application/pdf", nil)
	d.Srv.DeleteObject(v, "old-plan.txt") // a delete marker: the key is "deleted"
	return nil
}

func contentTypeFor(key string) string {
	i := strings.LastIndexByte(key, '.')
	if i < 0 || strings.HasSuffix(key, "/") {
		if strings.HasSuffix(key, "/") {
			return "application/x-directory"
		}
		return "application/octet-stream"
	}
	switch strings.ToLower(key[i:]) {
	case ".txt", ".log":
		return "text/plain; charset=utf-8"
	case ".json":
		return "application/json"
	case ".jsonl":
		return "application/x-ndjson"
	case ".csv":
		return "text/csv"
	case ".html":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css"
	case ".js":
		return "text/javascript"
	case ".md":
		return "text/markdown"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".mp4":
		return "video/mp4"
	case ".mov":
		return "video/quicktime"
	case ".webm":
		return "video/webm"
	case ".mp3":
		return "audio/mpeg"
	case ".pdf":
		return "application/pdf"
	case ".gz":
		return "application/gzip"
	case ".zst":
		return "application/zstd"
	}
	return "application/octet-stream"
}

// Keep only immutable image bodies; PutObject copies them into each isolated server.
var demoImages = sync.OnceValue(func() map[string][]byte {
	images := make(map[string][]byte, 9)
	for i, name := range []string{"hero.png", "logo-dark.png", "logo-light.png", "team-offsite.jpg", "product-shot-01.png", "product-shot-02.png", "banner-autumn.png", "og-card.png"} {
		images["assets/images/"+name] = demoImage(name, i)
	}
	images["website/img/hero.png"] = demoImage("hero", 9)
	return images
})

// demoImage draws a small, smooth 320x200 picture; key picks the hue. A .jpg
// name yields JPEG, everything else PNG.
func demoImage(key string, n int) []byte {
	const w, h = 320, 200
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	hue := float64(n*47%360) / 360
	cx, cy := float64(w)*(0.3+0.05*float64(n%5)), float64(h)*0.55
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			t := float64(x)/w*0.6 + float64(y)/h*0.4
			r, g, b := hsv(hue+0.08*t, 0.35, 0.96-0.25*t)
			if d := math.Hypot(float64(x)-cx, float64(y)-cy); d < 44 {
				r, g, b = hsv(hue+0.5, 0.55, 0.98-d/200)
			}
			img.Set(x, y, color.RGBA{r, g, b, 255})
		}
	}
	var buf bytes.Buffer
	if strings.HasSuffix(key, ".jpg") {
		_ = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80})
	} else {
		_ = png.Encode(&buf, img)
	}
	return buf.Bytes()
}

func hsv(h, s, v float64) (r, g, b uint8) {
	h -= math.Floor(h)
	i := int(h * 6)
	f := h*6 - float64(i)
	p, q, t := v*(1-s), v*(1-f*s), v*(1-(1-f)*s)
	var rr, gg, bb float64
	switch i % 6 {
	case 0:
		rr, gg, bb = v, t, p
	case 1:
		rr, gg, bb = q, v, p
	case 2:
		rr, gg, bb = p, v, t
	case 3:
		rr, gg, bb = p, q, v
	case 4:
		rr, gg, bb = t, p, v
	default:
		rr, gg, bb = v, p, q
	}
	return uint8(rr * 255), uint8(gg * 255), uint8(bb * 255)
}

func jsonLines(day int) string {
	var b strings.Builder
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&b, `{"ts":"2026-10-%02dT%02d:%02d:00Z","level":"info","msg":"request served","path":"/v1/items/%d","ms":%d}`+"\n", day+1, i%24, (i*7)%60, 1000+i*13, 12+(i*5)%90)
	}
	return b.String()
}

const readmeText = `acme-assets
===========

Static assets for the Acme storefront and marketing site.

  assets/   images, fonts, video and audio used by the apps
  website/  the published static site (deployed by CI)
  reports/  finance exports, refreshed monthly
  backups/  database dumps and file archives (lifecycle: Glacier after 30 days)
  logs/     access and application logs

Files in this bucket are written by the deploy pipeline. Do not edit by hand.
`

const manifestJSON = `{
  "name": "acme-storefront",
  "version": "2026.10.18",
  "build": "b-20261018.4",
  "assets": {
    "images": 8,
    "fonts": 3,
    "video": 3
  },
  "regions": ["us-east-1", "us-west-2", "eu-west-1"],
  "cdn": {
    "distribution": "E2ABCDEF12345",
    "ttl": 31536000,
    "immutable": true
  }
}
`

const spriteSVG = `<svg xmlns="http://www.w3.org/2000/svg" width="48" height="48" viewBox="0 0 48 48"><circle cx="24" cy="24" r="20" fill="#34598f"/></svg>
`

const customersCSV = `id,name,plan,region,mrr
1001,Northwind Traders,team,us-east-1,420
1002,Contoso Ltd,pro,eu-west-1,120
1003,Fabrikam Inc,team,us-west-2,380
1004,Adventure Works,free,us-east-1,0
1005,Tailspin Toys,pro,ap-southeast-2,120
1006,Wingtip,team,us-east-1,420
`

const indexHTML = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>Acme</title><link rel="stylesheet" href="css/app.3f9a1c.css"></head>
<body><h1>Acme</h1><script src="js/app.8be201.js"></script></body>
</html>
`
