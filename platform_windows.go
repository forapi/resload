//go:build windows

package main

import (
	"fmt"
	"math/bits"
	"reflect"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

var (
	kernel             = syscall.NewLazyDLL("kernel32.dll")
	virtualAlloc       = kernel.NewProc("VirtualAlloc")
	virtualFree        = kernel.NewProc("VirtualFree")
	globalMemoryStatus = kernel.NewProc("GlobalMemoryStatusEx")
	getProcessTimes    = kernel.NewProc("GetProcessTimes")
	getAffinity        = kernel.NewProc("GetProcessAffinityMask")
	getMemoryInfo      = kernel.NewProc("K32GetProcessMemoryInfo")
)

const currentProcess = ^uintptr(0)

type memoryStatus struct {
	Length, Load                                                                                         uint32
	TotalPhys, AvailPhys, TotalPageFile, AvailPageFile, TotalVirtual, AvailVirtual, AvailExtendedVirtual uint64
}

type processMemoryCounters struct {
	Size, PageFaultCount                                                                 uint32
	PeakWorkingSetSize, WorkingSetSize, QuotaPeakPagedPoolUsage, QuotaPagedPoolUsage     uintptr
	QuotaPeakNonPagedPoolUsage, QuotaNonPagedPoolUsage, PagefileUsage, PeakPagefileUsage uintptr
}

func windowsMemory() (memoryStatus, error) {
	m := memoryStatus{}
	m.Length = uint32(unsafe.Sizeof(m))
	if ok, _, err := globalMemoryStatus.Call(uintptr(unsafe.Pointer(&m))); ok == 0 {
		return m, fmt.Errorf("GlobalMemoryStatusEx: %w", err)
	}
	return m, nil
}

func detectResources() (resources, error) {
	m, err := windowsMemory()
	if err != nil {
		return resources{}, err
	}
	cores := runtime.NumCPU()
	var processMask, systemMask uintptr
	if ok, _, _ := getAffinity.Call(currentProcess, uintptr(unsafe.Pointer(&processMask)), uintptr(unsafe.Pointer(&systemMask))); ok != 0 && processMask != 0 {
		cores = min(cores, bits.OnesCount64(uint64(processMask)))
	}
	return resources{cores: float64(cores), memory: m.TotalPhys, source: "physical memory / process CPU affinity"}, nil
}

func sampleProcess() (processSample, error) {
	var created, exited, kernelTime, userTime syscall.Filetime
	if ok, _, err := getProcessTimes.Call(currentProcess, uintptr(unsafe.Pointer(&created)), uintptr(unsafe.Pointer(&exited)), uintptr(unsafe.Pointer(&kernelTime)), uintptr(unsafe.Pointer(&userTime))); ok == 0 {
		return processSample{}, fmt.Errorf("GetProcessTimes: %w", err)
	}
	ft := func(t syscall.Filetime) uint64 { return uint64(t.HighDateTime)<<32 | uint64(t.LowDateTime) }
	counters := processMemoryCounters{}
	counters.Size = uint32(unsafe.Sizeof(counters))
	if ok, _, err := getMemoryInfo.Call(currentProcess, uintptr(unsafe.Pointer(&counters)), uintptr(counters.Size)); ok == 0 {
		return processSample{}, fmt.Errorf("GetProcessMemoryInfo: %w", err)
	}
	m, err := windowsMemory()
	if err != nil {
		return processSample{}, err
	}
	return processSample{cpu: time.Duration(ft(kernelTime)+ft(userTime)) * 100 * time.Nanosecond, rss: uint64(counters.WorkingSetSize), available: m.AvailPhys}, nil
}

func allocateMemory(size int) ([]byte, error) {
	addr, _, err := virtualAlloc.Call(0, uintptr(size), 0x3000, 0x04) // MEM_RESERVE|MEM_COMMIT, PAGE_READWRITE
	if addr == 0 {
		return nil, err
	}
	// Modify a real slice header: this memory is owned by VirtualAlloc, outside
	// the Go heap, and remains valid until the matching VirtualFree call.
	var b []byte
	h := (*reflect.SliceHeader)(unsafe.Pointer(&b))
	h.Data, h.Len, h.Cap = addr, size, size
	return b, nil
}

func releaseMemory(b []byte) error {
	if len(b) == 0 {
		return nil
	}
	if ok, _, err := virtualFree.Call(uintptr(unsafe.Pointer(&b[0])), 0, 0x8000); ok == 0 {
		return err
	} // MEM_RELEASE
	return nil
}
