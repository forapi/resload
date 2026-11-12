package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
)

const version = "1.0.0"

type config struct {
	cpu, cores, memory, reserve       string
	duration, interval, ramp, cycle   time.Duration
	json, quiet, noStdin, showVersion bool
}

func parseFlags(args []string, out io.Writer) (config, error) {
	c := config{}
	f := flag.NewFlagSet("resload", flag.ContinueOnError)
	f.SetOutput(out)
	f.StringVar(&c.cpu, "cpu", "0%", "percentage of available CPU capacity, e.g. 30%")
	f.StringVar(&c.cores, "cpu-cores", "", "CPU capacity in logical cores, e.g. 1.5 (exclusive with --cpu)")
	f.StringVar(&c.memory, "memory", "0", "additional touched memory, e.g. 512MiB, 2GiB, 20%")
	f.StringVar(&c.reserve, "reserve", "64MiB", "memory headroom required while allocating; 0 disables")
	f.DurationVar(&c.duration, "duration", 0, "total runtime including ramp; 0 runs until stopped")
	f.DurationVar(&c.interval, "interval", time.Second, "status and CPU feedback interval (at least 200ms)")
	f.DurationVar(&c.ramp, "ramp", 3*time.Second, "time to ramp each new target from its previous value")
	f.DurationVar(&c.cycle, "cpu-cycle", 100*time.Millisecond, "CPU duty cycle (10ms to 1s)")
	f.BoolVar(&c.json, "json", false, "emit newline-delimited JSON status")
	f.BoolVar(&c.quiet, "quiet", false, "suppress status output")
	f.BoolVar(&c.noStdin, "no-stdin", false, "disable runtime commands from standard input")
	f.BoolVar(&c.showVersion, "version", false, "print version and exit")
	f.Usage = func() {
		fmt.Fprintln(out, "Usage: resload --cpu 30% --memory 2GiB [options]")
		f.PrintDefaults()
		fmt.Fprintln(out, "Commands: cpu 20% | cpu-cores 1.5 | memory 512MiB | status | quit")
	}
	if err := f.Parse(args); err != nil {
		return c, err
	}
	if f.NArg() != 0 {
		return c, fmt.Errorf("unexpected argument: %s", f.Arg(0))
	}
	if c.showVersion {
		return c, nil
	}
	if c.cores != "" {
		conflict := false
		f.Visit(func(v *flag.Flag) {
			if v.Name == "cpu" {
				conflict = true
			}
		})
		if conflict {
			return c, errors.New("--cpu and --cpu-cores are mutually exclusive")
		}
	}
	if c.duration < 0 || c.ramp < 0 {
		return c, errors.New("duration and ramp must be nonnegative")
	}
	if c.interval < 200*time.Millisecond {
		return c, errors.New("interval must be at least 200ms")
	}
	if c.cycle < 10*time.Millisecond || c.cycle > time.Second {
		return c, errors.New("cpu-cycle must be between 10ms and 1s")
	}
	return c, nil
}

func finiteNumber(s string) (float64, error) {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return 0, fmt.Errorf("invalid nonnegative number: %q", s)
	}
	return v, nil
}

func percentage(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if !strings.HasSuffix(s, "%") {
		return 0, fmt.Errorf("percentage must end in %%: %q", s)
	}
	v, err := finiteNumber(strings.TrimSuffix(s, "%"))
	if err != nil {
		return 0, err
	}
	if v > 100 {
		return 0, errors.New("percentage must be between 0% and 100%")
	}
	return v / 100, nil
}

func cpuTarget(value string, cores bool, capacity float64) (float64, error) {
	var n float64
	var err error
	if cores {
		n, err = finiteNumber(value)
	} else {
		n, err = percentage(value)
		n *= capacity
	}
	if err != nil {
		return 0, err
	}
	if n > capacity+1e-9 {
		return 0, fmt.Errorf("CPU target %.3f exceeds available capacity %.3f cores", n, capacity)
	}
	return n, nil
}

func memoryTarget(value string, total uint64, allowPercent bool) (uint64, error) {
	s := strings.TrimSpace(value)
	if strings.HasSuffix(s, "%") {
		if !allowPercent {
			return 0, errors.New("percentage is not allowed here")
		}
		p, err := percentage(s)
		if err != nil {
			return 0, err
		}
		if total == 0 {
			return 0, errors.New("cannot determine memory limit for percentage")
		}
		return uint64(float64(total) * p), nil
	}
	units := []struct {
		suffix string
		scale  float64
	}{
		{"TIB", 1 << 40}, {"GIB", 1 << 30}, {"MIB", 1 << 20}, {"KIB", 1 << 10},
		{"TB", 1e12}, {"GB", 1e9}, {"MB", 1e6}, {"KB", 1e3}, {"B", 1},
	}
	scale := float64(1)
	u := strings.ToUpper(s)
	for _, unit := range units {
		if strings.HasSuffix(u, unit.suffix) {
			s = strings.TrimSpace(s[:len(s)-len(unit.suffix)])
			scale = unit.scale
			break
		}
	}
	n, err := finiteNumber(s)
	if err != nil {
		return 0, err
	}
	n *= scale
	// A conservative bound also keeps arithmetic and float-to-int conversion defined.
	if math.IsInf(n, 0) || n >= float64(uint64(1)<<63) {
		return 0, errors.New("memory size is too large")
	}
	return uint64(n), nil
}

func humanBytes(n uint64) string {
	for _, u := range []struct {
		name string
		size uint64
	}{{"TiB", 1 << 40}, {"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}} {
		if n >= u.size {
			return fmt.Sprintf("%.1f%s", float64(n)/float64(u.size), u.name)
		}
	}
	return fmt.Sprintf("%dB", n)
}
