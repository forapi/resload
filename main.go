package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"
)

type status struct {
	Type                 string  `json:"type"`
	Elapsed              float64 `json:"elapsed_seconds"`
	CPUCores             float64 `json:"cpu_cores"`
	CPUPercent           float64 `json:"cpu_percent"`
	TargetCPUCores       float64 `json:"target_cpu_cores"`
	AvailableCPUCores    float64 `json:"available_cpu_cores"`
	MemoryBytes          uint64  `json:"memory_bytes"`
	TargetMemoryBytes    uint64  `json:"target_memory_bytes"`
	RSSBytes             uint64  `json:"rss_bytes"`
	MemoryLimitBytes     uint64  `json:"memory_limit_bytes"`
	AvailableMemoryBytes uint64  `json:"available_memory_bytes"`
	PID                  int     `json:"pid"`
	Platform             string  `json:"platform"`
	Reason               string  `json:"reason,omitempty"`
}

type reporter struct {
	out         io.Writer
	json, quiet bool
}

func (r reporter) emit(s status) error {
	if r.quiet {
		return nil
	}
	if r.json {
		return json.NewEncoder(r.out).Encode(s)
	}
	_, err := fmt.Fprintf(r.out, "%-7s %6.1fs  CPU %6.2f%% (%5.2f/%5.2f cores, target %.2f)  memory %s/%s  RSS %s  available %s%s\n",
		s.Type, s.Elapsed, s.CPUPercent, s.CPUCores, s.AvailableCPUCores, s.TargetCPUCores, humanBytes(s.MemoryBytes), humanBytes(s.TargetMemoryBytes), humanBytes(s.RSSBytes), humanBytes(s.AvailableMemoryBytes), reasonSuffix(s.Reason))
	return err
}

func reasonSuffix(reason string) string {
	if reason == "" {
		return ""
	}
	return "  (" + reason + ")"
}

