package main

import (
	"bytes"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"errors"
	"fmt"

	"github.com/DejavuMoe/Pail/internal/buildstamp"
)

type fileRange struct{ offset, size uint64 }

// binaryVersion reads the release marker that internal/buildstamp stores in
// the executable. The executable must be of the expected format and
// architecture, the marker "pail-release-version:<v>;" must occur exactly
// once and lie inside a file-backed section, and <v> is what the programs
// report. Stripped builds (-s -w) keep the marker; no target code is executed.
func binaryVersion(body []byte, osName, arch string) (string, error) {
	var ranges []fileRange
	switch osName {
	case "windows":
		f, err := pe.NewFile(bytes.NewReader(body))
		if err != nil {
			return "", err
		}
		if _, ok := f.OptionalHeader.(*pe.OptionalHeader64); !ok || (arch != "amd64" || f.Machine != pe.IMAGE_FILE_MACHINE_AMD64) && (arch != "arm64" || f.Machine != pe.IMAGE_FILE_MACHINE_ARM64) {
			return "", errors.New("incorrect PE architecture")
		}
		for _, s := range f.Sections {
			ranges = append(ranges, fileRange{uint64(s.Offset), uint64(s.Size)})
		}
	case "linux":
		f, err := elf.NewFile(bytes.NewReader(body))
		if err != nil {
			return "", err
		}
		if f.Class != elf.ELFCLASS64 || (arch != "amd64" || f.Machine != elf.EM_X86_64) && (arch != "arm64" || f.Machine != elf.EM_AARCH64) {
			return "", errors.New("incorrect ELF architecture")
		}
		for _, s := range f.Sections {
			if s.Type != elf.SHT_NOBITS {
				ranges = append(ranges, fileRange{s.Offset, s.FileSize})
			}
		}
	case "darwin":
		f, err := macho.NewFile(bytes.NewReader(body))
		if err != nil {
			return "", err
		}
		if f.Magic != macho.Magic64 || (arch != "amd64" || f.Cpu != macho.CpuAmd64) && (arch != "arm64" || f.Cpu != macho.CpuArm64) {
			return "", errors.New("incorrect Mach-O architecture")
		}
		for _, load := range f.Loads {
			if s, ok := load.(*macho.Segment); ok {
				ranges = append(ranges, fileRange{s.Offset, s.Filesz})
			}
		}
	default:
		return "", fmt.Errorf("unsupported binary platform %s", osName)
	}
	for _, r := range ranges {
		if r.offset > uint64(len(body)) || r.size > uint64(len(body))-r.offset {
			return "", errors.New("truncated binary section")
		}
	}
	marker := []byte(buildstamp.Name + ":")
	switch n := bytes.Count(body, marker); {
	case n == 0:
		return "", errors.New("missing release version stamp")
	case n > 1:
		return "", errors.New("duplicate release version stamp")
	}
	at := bytes.Index(body, marker)
	end := bytes.IndexByte(body[at:min(len(body), at+len(marker)+65)], ';')
	if end < 0 {
		return "", errors.New("unterminated release version stamp")
	}
	inside := false
	for _, r := range ranges {
		if uint64(at) >= r.offset && uint64(at+end) < r.offset+r.size {
			inside = true
		}
	}
	if !inside {
		return "", errors.New("release version stamp outside file-backed data")
	}
	v, ok := buildstamp.Parse(string(body[at : at+end+1]))
	if !ok {
		return "", errors.New("malformed release version stamp")
	}
	return v, nil
}
