package main

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

type cpuLoad struct {
	duty    atomic.Uint64
	sink    atomic.Uint64
	wg      sync.WaitGroup
	workers int
	cycle   time.Duration
	command float64
}

func newCPULoad(ctx context.Context, workers int, cycle time.Duration) *cpuLoad {
	c := &cpuLoad{workers: workers, cycle: cycle}
	for i := 0; i < workers; i++ {
		c.wg.Add(1)
		go c.worker(ctx, i)
	}
	return c
}

func (c *cpuLoad) set(cores float64) {
	c.command = math.Max(0, math.Min(float64(c.workers), cores))
	c.duty.Store(math.Float64bits(c.command))
}

func (c *cpuLoad) adjust(target, measured, seconds float64) {
	if target == 0 {
		c.set(0)
		return
	}
	// Correct duty based on *total process* CPU, including allocation and monitoring.
	// Bounded integral gain avoids abrupt oscillations and winds up only to worker count.
	gain := math.Min(0.5, seconds*0.35)
	c.set(c.command + gain*(target-measured))
}

func (c *cpuLoad) worker(ctx context.Context, id int) {
	defer c.wg.Done()
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	wait := func(d time.Duration) bool {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(d)
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return true
		}
	}
	// Distribute worker phases to reduce synchronized CPU bursts.
	if !wait(time.Duration(id) * c.cycle / time.Duration(c.workers)) {
		return
	}
	x := uint64(id + 1)
	for ctx.Err() == nil {
		start := time.Now()
		// Fill whole cores first, then use one fractional worker. Splitting a
		// small target over every core creates tiny bursts and scheduler overhead.
		duty := math.Max(0, math.Min(1, math.Float64frombits(c.duty.Load())-float64(id)))
		busy := time.Duration(duty * float64(c.cycle))
		deadline := start.Add(busy)
		for busy > 0 && time.Now().Before(deadline) {
			for j := 0; j < 2048; j++ {
				x ^= x << 13
				x ^= x >> 7
				x ^= x << 17
			}
			if ctx.Err() != nil {
				return
			}
		}
		c.sink.Add(x) // Observable result prevents removal of the arithmetic loop.
		if remaining := time.Until(start.Add(c.cycle)); remaining > 0 {
			if !wait(remaining) {
				return
			}
		}
	}
}

type linearRamp struct {
	from, to float64
	start    time.Time
	duration time.Duration
}

func (r linearRamp) at(now time.Time) float64 {
	if r.duration == 0 || !now.Before(r.start.Add(r.duration)) {
		return r.to
	}
	f := float64(now.Sub(r.start)) / float64(r.duration)
	return r.from + (r.to-r.from)*math.Max(0, f)
}
