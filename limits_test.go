package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCPUList(t *testing.T) {
	for _, tc := range []struct {
		s string
		n int
	}{{"0", 1}, {"0-3,8,10-11", 7}, {" 2-3\n", 2}} {
		n, err := countCPUList(tc.s)
		if err != nil || n != tc.n {
			t.Errorf("%q => %d, %v", tc.s, n, err)
		}
	}
	for _, s := range []string{"", "-1", "3-2", "0-2,2", "2,1", "0-9999999999", "0-1-2"} {
		if _, err := countCPUList(s); err == nil {
			t.Errorf("accepted malformed CPU list %q", s)
		}
	}
}

func TestCgroupMountResolutionAndAncestors(t *testing.T) {
	groups := "0::/team/app\n2:cpu,cpuacct:/work/task\n3:memory:/work/task\n"
	mounts := "1 0 0:1 / /sys/fs/cgroup rw - cgroup2 cgroup rw\n2 0 0:2 /work /cg/cpu rw - cgroup cgroup rw,cpu,cpuacct\n3 0 0:3 /work /cg/memory rw - cgroup cgroup rw,memory\n"
	dirs := cgroupDirectories(groups, mounts)
	want := []cgroupDir{{"/sys/fs/cgroup/team/app", "v2"}, {"/sys/fs/cgroup/team", "v2"}, {"/sys/fs/cgroup", "v2"}, {"/cg/cpu/task", "cpu"}, {"/cg/cpu", "cpu"}, {"/cg/memory/task", "memory"}, {"/cg/memory", "memory"}}
	if len(dirs) != len(want) {
		t.Fatalf("got %+v, want %+v", dirs, want)
	}
	for i := range want {
		if dirs[i] != want[i] {
			t.Errorf("directory %d: %+v, want %+v", i, dirs[i], want[i])
		}
	}
	ns := cgroupDirectories("0::/\n", "1 0 0:1 /docker/abc /sys/fs/cgroup rw - cgroup2 cgroup rw\n")
	if len(ns) != 1 || ns[0].path != "/sys/fs/cgroup" {
		t.Fatalf("namespace root resolution: %+v", ns)
	}
}

func TestCgroupLimitsAndRemainingMemory(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0755); err != nil {
		t.Fatal(err)
	}
	write := func(dir, name, value string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write(root, "cpu.max", "150000 100000\n")
	write(child, "cpu.max", "max 100000\n")
	write(child, "cpuset.cpus.effective", "0-3\n")
	write(root, "memory.max", "1073741824\n")
	write(root, "memory.current", "805306368\n")
	write(child, "memory.max", "max\n")
	dirs := []cgroupDir{{child, "v2"}, {root, "v2"}}
	if got := cgroupCPUCapacity(dirs, 8); got != 1.5 {
		t.Errorf("parent quota ignored: %f", got)
	}
	total, available := cgroupMemory(dirs, 8<<30, 4<<30)
	if total != 1<<30 || available != 256<<20 {
		t.Errorf("memory limits: total %d, available %d", total, available)
	}
	write(root, "memory.current", "2147483648")
	_, available = cgroupMemory(dirs, 8<<30, 4<<30)
	if available != 0 {
		t.Error("memory remaining underflowed")
	}
	write(root, "cpu.cfs_quota_us", "50000")
	write(root, "cpu.cfs_period_us", "100000")
	write(root, "memory.limit_in_bytes", "536870912")
	write(root, "memory.usage_in_bytes", "134217728")
	if got := cgroupCPUCapacity([]cgroupDir{{root, "cpu"}}, 8); got != 0.5 {
		t.Error("v1 fractional CPU quota ignored")
	}
	total, available = cgroupMemory([]cgroupDir{{root, "memory"}}, 8<<30, 4<<30)
	if total != 512<<20 || available != 384<<20 {
		t.Error("v1 memory limits ignored")
	}
}
