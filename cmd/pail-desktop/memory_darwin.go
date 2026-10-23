package main

import (
	"fmt"
	"os"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Layouts from Apple's sys/resource.h (rusage_info_v0) and
// sys/proc_info.h (proc_taskinfo), shared by arm64 and amd64.
type memoryRusage struct {
	UUID                                                                                          [16]byte
	User, System, IdleWakeups, InterruptWakeups, Pageins, Wired, Resident, Footprint, Start, Exit uint64
}

type memoryTaskInfo struct {
	Virtual, Resident, User, System, ThreadsUser, ThreadsSystem                                                    uint64
	Policy, Faults, Pageins, COWFaults, Sent, Received, MachCalls, UnixCalls, Switches, Threads, Running, Priority int32
}

type memoryProcFuncs struct {
	rusage  func(int32, int32, *memoryRusage) int32
	pidinfo func(int32, int32, uint64, *memoryTaskInfo, int32) int32
}

var memoryProcAPI = sync.OnceValues(func() (memoryProcFuncs, error) {
	h, err := purego.Dlopen("/usr/lib/libproc.dylib", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return memoryProcFuncs{}, err
	}
	rusage, err := purego.Dlsym(h, "proc_pid_rusage")
	if err != nil {
		purego.Dlclose(h)
		return memoryProcFuncs{}, err
	}
	pidinfo, err := purego.Dlsym(h, "proc_pidinfo")
	if err != nil {
		purego.Dlclose(h)
		return memoryProcFuncs{}, err
	}
	var r func(int32, int32, *memoryRusage) int32
	var p func(int32, int32, uint64, *memoryTaskInfo, int32) int32
	purego.RegisterFunc(&r, rusage)
	purego.RegisterFunc(&p, pidinfo)
	return memoryProcFuncs{r, p}, nil
})

func readProcessMemory() processMemory {
	api, err := memoryProcAPI()
	if err != nil {
		return processMemory{Error: err.Error()}
	}
	p := processMemory{}
	var r memoryRusage
	if code := api.rusage(int32(os.Getpid()), 0, &r); code == 0 {
		p.ResidentBytes, p.FootprintBytes, p.CPUTimeNS = r.Resident, r.Footprint, r.User+r.System
	} else {
		p.Error = fmt.Sprintf("proc_pid_rusage: %d", code)
	}
	var ti memoryTaskInfo
	if n := api.pidinfo(int32(os.Getpid()), 4, 0, &ti, int32(unsafe.Sizeof(ti))); n == int32(unsafe.Sizeof(ti)) {
		p.Threads = uint32(ti.Threads)
	} else {
		p.Error += fmt.Sprintf(" proc_pidinfo: %d", n)
	}
	return p
}
