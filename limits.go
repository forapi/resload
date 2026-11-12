package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

func countCPUList(s string) (int, error) {
	count := 0
	last := -1
	for _, part := range strings.Split(strings.TrimSpace(s), ",") {
		pair := strings.Split(part, "-")
		if len(pair) > 2 {
			return 0, fmt.Errorf("invalid CPU list %q", s)
		}
		lo, err := strconv.Atoi(pair[0])
		if err != nil || lo < 0 {
			return 0, fmt.Errorf("invalid CPU list %q", s)
		}
		hi := lo
		if len(pair) == 2 {
			hi, err = strconv.Atoi(pair[1])
			if err != nil || hi < lo {
				return 0, fmt.Errorf("invalid CPU list %q", s)
			}
		}
		if lo <= last || hi > 1<<20 {
			return 0, fmt.Errorf("unordered or oversized CPU list %q", s)
		}
		count += hi - lo + 1
		last = hi
	}
	return count, nil
}

type cgroupDir struct{ path, kind string }

func mountUnescape(s string) string {
	return strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace(s)
}

// Resolve actual mount roots, rather than assuming /sys/fs/cgroup or the root
// cgroup. Include ancestors because a parent can impose the tighter limit.
func cgroupDirectories(groups, mounts string) []cgroupDir {
	membership := map[string]string{}
	for _, line := range strings.Split(groups, "\n") {
		f := strings.SplitN(line, ":", 3)
		if len(f) != 3 {
			continue
		}
		if f[1] == "" {
			membership["v2"] = f[2]
		} else {
			for _, controller := range strings.Split(f[1], ",") {
				membership[controller] = f[2]
			}
		}
	}
	var result []cgroupDir
	seen := map[string]bool{}
	for _, line := range strings.Split(mounts, "\n") {
		halves := strings.SplitN(line, " - ", 2)
		if len(halves) != 2 {
			continue
		}
		left, right := strings.Fields(halves[0]), strings.Fields(halves[1])
		if len(left) < 5 || len(right) < 3 {
			continue
		}
		var kinds []string
		if right[0] == "cgroup2" {
			kinds = []string{"v2"}
		} else if right[0] == "cgroup" {
			for _, v := range strings.Split(right[2], ",") {
				if v == "cpu" || v == "memory" {
					kinds = append(kinds, v)
				}
			}
		}
		root, mount := path.Clean(mountUnescape(left[3])), path.Clean(mountUnescape(left[4]))
		for _, kind := range kinds {
			group, ok := membership[kind]
			if !ok {
				continue
			}
			group = path.Clean(group)
			rel := ""
			if root == "/" {
				rel = strings.TrimPrefix(group, "/")
			} else if group == root {
				rel = ""
			} else if strings.HasPrefix(group, root+"/") {
				rel = strings.TrimPrefix(group, root+"/")
			} else if group != "/" {
				rel = strings.TrimPrefix(group, "/")
			}
			dir := path.Join(mount, rel)
			for {
				key := kind + ":" + dir
				if !seen[key] {
					result = append(result, cgroupDir{dir, kind})
					seen[key] = true
				}
				if dir == mount {
					break
				}
				parent := path.Dir(dir)
				if parent == dir || (parent != mount && !strings.HasPrefix(parent, mount+"/")) {
					break
				}
				dir = parent
			}
		}
	}
	return result
}

func readUintFile(dir, name string) (uint64, bool) {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return 0, false
	}
	n, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	return n, err == nil
}

func cgroupCPUCapacity(dirs []cgroupDir, cores float64) float64 {
	for _, d := range dirs {
		var quota, period float64
		if d.kind == "v2" {
			if b, e := os.ReadFile(filepath.Join(d.path, "cpu.max")); e == nil {
				f := strings.Fields(string(b))
				if len(f) == 2 {
					quota, _ = strconv.ParseFloat(f[0], 64)
					period, _ = strconv.ParseFloat(f[1], 64)
				}
			}
			if b, e := os.ReadFile(filepath.Join(d.path, "cpuset.cpus.effective")); e == nil {
				if n, e := countCPUList(string(b)); e == nil && float64(n) < cores {
					cores = float64(n)
				}
			}
		} else if d.kind == "cpu" {
			if q, ok := readUintFile(d.path, "cpu.cfs_quota_us"); ok {
				quota = float64(q)
			}
			if p, ok := readUintFile(d.path, "cpu.cfs_period_us"); ok {
				period = float64(p)
			}
		}
		if quota > 0 && period > 0 && quota/period < cores {
			cores = quota / period
		}
	}
	return cores
}

func cgroupMemory(dirs []cgroupDir, total, available uint64) (uint64, uint64) {
	for _, d := range dirs {
		limitName, usageName := "memory.max", "memory.current"
		if d.kind == "memory" {
			limitName, usageName = "memory.limit_in_bytes", "memory.usage_in_bytes"
		} else if d.kind != "v2" {
			continue
		}
		limit, ok := readUintFile(d.path, limitName)
		if !ok {
			continue
		}
		if limit < total {
			total = limit
		}
		if usage, ok := readUintFile(d.path, usageName); ok {
			remaining := uint64(0)
			if usage < limit {
				remaining = limit - usage
			}
			if remaining < available {
				available = remaining
			}
		}
	}
	return total, min(total, available)
}
