package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var getProcessMemoryInfo = windows.NewLazySystemDLL("psapi.dll").NewProc("GetProcessMemoryInfo")

func readProcessMemory() processMemory {
	// PROCESS_MEMORY_COUNTERS_EX; SIZE_T follows the target pointer width.
	var counters struct {
		Size, Faults                                                                                    uint32
		PeakWorking, Working, PeakPaged, Paged, PeakNonPaged, NonPaged, Pagefile, PeakPagefile, Private uintptr
	}
	counters.Size = uint32(unsafe.Sizeof(counters))
	p := processMemory{}
	if ok, _, err := getProcessMemoryInfo.Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&counters)), uintptr(counters.Size)); ok == 0 {
		p.Error = err.Error()
	} else {
		p.ResidentBytes, p.PrivateBytes = uint64(counters.Working), uint64(counters.Private)
	}
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(windows.CurrentProcess(), &created, &exited, &kernel, &user); err == nil {
		p.CPUTimeNS = ((uint64(kernel.HighDateTime)<<32 | uint64(kernel.LowDateTime)) + (uint64(user.HighDateTime)<<32 | uint64(user.LowDateTime))) * 100
	} else {
		p.Error += " GetProcessTimes: " + err.Error()
	}
	h, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		p.Error += " process snapshot: " + err.Error()
		return p
	}
	defer windows.CloseHandle(h)
	e := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err = windows.Process32First(h, &e); err == nil; err = windows.Process32Next(h, &e) {
		if e.ProcessID == windows.GetCurrentProcessId() {
			p.Threads = e.Threads
			return p
		}
	}
	p.Error += " current process missing from snapshot"
	return p
}
