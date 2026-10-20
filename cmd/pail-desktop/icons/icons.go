// Package icons embeds the Lucide icon set (ISC, see LICENSE) used by Pail.
package icons

import (
	"embed"
	"path"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"
)

//go:embed *.svg
var files embed.FS

var (
	mu    sync.Mutex
	cache = map[string]*ui.SVG{}
)

// Get returns the parsed icon with the given Lucide name (for example
// "folder-plus"). An unknown name falls back to the generic file icon so a
// typo never blanks a row.
func Get(name string) *ui.SVG {
	mu.Lock()
	defer mu.Unlock()
	if s, ok := cache[name]; ok {
		return s
	}
	data, err := files.ReadFile(name + ".svg")
	if err != nil {
		data, _ = files.ReadFile("file.svg")
	}
	s := ui.MustParseSVG(data)
	cache[name] = s
	return s
}

// Names lists every embedded icon name (used by tests).
func Names() []string {
	entries, _ := files.ReadDir(".")
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, strings.TrimSuffix(e.Name(), ".svg"))
	}
	return out
}

var byExt = map[string]string{
	".txt": "file-text", ".md": "file-text", ".log": "file-text", ".pdf": "file-text", ".doc": "file-text", ".docx": "file-text", ".rtf": "file-text",
	".png": "file-image", ".jpg": "file-image", ".jpeg": "file-image", ".gif": "file-image", ".webp": "file-image", ".svg": "file-image", ".bmp": "file-image", ".ico": "file-image", ".avif": "file-image",
	".mp4": "file-video", ".mov": "file-video", ".mkv": "file-video", ".webm": "file-video", ".avi": "file-video",
	".mp3": "file-audio", ".wav": "file-audio", ".flac": "file-audio", ".ogg": "file-audio", ".m4a": "file-audio",
	".zip": "file-archive", ".gz": "file-archive", ".tgz": "file-archive", ".tar": "file-archive", ".7z": "file-archive", ".rar": "file-archive", ".bz2": "file-archive", ".zst": "file-archive",
	".go": "file-code", ".js": "file-code", ".ts": "file-code", ".css": "file-code", ".html": "file-code", ".htm": "file-code", ".py": "file-code", ".sh": "file-code", ".xml": "file-code", ".yaml": "file-code", ".yml": "file-code", ".sql": "file-code",
	".json": "file-json", ".jsonl": "file-json",
	".csv": "file-spreadsheet", ".xls": "file-spreadsheet", ".xlsx": "file-spreadsheet", ".tsv": "file-spreadsheet", ".parquet": "file-spreadsheet",
	".pem": "file-lock", ".key": "file-lock", ".enc": "file-lock",
	".woff": "file-type", ".woff2": "file-type", ".ttf": "file-type", ".otf": "file-type",
	".bin": "package", ".iso": "package", ".dmg": "package", ".exe": "package",
}

// NameFor returns the Lucide name of the type icon for an object key.
func NameFor(key string, folder bool) string {
	if folder {
		return "folder"
	}
	if n, ok := byExt[strings.ToLower(path.Ext(key))]; ok {
		return n
	}
	return "file"
}

// ForKey returns the type icon for an object key or folder.
func ForKey(key string, folder bool) *ui.SVG { return Get(NameFor(key, folder)) }
