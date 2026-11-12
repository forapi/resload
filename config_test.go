package main

import (
	"io"
	"math"
	"testing"
	"time"
)

func TestMemoryTargets(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  uint64
	}{
		{"512MiB", 512 << 20}, {"1.5 GiB", 1536 << 20}, {"2GB", 2_000_000_000}, {"25%", 1 << 30}, {"0", 0}, {"4096B", 4096}, {"1kib", 1024},
	} {
		got, err := memoryTarget(tc.input, 4<<30, true)
		if err != nil || got != tc.want {
			t.Errorf("%q: got %d, %v; want %d", tc.input, got, err, tc.want)
		}
	}
	for _, s := range []string{"", "NaN", "Inf", "-1GiB", "101%", "1e100GiB", "8EiB", "9223372036854775808", "0x100"} {
		if _, err := memoryTarget(s, 4<<30, true); err == nil {
			t.Errorf("accepted invalid size %q", s)
		}
	}
	if _, err := memoryTarget("20%", 0, true); err == nil {
		t.Error("accepted percentage without memory total")
	}
	if _, err := memoryTarget("20%", 4<<30, false); err == nil {
		t.Error("reserve accepted percentage")
	}
}

func TestCPUAndFlagValidation(t *testing.T) {
	got, err := cpuTarget("25%", false, 8)
	if err != nil || got != 2 {
		t.Fatalf("CPU percentage: %v %v", got, err)
	}
	got, err = cpuTarget("0.25", true, 0.5)
	if err != nil || got != 0.25 {
		t.Fatalf("fractional quota: %v %v", got, err)
	}
	for _, s := range []string{"101%", "-1%", "NaN%", "Inf%", "30"} {
		if _, err := cpuTarget(s, false, 8); err == nil {
			t.Errorf("accepted CPU %q", s)
		}
	}
	if _, err := cpuTarget("2", true, 1.5); err == nil {
		t.Error("accepted target beyond available cores")
	}
	for _, args := range [][]string{
		{"--cpu", "0%", "--cpu-cores", "1"}, {"--duration", "-1s"}, {"--ramp", "-1s"}, {"--interval", "1ms"}, {"--cpu-cycle", "0s"}, {"--cpu-cycle", "2s"}, {"extra"},
	} {
		if _, err := parseFlags(args, io.Discard); err == nil {
			t.Errorf("accepted flags %v", args)
		}
	}
}

func TestFeedbackAndRamp(t *testing.T) {
	c := &cpuLoad{workers: 4}
	c.set(1)
	c.adjust(1, 0.5, 1)
	if c.command <= 1 {
		t.Error("feedback did not increase low CPU load")
	}
	c.adjust(1, 3, 1)
	if c.command >= 1 {
		t.Error("feedback did not reduce excessive CPU load")
	}
	for i := 0; i < 100; i++ {
		c.adjust(4, 0, 1)
	}
	if c.command != 4 {
		t.Error("feedback exceeded saturation bounds")
	}
	c.adjust(0, 0, 1)
	if c.command != 0 {
		t.Error("zero target must stop CPU workers")
	}
	now := time.Now()
	r := linearRamp{from: 1, to: 3, start: now, duration: 2 * time.Second}
	for _, tc := range []struct {
		offset time.Duration
		want   float64
	}{{-time.Second, 1}, {0, 1}, {time.Second, 2}, {2 * time.Second, 3}, {3 * time.Second, 3}} {
		if got := r.at(now.Add(tc.offset)); math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("ramp at %s: %f", tc.offset, got)
		}
	}
}
