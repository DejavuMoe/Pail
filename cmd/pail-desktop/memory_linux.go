package main

import (
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func readProcessMemory() processMemory {
	p := processMemory{}
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		p.Error = err.Error()
	} else {
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) < 2 {
				continue
			}
			n, _ := strconv.ParseUint(f[1], 10, 64)
			switch f[0] {
			case "VmRSS:":
				p.ResidentBytes = n * 1024
			case "Threads:":
				p.Threads = uint32(n)
			}
		}
	}
	var usage unix.Rusage
	if err := unix.Getrusage(unix.RUSAGE_SELF, &usage); err != nil {
		p.Error += " Getrusage: " + err.Error()
	} else {
		p.CPUTimeNS = uint64(usage.Utime.Nano() + usage.Stime.Nano())
	}
	return p
}
