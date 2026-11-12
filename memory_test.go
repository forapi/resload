package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
)

func TestMemoryAllocationTouchShrinkAndRelease(t *testing.T) {
	live := map[*byte]int{}
	m := &memoryLoad{seed: 123, allocate: func(n int) ([]byte, error) { b := make([]byte, n); live[&b[0]] = n; return b, nil }, release: func(b []byte) error {
		if _, ok := live[&b[0]]; !ok {
			t.Error("double free or unknown memory block")
		}
		delete(live, &b[0])
		return nil
	}}
	ctx := context.Background()
	const target = memoryChunk + 123
	for m.bytes < pageRound(target) {
		if err := m.step(ctx, target, 1<<30, 0); err != nil {
			t.Fatal(err)
		}
	}
	if m.bytes != pageRound(target) || len(live) != 2 {
		t.Fatalf("unexpected allocation %d, blocks %d", m.bytes, len(live))
	}
	for _, b := range m.blocks {
		for i := 0; i < len(b); i += os.Getpagesize() {
			if bytes.Equal(b[i:i+32], make([]byte, 32)) {
				t.Fatalf("page at %d was never touched", i)
			}
		}
	}
	if err := m.step(ctx, 4096, 1<<30, 0); err != nil {
		t.Fatal(err)
	}
	if m.bytes != pageRound(4096) {
		t.Fatalf("shrink failed: %d", m.bytes)
	}
	if err := m.step(ctx, 0, 1<<30, 0); err != nil {
		t.Fatal(err)
	}
	if m.bytes != 0 || len(live) != 0 {
		t.Fatal("zero target retained memory")
	}
	if err := m.close(); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryHeadroomFailureAndCancellation(t *testing.T) {
	allocations := 0
	m := &memoryLoad{seed: 1, allocate: func(n int) ([]byte, error) { allocations++; return nil, errors.New("test allocation failure") }, release: func([]byte) error { return nil }}
	if err := m.step(context.Background(), 4096, 4096, 1); err == nil {
		t.Error("ignored memory reserve")
	}
	if allocations != 0 {
		t.Fatal("allocation attempted without headroom")
	}
	if err := m.step(context.Background(), 4096, 8192, 0); err == nil {
		t.Error("allocation error was lost")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	before := allocations
	if err := m.step(ctx, 4096, 8192, 0); err != nil {
		t.Fatal(err)
	}
	if allocations != before {
		t.Fatal("allocated after cancellation")
	}
}

func TestNativeMemoryMapping(t *testing.T) {
	b, err := allocateMemory(os.Getpagesize() * 2)
	if err != nil {
		t.Fatal(err)
	}
	for i := range b {
		b[i] = byte(i % 251)
	}
	if b[len(b)-1] != byte((len(b)-1)%251) {
		t.Fatal("native mapping is not writable")
	}
	if err := releaseMemory(b); err != nil {
		t.Fatal(err)
	}
}
