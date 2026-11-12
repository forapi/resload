//go:build linux || darwin

package main

import (
	"syscall"
	"time"
)

func allocateMemory(size int) ([]byte, error) {
	return syscall.Mmap(-1, 0, size, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
}

func releaseMemory(b []byte) error { return syscall.Munmap(b) }

func processCPUTime() (time.Duration, error) {
	var r syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &r); err != nil {
		return 0, err
	}
	return time.Duration(r.Utime.Nano() + r.Stime.Nano()), nil
}
