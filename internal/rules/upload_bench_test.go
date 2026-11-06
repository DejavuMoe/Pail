package rules

import "testing"

// benchRules is a typical rule set: a few rules with headers and metadata.
func benchRules() []UploadRule {
	return []UploadRule{
		{Name: "images", KeyPattern: "*.jpg", Headers: map[string]string{"Cache-Control": "max-age=3600", "Content-Language": "en"}, Metadata: map[string]string{"origin": "pail", "team": "web"}},
		{Name: "logs", KeyPattern: "logs/*", StorageClass: "STANDARD_IA", Compression: &Compression{Method: "gzip", Level: 6}},
		{Name: "private", BucketPattern: "private-*", SSE: &Encryption{Type: "AES256"}},
		{Name: "docs", KeyPattern: "docs/*", Headers: map[string]string{"Content-Disposition": "inline"}},
		{Name: "all", Metadata: map[string]string{"uploaded-by": "pail"}},
	}
}

// Evaluate checks the whole rule set before every match.
func BenchmarkEvaluate(b *testing.B) {
	rs := benchRules()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Evaluate(rs, "private-assets", "docs/readme.txt"); err != nil {
			b.Fatal(err)
		}
	}
}

// Select skips the check for a rule set that was validated when it was saved.
func BenchmarkSelect(b *testing.B) {
	rs := benchRules()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		Select(rs, "private-assets", "docs/readme.txt")
	}
}
