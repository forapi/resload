//go:build darwin

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func systemOutput(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Output()
}

func detectResources() (resources, error) {
	b, err := systemOutput("/usr/sbin/sysctl", "-n", "hw.memsize")
	if err != nil {
		return resources{}, err
	}
	total, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return resources{}, err
	}
	return resources{cores: float64(runtime.NumCPU()), memory: total, source: "logical CPUs / physical memory"}, nil
}

func macAvailable() (uint64, error) {
	b, err := systemOutput("/usr/bin/vm_stat")
	if err != nil {
		return 0, err
	}
	lines := strings.Split(string(b), "\n")
	if len(lines) == 0 {
		return 0, fmt.Errorf("empty vm_stat output")
	}
	var pageSize uint64
	fields := strings.Fields(lines[0])
	for i, s := range fields {
		if s == "of" && i+1 < len(fields) {
			pageSize, _ = strconv.ParseUint(fields[i+1], 10, 64)
			break
		}
	}
	if pageSize == 0 {
		return 0, fmt.Errorf("could not parse vm_stat page size")
	}
	var pages uint64
	for _, line := range lines[1:] {
		f := strings.SplitN(line, ":", 2)
		if len(f) != 2 {
			continue
		}
		if f[0] == "Pages free" || f[0] == "Pages inactive" || f[0] == "Pages speculative" {
			n, e := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(f[1]), "."), 10, 64)
			if e != nil {
				return 0, e
			}
			pages += n
		}
	}
	return pages * pageSize, nil
}

func sampleProcess() (processSample, error) {
	cpu, err := processCPUTime()
	if err != nil {
		return processSample{}, err
	}
	b, err := systemOutput("/bin/ps", "-o", "rss=", "-p", strconv.Itoa(os.Getpid()))
	if err != nil {
		return processSample{}, err
	}
	rss, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return processSample{}, err
	}
	available, err := macAvailable()
	if err != nil {
		return processSample{}, err
	}
	return processSample{cpu: cpu, rss: rss * 1024, available: available}, nil
}
