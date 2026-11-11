package main

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

var ldRE = regexp.MustCompile(`(?s)<script type="application/ld\+json">(.*?)</script>`)

func structuredData(t *testing.T, page, name string) map[string]any {
	t.Helper()
	m := ldRE.FindAllStringSubmatch(page, -1)
	if len(m) != 1 {
		t.Fatalf("%s: %d JSON-LD blocks, want 1", name, len(m))
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(m[0][1]), &v); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if v["@context"] != "https://schema.org" {
		t.Fatalf("%s: context %v", name, v["@context"])
	}
	return v
}

func TestStructuredData(t *testing.T) {
	out := buildSite(t, "0.6.1")
	for _, lang := range languages {
		name := lang.Path + "index.html"
		page := read(t, filepath.Join(out, filepath.FromSlash(strings.TrimPrefix(lang.Path, "/")), "index.html"))
		if !strings.Contains(page, `<meta name="robots" content="max-image-preview:large">`) {
			t.Errorf("%s: no robots preview hint", name)
		}
		graph, _ := structuredData(t, page, name)["@graph"].([]any)
		if len(graph) != 3 {
			t.Fatalf("%s: graph %v", name, graph)
		}
		site, app := graph[0].(map[string]any), graph[1].(map[string]any)
		if site["@type"] != "WebSite" || site["name"] != "Pail" || site["url"] != "https://pail.download/" {
			t.Errorf("%s: website %v", name, site)
		}
		desc := regexp.MustCompile(`<meta name="description" content="([^"]+)"`).FindStringSubmatch(page)
		for k, want := range map[string]any{
			"@type":           "SoftwareApplication",
			"url":             "https://pail.download" + lang.Path,
			"inLanguage":      lang.HTMLLang,
			"softwareVersion": "0.6.1",
			"downloadUrl":     githubURL + "/releases/tag/v0.6.1",
			"releaseNotes":    "https://pail.download" + docURL(docPageByKey("changelog"), lang),
		} {
			if app[k] != want {
				t.Errorf("%s: %s = %v, want %v", name, k, app[k], want)
			}
		}
		video := graph[2].(map[string]any)
		if video["@type"] != "VideoObject" || video["duration"] != "PT48S" || video["uploadDate"] != demoVideoDate ||
			!strings.HasPrefix(video["contentUrl"].(string), "https://pail.download/assets/pail-demo.") ||
			!strings.HasPrefix(video["thumbnailUrl"].(string), "https://pail.download/assets/pail-demo.") {
			t.Errorf("%s: video %v", name, video)
		}
		if desc == nil || app["description"] != desc[1] {
			t.Errorf("%s: description differs from the meta description", name)
		}
		if app["dateModified"] == nil {
			t.Errorf("%s: a released version has no date", name)
		}
		for _, k := range []string{"screenshot", "image"} {
			if s, _ := app[k].(string); !strings.HasPrefix(s, "https://pail.download/assets/") {
				t.Errorf("%s: %s %q is not a built asset", name, k, s)
			}
		}

		for _, p := range docPages {
			url := docURL(p, lang)
			name := url + "index.html"
			page := read(t, filepath.Join(out, filepath.FromSlash(strings.TrimPrefix(url, "/")), "index.html"))
			v := structuredData(t, page, name)
			items, _ := v["itemListElement"].([]any)
			if v["@type"] != "BreadcrumbList" || len(items) != 2 {
				t.Fatalf("%s: breadcrumbs %v", name, v)
			}
			if last := items[1].(map[string]any); last["item"] != "https://pail.download"+url {
				t.Errorf("%s: breadcrumb ends at %v", name, last["item"])
			}
		}
	}
	if strings.Contains(read(t, filepath.Join(out, "404.html")), "ld+json") {
		t.Error("404 page has structured data")
	}
}

func TestISODuration(t *testing.T) {
	for d, want := range map[time.Duration]string{47872 * time.Millisecond: "PT48S", 65 * time.Second: "PT1M5S", 3725 * time.Second: "PT1H2M5S", 0: "PT0S"} {
		if got := isoDuration(d); got != want {
			t.Errorf("%v: %s, want %s", d, got, want)
		}
	}
}

func TestIndexNowKeyFile(t *testing.T) {
	out := buildSite(t, "1.2.3")
	if got := read(t, filepath.Join(out, indexNowKey+".txt")); got != indexNowKey {
		t.Fatalf("key file holds %q", got)
	}
}

func TestSitemapHasDefaultLanguage(t *testing.T) {
	sm := read(t, filepath.Join(buildSite(t, "1.2.3"), "sitemap.xml"))
	urls := strings.Count(sm, "<url>")
	if urls != 2*(1+len(docPages)) {
		t.Fatalf("%d sitemap entries", urls)
	}
	if n := strings.Count(sm, `hreflang="x-default" href="https://pail.download/"`); n != 2 {
		t.Errorf("landing x-default appears %d times, want 2", n)
	}
	if n := strings.Count(sm, `hreflang="x-default"`); n != urls {
		t.Errorf("%d x-default alternates for %d entries", n, urls)
	}
}
