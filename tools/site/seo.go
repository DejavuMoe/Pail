package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"strconv"
	"time"
)

// Structured data (schema.org JSON-LD) lets search engines read what the site
// is about without guessing from the text: the site's name, the application,
// its platforms, price and version, and where each docs page sits.

type ldThing = map[string]any

// appStructuredData is the JSON-LD of a landing page: the site and the
// application it offers.
// demoVideo is the demo recording shown on the landing page.
type demoVideo struct {
	name, description, contentURL, thumbnailURL string
	duration                                    time.Duration
}

// demoVideoDate is when docs/video/pail-demo.mp4 was published; change it
// together with the video.
const demoVideoDate = "2026-11-07"

func appStructuredData(base string, lang language, description, version, released, screenshot, image string, video demoVideo) (template.JS, error) {
	app := ldThing{
		"@type":               "SoftwareApplication",
		"@id":                 base + "/#app",
		"name":                "Pail",
		"description":         description,
		"url":                 base + lang.Path,
		"inLanguage":          lang.HTMLLang,
		"applicationCategory": "DeveloperApplication",
		"operatingSystem":     "Windows, macOS, Linux",
		"softwareVersion":     version,
		"downloadUrl":         githubURL + "/releases/tag/v" + version,
		"releaseNotes":        base + docURL(docPageByKey("changelog"), lang),
		"license":             "https://opensource.org/licenses/MIT",
		"isAccessibleForFree": true,
		"offers":              ldThing{"@type": "Offer", "price": "0", "priceCurrency": "USD"},
		"screenshot":          screenshot,
		"image":               image,
		"sameAs":              []string{githubURL},
	}
	if released != "" {
		app["dateModified"] = released
	}
	return marshalLD(ldThing{
		"@context": "https://schema.org",
		"@graph": []ldThing{
			{"@type": "WebSite", "@id": base + "/#website", "name": "Pail", "url": base + "/", "inLanguage": []string{"en", "zh-Hans"}},
			app,
			{
				"@type": "VideoObject", "@id": base + lang.Path + "#demo",
				"name": video.name, "description": video.description, "inLanguage": lang.HTMLLang,
				"contentUrl": video.contentURL, "thumbnailUrl": video.thumbnailURL,
				"uploadDate": demoVideoDate, "duration": isoDuration(video.duration),
			},
		},
	})
}

// docStructuredData is the JSON-LD of a docs page: its place below the
// landing page of the same language.
func docStructuredData(base string, lang language, title, canonical string) (template.JS, error) {
	return marshalLD(ldThing{
		"@context": "https://schema.org",
		"@type":    "BreadcrumbList",
		"itemListElement": []ldThing{
			{"@type": "ListItem", "position": 1, "name": "Pail", "item": base + lang.Path},
			{"@type": "ListItem", "position": 2, "name": title, "item": canonical},
		},
	})
}

// isoDuration writes d as an ISO 8601 duration (PT1M5S), whole seconds.
func isoDuration(d time.Duration) string {
	s := int(d.Round(time.Second) / time.Second)
	out := "PT"
	if s >= 3600 {
		out += strconv.Itoa(s/3600) + "H"
		s %= 3600
	}
	if s >= 60 {
		out += strconv.Itoa(s/60) + "M"
		s %= 60
	}
	return out + strconv.Itoa(s) + "S"
}

// mp4Duration reads the length of an MP4 file from its movie header (mvhd).
func mp4Duration(name string) (time.Duration, error) {
	b, err := os.ReadFile(name)
	if err != nil {
		return 0, err
	}
	i := bytes.Index(b, []byte("mvhd"))
	if i < 0 || len(b) < i+40 {
		return 0, fmt.Errorf("%s: no movie header", name)
	}
	var scale, length uint64
	switch b[i+4] { // version
	case 0:
		scale, length = uint64(binary.BigEndian.Uint32(b[i+16:])), uint64(binary.BigEndian.Uint32(b[i+20:]))
	case 1:
		scale, length = uint64(binary.BigEndian.Uint32(b[i+24:])), binary.BigEndian.Uint64(b[i+28:])
	default:
		return 0, fmt.Errorf("%s: unknown movie header version", name)
	}
	if scale == 0 || length == 0 {
		return 0, fmt.Errorf("%s: the movie header has no duration", name)
	}
	return time.Duration(float64(length) / float64(scale) * float64(time.Second)), nil
}

func marshalLD(v any) (template.JS, error) {
	b, err := json.Marshal(v) // escapes <, > and & for use inside <script>
	return template.JS(b), err
}
