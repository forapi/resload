//go:build linux

package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
)

var linuxGroups []cgroupDir

func linuxMemory() (uint64, uint64, error) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, err
	}
	values := map[string]uint64{}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		n, _ := strconv.ParseUint(f[1], 10, 64)
		values[strings.TrimSuffix(f[0], ":")] = n * 1024
	}
	total := values["MemTotal"]
	available, ok := values["MemAvailable"]
	if !ok {
		available = values["MemFree"] + values["Buffers"] + values["Cached"]
	}
	if total == 0 {
		return 0, 0, fmt.Errorf("could not read MemTotal from /proc/meminfo")
	}
	total, available = cgroupMemory(linuxGroups, total, available)
	return total, available, nil
}

func detectResources() (resources, error) {
	groups, _ := os.ReadFile("/proc/self/cgroup")
	mounts, _ := os.ReadFile("/proc/self/mountinfo")
	linuxGroups = cgroupDirectories(string(groups), string(mounts))
	cores := float64(runtime.NumCPU())
	if b, err := os.ReadFile("/proc/self/status"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "Cpus_allowed_list:") {
				if n, e := countCPUList(strings.TrimPrefix(line, "Cpus_allowed_list:")); e == nil {
					cores = min(cores, float64(n))
				}
			}
		}
	}
	cores = cgroupCPUCapacity(linuxGroups, cores)
	total, _, err := linuxMemory()
	if err != nil {
		return resources{}, err
	}
	return resources{cores: cores, memory: total, source: "CPU affinity / physical memory / cgroup v1 or v2 limits"}, nil
}

func sampleProcess() (processSample, error) {
	cpu, err := processCPUTime()
	if err != nil {
		return processSample{}, err
	}
	b, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return processSample{}, err
	}
	f := strings.Fields(string(b))
	if len(f) < 2 {
		return processSample{}, fmt.Errorf("invalid /proc/self/statm")
	}
	pages, err := strconv.ParseUint(f[1], 10, 64)
	if err != nil {
		return processSample{}, err
	}
	_, available, err := linuxMemory()
	if err != nil {
		return processSample{}, err
	}
	return processSample{cpu: cpu, rss: pages * uint64(os.Getpagesize()), available: available}, nil
}
