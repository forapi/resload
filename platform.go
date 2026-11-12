package main

import "time"

type resources struct {
	cores  float64
	memory uint64
	source string
}

type processSample struct {
	cpu       time.Duration
	rss       uint64
	available uint64
}
