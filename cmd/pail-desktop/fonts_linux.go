package main

import (
	_ "embed"
	"log"

	"github.com/egoist/mygo/ui"
)

// Inter 4.1 (SIL Open Font License 1.1, fonts/OFL.txt) is bundled on Linux
// only: the desktop's interface font differs from distribution to distribution
// and the design needs 400, 500 and 600 to look alike everywhere. Windows and
// macOS keep Segoe UI and SF, and their binaries do not carry these files.
//
// The files are subsets of the Inter 4.1 static TTFs (Latin, Greek, Cyrillic,
// punctuation, arrows and symbols; Chinese falls back to the system font),
// keeping hinting, the default OpenType features and tnum, case, zero, frac,
// sups, subs and ordn. For each weight W, with fontTools 4.60:
//
//	pyftsubset Inter-W.ttf --output-file=fonts/Inter-W.ttf \
//	  --unicodes=U+0000-024F,U+0259,U+02B0-036F,U+0370-03FF,U+0400-052F,U+1E00-1EFF,U+2000-218F,U+2190-23FF,U+2460-24FF,U+2500-26FF,U+2700-27BF,U+FB00-FB06,U+FEFF,U+FFFD \
//	  --layout-features+=tnum,case,zero,frac,sups,subs,ordn \
//	  --name-IDs='*' --name-languages='*' --name-legacy --notdef-outline

//go:embed fonts/Inter-Regular.ttf
var interRegular []byte

//go:embed fonts/Inter-Medium.ttf
var interMedium []byte

//go:embed fonts/Inter-SemiBold.ttf
var interSemiBold []byte

//go:embed fonts/OFL.txt
var interLicense string

// uiFont is the theme's font family.
const uiFont = "Inter"

// registerFonts adds the three static Inter files. mygo's Linux text engine
// (Pango over fontconfig) takes each TrueType file as it is: all three report
// the family "Inter" with their own weight, so one family name serves every
// FontWeight and fontconfig picks the file closest to the weight asked for.
// The Regular file is registered under the name "Inter"; the other two keep
// the names their files carry, which include "Inter".
func registerFonts() {
	for _, f := range []struct {
		data   []byte
		family string
	}{{interRegular, "Inter"}, {interMedium, ""}, {interSemiBold, ""}} {
		if err := ui.RegisterFont(f.data, f.family); err != nil {
			log.Printf("font: %v", err)
			return
		}
	}
}