func commands(ctx context.Context, in io.Reader) <-chan string {
	ch := make(chan string)
	go func() {
		defer close(ch)
		s := bufio.NewScanner(in)
		s.Buffer(make([]byte, 4096), 64<<10)
		for s.Scan() {
			select {
			case ch <- strings.TrimSpace(s.Text()):
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch
}

func run(parent context.Context, c config, in io.Reader, out, errOut io.Writer) (result error) {
	res, err := detectResources()
	if err != nil {
		return fmt.Errorf("detect resources: %w", err)
	}
	if res.cores <= 0 || res.memory == 0 {
		return errors.New("no available CPU or memory capacity detected")
	}
	// Respect an explicit GOMAXPROCS lower than the OS/container capacity too.
	res.cores = math.Min(res.cores, float64(runtime.GOMAXPROCS(0)))
	cpuValue, coresMode := c.cpu, false
	if c.cores != "" {
		cpuValue, coresMode = c.cores, true
	}
	targetCPU, err := cpuTarget(cpuValue, coresMode, res.cores)
	if err != nil {
		return err
	}
	targetMemory, err := memoryTarget(c.memory, res.memory, true)
	if err != nil {
		return fmt.Errorf("memory: %w", err)
	}
	reserve, err := memoryTarget(c.reserve, res.memory, false)
	if err != nil {
		return fmt.Errorf("reserve: %w", err)
	}
	validateMemory := func(n uint64) error {
		if n > 0 && (reserve >= res.memory || pageRound(n) > res.memory-reserve) {
			return fmt.Errorf("memory target plus reserve exceeds effective memory limit %s", humanBytes(res.memory))
		}
		return nil
	}
	if err = validateMemory(targetMemory); err != nil {
		return err
	}
	if targetCPU == 0 && targetMemory == 0 {
		return errors.New("specify a nonzero --cpu, --cpu-cores, or --memory; use --help for examples")
	}

	ctx, cancel := context.WithCancel(parent)
	if c.duration > 0 {
		var deadlineCancel context.CancelFunc
		ctx, deadlineCancel = context.WithTimeout(ctx, c.duration)
		defer deadlineCancel()
	}
	defer cancel()
	initial, err := sampleProcess()
	if err != nil {
		return fmt.Errorf("sample resources: %w", err)
	}
	start := time.Now()
	lastTime, lastSample := start, initial
	available := initial.available
	memory := newMemoryLoad()
	cpu := newCPULoad(ctx, max(1, int(math.Ceil(res.cores))), c.cycle)
	r := reporter{out: out, json: c.json, quiet: c.quiet}
	cpuRamp := linearRamp{to: targetCPU, start: start, duration: c.ramp}
	memoryRamp := linearRamp{to: float64(targetMemory), start: start, duration: c.ramp}
	lastCPUTarget := float64(0)
	measuredCPU := float64(0)
	reason := "stopped"
	makeStatus := func(kind string) status {
		return status{Type: kind, Elapsed: time.Since(start).Seconds(), CPUCores: measuredCPU, CPUPercent: measuredCPU / res.cores * 100,
			TargetCPUCores: targetCPU, AvailableCPUCores: res.cores, MemoryBytes: memory.bytes, TargetMemoryBytes: targetMemory,
			RSSBytes: lastSample.rss, MemoryLimitBytes: res.memory, AvailableMemoryBytes: available, PID: os.Getpid(), Platform: runtime.GOOS + "/" + runtime.GOARCH}
	}
	defer func() {
		cancel()
		cpu.wg.Wait()
		if err := memory.close(); err != nil && result == nil {
			result = err
		}
		if s, err := sampleProcess(); err == nil {
			lastSample = s
			available = s.available
		}
		if result != nil {
			reason = "error: " + result.Error()
		}
		s := makeStatus("stopped")
		s.Reason = reason
		if err := r.emit(s); err != nil && result == nil {
			result = err
		}
	}()
	if !c.json && !c.quiet {
		fmt.Fprintf(out, "resload %s | PID %d | %s/%s | effective CPU %.3f cores | memory limit %s | reserve %s\nSource: %s\n", version, os.Getpid(), runtime.GOOS, runtime.GOARCH, res.cores, humanBytes(res.memory), humanBytes(reserve), res.source)
		if !c.noStdin {
			fmt.Fprintln(out, "Commands: cpu 20% | cpu-cores 1.5 | memory 512MiB | status | quit")
		}
	}
	if err := r.emit(makeStatus("start")); err != nil {
		return err
	}
	var input <-chan string
	if !c.noStdin {
		input = commands(ctx, in)
	}
	stepTicker := time.NewTicker(50 * time.Millisecond)
	defer stepTicker.Stop()
	feedbackTicker := time.NewTicker(c.interval)
	defer feedbackTicker.Stop()
	refreshTicker := time.NewTicker(5 * time.Second)
	defer refreshTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				reason = "duration reached"
			} else {
				reason = "interrupted"
			}
			return nil
		case <-stepTicker.C:
			now := time.Now()
			currentCPU := cpuRamp.at(now)
			cpu.set(cpu.command + currentCPU - lastCPUTarget)
			lastCPUTarget = currentCPU
			before := memory.bytes
			if err := memory.step(ctx, uint64(memoryRamp.at(now)), available, reserve); err != nil {
				if ctx.Err() != nil {
					continue
				}
				return err
			}
			if memory.bytes >= before {
				available -= min(available, memory.bytes-before)
			} else {
				available += before - memory.bytes
			}
		case <-feedbackTicker.C:
			s, err := sampleProcess()
			if err != nil {
				return fmt.Errorf("sample resources: %w", err)
			}
			now := time.Now()
			elapsed := now.Sub(lastTime).Seconds()
			measuredCPU = math.Max(0, (s.cpu-lastSample.cpu).Seconds()/elapsed)
			lastTime, lastSample = now, s
			available = s.available
			cpu.adjust(cpuRamp.at(now), measuredCPU, elapsed)
			if err := r.emit(makeStatus("status")); err != nil {
				return err
			}
		case <-refreshTicker.C:
			memory.refresh(ctx)
		case line, ok := <-input:
			if !ok {
				input = nil
				continue
			}
			f := strings.Fields(line)
			if len(f) == 0 {
				continue
			}
			now := time.Now()
			var commandErr error
			switch f[0] {
			case "quit", "exit":
				if len(f) != 1 {
					commandErr = errors.New("usage: quit")
					break
				}
				reason = "quit command"
				return nil
			case "status":
				if len(f) != 1 {
					commandErr = errors.New("usage: status")
					break
				}
				if err := r.emit(makeStatus("status")); err != nil {
					return err
				}
			case "help":
				fmt.Fprintln(errOut, "Commands: cpu 20% | cpu-cores 1.5 | memory 512MiB | status | quit")
			case "cpu", "cpu-cores":
				if len(f) != 2 {
					commandErr = fmt.Errorf("usage: %s VALUE", f[0])
					break
				}
				n, e := cpuTarget(f[1], f[0] == "cpu-cores", res.cores)
				if e != nil {
					commandErr = e
					break
				}
				cpuRamp = linearRamp{from: cpuRamp.at(now), to: n, start: now, duration: c.ramp}
				targetCPU = n
				if err := r.emit(makeStatus("target")); err != nil {
					return err
				}
			case "memory":
				if len(f) < 2 {
					commandErr = errors.New("usage: memory SIZE")
					break
				}
				n, e := memoryTarget(strings.Join(f[1:], " "), res.memory, true)
				if e != nil {
					commandErr = e
					break
				}
				if e = validateMemory(n); e != nil {
					commandErr = e
					break
				}
				memoryRampDuration := c.ramp
				// Release reductions promptly; repeatedly refilling a shrinking partial
				// chunk would otherwise cause unnecessary CPU and memory-bandwidth load.
				if n < memory.bytes {
					memoryRampDuration = 0
				}
				memoryRamp = linearRamp{from: float64(memory.bytes), to: float64(n), start: now, duration: memoryRampDuration}
				targetMemory = n
				if err := r.emit(makeStatus("target")); err != nil {
					return err
				}
			default:
				commandErr = fmt.Errorf("unknown command %q; type help", f[0])
			}
			if commandErr != nil {
				fmt.Fprintln(errOut, "command:", commandErr)
			}
		}
	}
}

func main() {
	c, err := parseFlags(os.Args[1:], os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err == nil && c.showVersion {
		fmt.Println("resload", version)
		return
	}
	if err == nil {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		err = run(ctx, c, os.Stdin, os.Stdout, os.Stderr)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "resload:", err)
		os.Exit(1)
	}
}
