package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

func TestRunAllocatesReportsAndReleases(t *testing.T) {
	c, err := parseFlags([]string{"--memory", "4MiB", "--duration", "700ms", "--ramp", "0s", "--interval", "200ms", "--json", "--no-stdin"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run(context.Background(), c, strings.NewReader(""), &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	d := json.NewDecoder(&out)
	seenMemory := false
	var last status
	for d.More() {
		var s status
		if err := d.Decode(&s); err != nil {
			t.Fatal(err)
		}
		if s.Type == "status" && s.MemoryBytes == 4<<20 {
			seenMemory = true
		}
		last = s
	}
	if !seenMemory {
		t.Error("never reported allocated memory")
	}
	if last.Type != "stopped" || last.MemoryBytes != 0 || last.Reason != "duration reached" {
		t.Errorf("bad final status: %+v", last)
	}
}

func TestRuntimeCommandsAndInvalidInput(t *testing.T) {
	c, err := parseFlags([]string{"--memory", "1MiB", "--ramp", "0s", "--json", "--duration", "3s"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	input := "cpu NaN%\nmemory invalid\ncpu 5%\nmemory 2MiB\nstatus\nquit\n"
	if err := run(context.Background(), c, strings.NewReader(input), &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut.String(), "command:") {
		t.Error("invalid command was not reported")
	}
	var s status
	d := json.NewDecoder(&out)
	for d.More() {
		if err := d.Decode(&s); err != nil {
			t.Fatal(err)
		}
	}
	if s.Reason != "quit command" || s.TargetMemoryBytes != 2<<20 || s.TargetCPUCores <= 0 {
		t.Errorf("commands were not applied: %+v", s)
	}
}

func TestCancellationStopsWorkers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c := newCPULoad(ctx, 2, 100*time.Millisecond)
	c.set(2)
	time.Sleep(20 * time.Millisecond)
	start := time.Now()
	cancel()
	c.wg.Wait()
	if time.Since(start) > time.Second {
		t.Error("CPU workers did not stop promptly")
	}
}
