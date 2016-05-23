package main

import (
	"os"
	"time"

	"github.com/peterbourgon/g2s"
)

func setupStatsd() (g2s.Statter, error) {
	if config.Statsd.Addr == "" {
		return g2s.Noop(), nil
	}

	if config.Statsd.Namespace == "" {
		hostname, _ := os.Hostname()
		config.Statsd.Namespace = "nixy." + hostname
	}

	if config.Statsd.SampleRate < 1 || config.Statsd.SampleRate > 100 {
		config.Statsd.SampleRate = 100
	}

	return g2s.Dial("udp", config.Statsd.Addr)
}

func statsCount(metric string, n int) {
	ns := config.Statsd.Namespace
	statsd.Counter(1.0, ns+"."+metric, n)
}

func statsTiming(metric string, elapsed time.Duration) {
	ns := config.Statsd.Namespace
	statsd.Timing(1.0, ns+"."+metric, elapsed)
}
