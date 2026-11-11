package main

import (
	"bytes"
	"debug/buildinfo"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
)

func versionFixture(t *testing.T, target, version, flags string) []byte {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"go.mod": "module github.com/DejavuMoe/Pail/cmd/pail\n\ngo 1.27.3\n",
		"main.go": `package main
import "fmt"
var version = "dev"
var other = "1.2.3"
func main() { fmt.Println(version, other) }
`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	osName, arch, _ := strings.Cut(target, "/")
	bin := filepath.Join(dir, "fixture")
	ldflags := flags
	if version != "" {
		ldflags += " -X main.version=" + version
	}
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags", ldflags, "-o", bin, ".")
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

func TestVersionEvidenceRejectsClaimedLdflags(t *testing.T) {
	info := releaseInfo(debug.BuildSetting{Key: "-ldflags", Value: "-w -X main.version=1.2.3"})
	for _, body := range [][]byte{nil, []byte("1.2.3"), versionFixture(t, "linux/amd64", "1.2.30", "-w")} {
		if err := checkBuildInfo(info, body, "pail", false, "1.2.3", "linux", "amd64"); err == nil {
			t.Error("accepted invalid binary using only claimed linker flags")
		}
	}
}

func versionHeaderOffset(t *testing.T, body []byte, osName string) int {
	t.Helper()
	switch osName {
	case "windows":
		f, err := pe.NewFile(bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range f.Symbols {
			if s.Name == "main.version" {
				return int(f.Sections[s.SectionNumber-1].Offset + s.Value)
			}
		}
	case "linux":
		f, err := elf.NewFile(bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		syms, err := f.Symbols()
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range syms {
			if s.Name == "main.version" {
				section := f.Sections[s.Section]
				return int(section.Offset + s.Value - section.Addr)
			}
		}
	case "darwin":
		f, err := macho.NewFile(bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range f.Symtab.Syms {
			if s.Name == "main.version" {
				section := f.Sections[s.Sect-1]
				return int(uint64(section.Offset) + s.Value - section.Addr)
			}
		}
	}
	t.Fatal("fixture has no main.version symbol")
	return 0
}

func TestBinaryVersionRejectsMalformedValues(t *testing.T) {
	for _, target := range []string{"windows/amd64", "windows/arm64", "linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64"} {
		t.Run(target, func(t *testing.T) {
			osName, arch, _ := strings.Cut(target, "/")
			body := versionFixture(t, target, "1.2.3", "-w")
			info, err := buildinfo.Read(bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			offset := versionHeaderOffset(t, body, osName)
			for _, field := range []int{0, 8} {
				broken := bytes.Clone(body)
				binary.LittleEndian.PutUint64(broken[offset+field:], ^uint64(0))
				if err := checkBuildInfo(info, broken, "pail", false, "1.2.3", osName, arch); err == nil {
					t.Errorf("accepted overflowing string header at field %d", field)
				}
			}
			for _, broken := range [][]byte{
				bytes.ReplaceAll(body, []byte("main.version"), []byte("main.invalid")),
				append(make([]byte, 16), body[16:]...),
			} {
				if err := checkBuildInfo(info, broken, "pail", false, "1.2.3", osName, arch); err == nil {
					t.Error("accepted missing symbol or invalid executable header")
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

func TestTrimpathVersionUsesMainVariable(t *testing.T) {
	for _, target := range []string{"windows/amd64", "windows/arm64", "linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64"} {
		t.Run(target, func(t *testing.T) {
			osName, arch, _ := strings.Cut(target, "/")
			for _, tc := range []struct {
				name, version, flags string
				accept               bool
			}{
				{"exact", "1.2.3", "-w", true},
				{"prefix", "1.2.30", "-w", false},
				{"unrelated", "", "-w", false},
				{"stripped", "1.2.3", "-s -w", false},
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
						for _, n := range []int{0, 2, 16, len(body) / 2, len(body) - 1} {
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
