package main

import (
	"bytes"
	"debug/buildinfo"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/DejavuMoe/Pail/internal/buildstamp"
)

// versionFixture builds a small program that reports its version through the
// real internal/buildstamp package, stamped with version unless it is empty.
func versionFixture(t *testing.T, target, version, flags string) []byte {
	t.Helper()
	dir := t.TempDir()
	stamp, err := os.ReadFile(filepath.Join("..", "..", "internal", "buildstamp", "buildstamp.go"))
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"go.mod":                       "module github.com/DejavuMoe/Pail\n\ngo 1.27.3\n",
		"internal/buildstamp/stamp.go": string(stamp),
		"cmd/pail/main.go": "package main\n\nimport (\n\t\"fmt\"\n\n\t\"github.com/DejavuMoe/Pail/internal/buildstamp\"\n)\n\n" +
			"var other = \"1.2.3\"\n\nfunc main() { fmt.Println(buildstamp.Version(), other) }\n",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	osName, arch, _ := strings.Cut(target, "/")
	bin := filepath.Join(dir, "fixture")
	ldflags := flags
	if version != "" {
		ldflags += " " + buildstamp.LDFlag(version)
	}
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags", ldflags, "-o", bin, "./cmd/pail")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS="+osName, "GOARCH="+arch, "CGO_ENABLED=0", "GOWORK=off", "GOFLAGS=")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", target, err, out)
	}
	body, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

var fixtureTargets = []string{"windows/amd64", "windows/arm64", "linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64"}

func TestVersionEvidenceRejectsClaimedLdflags(t *testing.T) {
	info := releaseInfo(debug.BuildSetting{Key: "-ldflags", Value: "-s -w " + buildstamp.LDFlag("1.2.3")})
	for _, body := range [][]byte{nil, []byte("1.2.3"), versionFixture(t, "linux/amd64", "1.2.30", "-s -w")} {
		if err := checkBuildInfo(info, body, "pail", false, "1.2.3", "linux", "amd64"); err == nil {
			t.Error("accepted invalid binary using only claimed linker flags")
		}
	}
}

func TestBinaryVersionRejectsMalformedStamps(t *testing.T) {
	marker := []byte(buildstamp.Name + ":1.2.3;")
	for _, target := range fixtureTargets {
		t.Run(target, func(t *testing.T) {
			osName, arch, _ := strings.Cut(target, "/")
			body := versionFixture(t, target, "1.2.3", "-s -w")
			info, err := buildinfo.Read(bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			at := bytes.Index(body, marker)
			if at < 0 || bytes.Count(body, marker) != 1 {
				t.Fatal("fixture lacks exactly one stamp")
			}
			unterminated := bytes.Clone(body)
			unterminated[at+len(marker)-1] = 'x'
			badChar := bytes.Clone(body)
			badChar[at+len(marker)-2] = ' '
			second := bytes.Clone(body)
			copy(second[len(second)/2:], marker)
			for name, broken := range map[string][]byte{
				"renamed":       bytes.ReplaceAll(body, []byte(buildstamp.Name+":"), []byte("pail-release-versiox:")),
				"unterminated":  unterminated,
				"bad character": badChar,
				"duplicate":     second,
				"bad header":    append(make([]byte, 16), body[16:]...),
			} {
				if err := checkBuildInfo(info, broken, "pail", false, "1.2.3", osName, arch); err == nil {
					t.Errorf("accepted %s stamp", name)
				}
			}
			otherArch := "amd64"
			if arch == otherArch {
				otherArch = "arm64"
			}
			if _, err := binaryVersion(body, osName, otherArch); err == nil {
				t.Error("accepted wrong machine architecture")
			}
		})
	}
}

func TestTrimpathVersionUsesStamp(t *testing.T) {
	for _, target := range fixtureTargets {
		t.Run(target, func(t *testing.T) {
			osName, arch, _ := strings.Cut(target, "/")
			for _, tc := range []struct {
				name, version, flags string
				accept               bool
			}{
				{"stripped", "1.2.3", "-s -w", true},
				{"symbols", "1.2.3", "-w", true},
				{"prefix", "1.2.30", "-s -w", false},
				{"suffix", "1.2.3-dirty", "-s -w", false},
				{"unstamped", "", "-s -w", false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					body := versionFixture(t, target, tc.version, tc.flags)
					info, err := buildinfo.Read(bytes.NewReader(body))
					if err != nil {
						t.Fatal(err)
					}
					err = checkBuildInfo(info, body, "pail", false, "1.2.3", osName, arch)
					if (err == nil) != tc.accept {
						t.Errorf("version=%q flags=%q: err=%v, want accepted=%v", tc.version, tc.flags, err, tc.accept)
					}
					if tc.accept {
						for _, n := range []int{0, 2, 16, len(body) / 2} {
							if err := checkBuildInfo(info, body[:n], "pail", false, "1.2.3", osName, arch); err == nil {
								t.Errorf("accepted truncated %s binary: %d/%d bytes", target, n, len(body))
							}
						}
					}
				})
			}
		})
	}
}
