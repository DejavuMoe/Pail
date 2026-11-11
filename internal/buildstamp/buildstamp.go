// Package buildstamp holds the release version of the two programs.
//
// Release builds set it with LDFlag(version), which stores the marker
// "pail-release-version:<version>;" in the binary's read-only data. The
// marker survives stripped builds (-s -w), so tools/verify-release can read
// the version without a symbol table: it must occur exactly once, and the
// programs report exactly the version it carries.
package buildstamp

import "strings"

// Name is the marker name; the source never spells it with its colon, so the
// complete marker appears in a binary only when a release build sets it.
const Name = "pail-release-version"

// Var is the linker path of the stamped variable.
const Var = "github.com/DejavuMoe/Pail/internal/buildstamp.stamp"

var stamp string

// Version is the stamped version, or "dev" for a build without a valid stamp.
func Version() string {
	if v, ok := Parse(stamp); ok {
		return v
	}
	return "dev"
}

// Parse returns the version in a marker "<Name>:<version>;".
func Parse(marker string) (string, bool) {
	name, rest, ok := strings.Cut(marker, ":")
	if !ok || name != Name {
		return "", false
	}
	v, ok := strings.CutSuffix(rest, ";")
	if !ok || !ValidVersion(v) {
		return "", false
	}
	return v, true
}

// ValidVersion reports whether v is a non-empty version made of letters,
// digits, '.', '-' and '+', at most 64 bytes long.
func ValidVersion(v string) bool {
	if v == "" || len(v) > 64 {
		return false
	}
	for _, r := range v {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '.' || r == '-' || r == '+') {
			return false
		}
	}
	return true
}

// LDFlag is the -ldflags argument that stamps version.
func LDFlag(version string) string {
	return "-X " + Var + "=" + Name + ":" + version + ";"
}
