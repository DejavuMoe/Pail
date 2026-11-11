//go:build !pail_pprof

package main

// pprofFlag is a no-op without -tags pail_pprof.
func pprofFlag() func() { return func() {} }
