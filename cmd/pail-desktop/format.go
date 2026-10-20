package main

import (
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/DejavuMoe/Pail/cmd/pail-desktop/locales"
)

// humanBytes formats a size with binary steps and the usual KB/MB/GB labels.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	v, i := float64(n), 0
	for v >= unit && i < 4 {
		v /= unit
		i++
	}
	s := fmt.Sprintf("%.1f", v)
	if v >= 100 {
		s = fmt.Sprintf("%.0f", v)
	}
	return s + " " + []string{"B", "KB", "MB", "GB", "TB"}[i]
}

// groupInt formats 1005 as "1,005".
func groupInt(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Format("2006-01-02 15:04")
}

func formatETA(done, total int64, speed float64) string {
	if speed <= 0 || total <= done {
		return "—"
	}
	s := int64(float64(total-done) / speed)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s%3600/60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

func formatSpeed(speed float64) string {
	if speed < 1 {
		return "—"
	}
	return humanBytes(int64(speed)) + "/s"
}

// typeLabel is the Type column text: "Folder", "PNG image", "File".
func typeLabel(e Entry) string {
	if e.Folder {
		return locales.FormatFolder()
	}
	ext := strings.ToUpper(strings.TrimPrefix(path.Ext(e.Key), "."))
	if ext == "" {
		return locales.FormatFile()
	}
	return locales.FormatTypeFile(ext)
}

// extOf is the sort key of the Type column.
func extOf(e Entry) string {
	if e.Folder {
		return ""
	}
	return strings.ToLower(path.Ext(e.Key))
}

// storageLabel shortens S3 storage class names for the table.
func storageLabel(c string) string {
	switch c {
	case "":
		return "—"
	case "STANDARD":
		return locales.FormatClassStandard()
	case "STANDARD_IA":
		return locales.FormatClassStandardIa()
	case "ONEZONE_IA":
		return locales.FormatClassOnezoneIa()
	case "INTELLIGENT_TIERING":
		return locales.FormatClassIntelligentTiering()
	case "GLACIER_IR":
		return locales.FormatClassGlacierIr()
	case "GLACIER":
		return locales.FormatClassGlacier()
	case "DEEP_ARCHIVE":
		return locales.FormatClassDeepArchive()
	case "REDUCED_REDUNDANCY":
		return locales.FormatClassReducedRedundancy()
	}
	return c
}

// classChoices labels S3 storage classes the way the table does.
func classChoices(ids ...string) []choice {
	out := make([]choice, len(ids))
	for i, id := range ids {
		out[i] = choice{id, storageLabel(id)}
	}
	return out
}

var s3Classes = []string{"STANDARD", "STANDARD_IA", "ONEZONE_IA", "INTELLIGENT_TIERING",
	"GLACIER_IR", "GLACIER", "DEEP_ARCHIVE", "REDUCED_REDUNDANCY"}

// splitPrefix turns "a/b/" into ["a", "b"].
func splitPrefix(prefix string) []string {
	prefix = strings.Trim(prefix, "/")
	if prefix == "" {
		return nil
	}
	return strings.Split(prefix, "/")
}
