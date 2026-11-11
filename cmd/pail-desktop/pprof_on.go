//go:build pail_pprof

package main

import (
	"flag"
	"log"
	"net"
	"net/http"
	_ "net/http/pprof" // registers /debug/pprof/ on http.DefaultServeMux
)

// pprofFlag registers --pprof ADDR: serve net/http/pprof on a loopback
// address for profiling CPU, memory and goroutines. Only builds made with
// -tags pail_pprof have it; release builds do not.
func pprofFlag() func() {
	addr := flag.String("pprof", "", "serve net/http/pprof on this loopback address, e.g. 127.0.0.1:6060")
	return func() {
		if *addr == "" {
			return
		}
		host, _, err := net.SplitHostPort(*addr)
		if ip := net.ParseIP(host); err != nil || ip == nil || !ip.IsLoopback() {
			log.Fatalf("--pprof needs a loopback address, got %q", *addr)
		}
		go func() { log.Print(http.ListenAndServe(*addr, nil)) }()
	}
}
