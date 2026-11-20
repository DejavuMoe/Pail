package main

import (
	"encoding/json"
	"html/template"
)

// Structured data (schema.org JSON-LD) lets search engines read what the site
// is about without guessing from the text: the site's name, the application,
// its platforms, price and version, and where each docs page sits.

type ldThing = map[string]any

// appStructuredData is the JSON-LD of a landing page: the site and the
// application it offers.
func appStructuredData(base string, lang language, description, version, released, screenshot, image string) (template.JS, error) {
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

func marshalLD(v any) (template.JS, error) {
	b, err := json.Marshal(v) // escapes <, > and & for use inside <script>
	return template.JS(b), err
}
