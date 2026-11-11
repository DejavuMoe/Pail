package main

import (
	"bytes"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"encoding/binary"
	"errors"
	"fmt"
)

type versionSection struct{ addr, offset, size uint64 }

// binaryVersion reads the Go string header at main.version, then its contents.
// Release builds retain symbols (-w, without -s); no target code is executed.
func binaryVersion(body []byte, osName, arch string) (string, error) {
	var sections []versionSection
	var address uint64
	found := false
	var order binary.ByteOrder = binary.LittleEndian
	addSymbol := func(name string, value uint64) error {
		if name != "main.version" {
			return nil
		}
		if found {
			return errors.New("duplicate main.version symbol")
		}
		address, found = value, true
		return nil
	}
	switch osName {
	case "windows":
		f, err := pe.NewFile(bytes.NewReader(body))
		if err != nil {
			return "", err
		}
		h, ok := f.OptionalHeader.(*pe.OptionalHeader64)
		if !ok || (arch != "amd64" || f.Machine != pe.IMAGE_FILE_MACHINE_AMD64) && (arch != "arm64" || f.Machine != pe.IMAGE_FILE_MACHINE_ARM64) {
			return "", errors.New("incorrect PE architecture")
		}
		for _, s := range f.Sections {
			addr := h.ImageBase + uint64(s.VirtualAddress)
			if addr < h.ImageBase {
				return "", errors.New("overflowing PE address")
			}
			sections = append(sections, versionSection{addr, uint64(s.Offset), uint64(s.Size)})
		}
		for _, s := range f.Symbols {
			if s.Name == "main.version" {
				if s.SectionNumber <= 0 || int(s.SectionNumber) > len(sections) {
					return "", errors.New("invalid main.version section")
				}
				section := sections[s.SectionNumber-1]
				if uint64(s.Value) >= section.size || section.addr+uint64(s.Value) < section.addr {
					return "", errors.New("invalid main.version address")
				}
				if err := addSymbol(s.Name, section.addr+uint64(s.Value)); err != nil {
					return "", err
				}
			}
		}
	case "linux":
		f, err := elf.NewFile(bytes.NewReader(body))
		if err != nil {
			return "", err
		}
		if f.Class != elf.ELFCLASS64 || (arch != "amd64" || f.Machine != elf.EM_X86_64) && (arch != "arm64" || f.Machine != elf.EM_AARCH64) {
			return "", errors.New("incorrect ELF architecture")
		}
		order = f.ByteOrder
		for _, s := range f.Sections {
			if s.Type != elf.SHT_NOBITS {
				sections = append(sections, versionSection{s.Addr, s.Offset, s.FileSize})
			}
		}
		syms, err := f.Symbols()
		if err != nil {
			return "", err
		}
		for _, s := range syms {
			if s.Name == "main.version" && (s.Size != 16 || s.Section == elf.SHN_UNDEF) {
				return "", errors.New("invalid main.version symbol")
			}
			if err := addSymbol(s.Name, s.Value); err != nil {
				return "", err
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
		order = f.ByteOrder
		for _, load := range f.Loads {
			if s, ok := load.(*macho.Segment); ok {
				sections = append(sections, versionSection{s.Addr, s.Offset, s.Filesz})
			}
		}
		if f.Symtab != nil {
			for _, s := range f.Symtab.Syms {
				if err := addSymbol(s.Name, s.Value); err != nil {
					return "", err
				}
			}
		}
	default:
		return "", fmt.Errorf("unsupported binary platform %s", osName)
	}
	if !found {
		return "", errors.New("missing main.version symbol (build with -w, without -s)")
	}
	for _, s := range sections {
		if s.offset > uint64(len(body)) || s.size > uint64(len(body))-s.offset {
			return "", errors.New("truncated binary section")
		}
	}
	read := func(addr, size uint64) ([]byte, error) {
		for _, s := range sections {
			if addr >= s.addr && addr-s.addr <= s.size && size <= s.size-(addr-s.addr) {
				offset := s.offset + (addr - s.addr)
				return body[offset : offset+size], nil
			}
		}
		return nil, errors.New("main.version points outside file-backed data")
	}
	header, err := read(address, 16)
	if err != nil {
		return "", err
	}
	value, err := read(order.Uint64(header[:8]), order.Uint64(header[8:]))
	if err != nil {
		return "", err
	}
	return string(value), nil
}
