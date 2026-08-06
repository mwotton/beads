package main

import (
	"math"
	"runtime/debug"
	"testing"
)

// restoreGC snapshots the process GC knobs and puts them back, so these tests
// don't leak a 400% GOGC into the rest of the package's tests.
func restoreGC(t *testing.T) {
	t.Helper()
	oldPercent := debug.SetGCPercent(100)
	oldLimit := debug.SetMemoryLimit(math.MaxInt64)
	t.Cleanup(func() {
		debug.SetGCPercent(oldPercent)
		debug.SetMemoryLimit(oldLimit)
	})
}

func TestTuneGCAppliesCLIDefaults(t *testing.T) {
	restoreGC(t)

	tuneGCWith("", "")

	// SetGCPercent returns the previous value, so calling it again reports
	// what tuneGCWith installed.
	if got := debug.SetGCPercent(gcPercent); got != gcPercent {
		t.Errorf("GOGC = %d, want %d", got, gcPercent)
	}
	if got := debug.SetMemoryLimit(gcSoftMemoryLimit); got != gcSoftMemoryLimit {
		t.Errorf("memory limit = %d, want %d", got, gcSoftMemoryLimit)
	}
}

// An operator who sets GOGC or GOMEMLIMIT keeps the last word: the Go runtime
// already applied their value at startup, and tuneGC must not stomp it.
func TestTuneGCDefersToEnvironment(t *testing.T) {
	restoreGC(t)

	const sentinelPercent = 37
	const sentinelLimit = 512 << 20
	debug.SetGCPercent(sentinelPercent)
	debug.SetMemoryLimit(sentinelLimit)

	tuneGCWith("37", "536870912")

	if got := debug.SetGCPercent(sentinelPercent); got != sentinelPercent {
		t.Errorf("GOGC = %d, want the operator's %d untouched", got, sentinelPercent)
	}
	if got := debug.SetMemoryLimit(sentinelLimit); got != sentinelLimit {
		t.Errorf("memory limit = %d, want the operator's %d untouched", got, sentinelLimit)
	}
}
