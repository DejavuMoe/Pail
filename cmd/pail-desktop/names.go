package main

import "strings"

// compareNames preserves Compare(ToLower(a), ToLower(b)) without allocating
// lowercase copies for every ASCII sort comparison. Unicode keeps that rule.
func compareNames(a, b string) int {
	for i := 0; i < min(len(a), len(b)); i++ {
		x, y := a[i], b[i]
		if x >= 0x80 || y >= 0x80 {
			return strings.Compare(strings.ToLower(a), strings.ToLower(b))
		}
		if x >= 'A' && x <= 'Z' {
			x += 'a' - 'A'
		}
		if y >= 'A' && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
	}
	return cmpInt(int64(len(a)), int64(len(b)))
}
