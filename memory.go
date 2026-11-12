package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
)

const memoryChunk = 16 << 20

type memoryLoad struct {
	blocks   [][]byte
	bytes    uint64
	seed     uint64
	allocate func(int) ([]byte, error)
	release  func([]byte) error
}

func newMemoryLoad() *memoryLoad {
	return &memoryLoad{seed: 0x9e3779b97f4a7c15, allocate: allocateMemory, release: releaseMemory}
}

func pageRound(n uint64) uint64 {
	p := uint64(os.Getpagesize())
	return (n + p - 1) / p * p
}

func (m *memoryLoad) step(ctx context.Context, target, available, reserve uint64) error {
	target = pageRound(target)
	for m.bytes > target && len(m.blocks) > 0 {
		i := len(m.blocks) - 1
		b := m.blocks[i]
		if err := m.release(b); err != nil {
			return fmt.Errorf("release memory: %w", err)
		}
		m.bytes -= uint64(len(b))
		m.blocks[i] = nil
		m.blocks = m.blocks[:i]
		available += uint64(len(b))
	}
	if m.bytes >= target || ctx.Err() != nil {
		return nil
	}
	size := min(uint64(memoryChunk), target-m.bytes)
	if available < reserve || size > available-reserve {
		return fmt.Errorf("insufficient memory headroom: need %s, available %s, reserve %s; lower --memory or --reserve", humanBytes(size), humanBytes(available), humanBytes(reserve))
	}
	b, err := m.allocate(int(size))
	if err != nil {
		return fmt.Errorf("allocate %s: %w", humanBytes(size), err)
	}
	// Retain ownership before touching so cancellation/error always releases the mapping.
	m.blocks = append(m.blocks, b)
	m.bytes += uint64(len(b))
	for off := 0; off < len(b); off += 8 {
		if off%(64<<10) == 0 && ctx.Err() != nil {
			return ctx.Err()
		}
		m.seed ^= m.seed << 13
		m.seed ^= m.seed >> 7
		m.seed ^= m.seed << 17
		binary.LittleEndian.PutUint64(b[off:off+8], m.seed)
	}
	return nil
}

func (m *memoryLoad) refresh(ctx context.Context) {
	// Touch one byte per page every few seconds. This encourages residency without
	// turning a memory-only run into a continuous memory-bandwidth benchmark.
	p := os.Getpagesize()
	for _, b := range m.blocks {
		for i := 0; i < len(b); i += p {
			if i%(1<<20) == 0 && ctx.Err() != nil {
				return
			}
			b[i]++
		}
	}
}

func (m *memoryLoad) close() error {
	var first error
	for _, b := range m.blocks {
		if err := m.release(b); err != nil && first == nil {
			first = err
		}
	}
	m.blocks = nil
	m.bytes = 0
	return first
}
