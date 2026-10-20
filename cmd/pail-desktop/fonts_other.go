//go:build !linux

package main

// uiFont is empty off Linux: the theme keeps the system's interface font
// (Segoe UI, SF), and no font is embedded in these binaries.
const uiFont = ""

func registerFonts() {}
