package main

import (
	"os"
	"runtime/debug"
)

// Default GC knobs for the bd CLI process.
//
// bd is a short-lived, one-shot process: it opens the store, answers one
// question, and exits. Go's default GOGC=100 is tuned for long-running
// servers, where keeping the heap small pays off over hours. Here it just
// buys collections nobody benefits from — the process exits before the freed
// memory is ever reused.
//
// Profiling a single `bd show --json` against a production-shaped embedded
// Dolt store measured ~19% of the command's CPU in gcBgMarkWorker/gcDrain.
// Opening the Dolt engine allocates hard (chunk journal index replay, table
// index parsing), so the mark workers run through most of the command.
// Raising GOGC to 400 removed roughly a quarter of total user CPU for about
// 110MB more peak RSS; going all the way to GOGC=off bought only another ~4%
// for a further 130MB, so 400 is the knee of the curve rather than the floor.
//
// gcSoftMemoryLimit is the safety net that makes the higher GOGC safe to
// ship. GOGC alone is a ratio with no ceiling: a pathological workload (say
// `bd list --limit 0` against a very large store) would let the heap grow
// unbounded between collections. The soft limit makes the collector ignore
// GOGC and start working as the heap approaches it, so the trade is "spend
// memory to skip GC up to a point", not "never GC".
const (
	gcPercent         = 400
	gcSoftMemoryLimit = 2 << 30 // 2 GiB
)

// tuneGC applies the CLI's GC defaults, honoring the standard environment
// variables so operators keep the last word. Go itself reads GOGC/GOMEMLIMIT
// at startup; we only override when the operator has not expressed a
// preference, so `GOGC=100 bd show` still measures stock behavior and
// GOMEMLIMIT stays usable for memory-constrained hosts.
func tuneGC() {
	tuneGCWith(os.Getenv("GOGC"), os.Getenv("GOMEMLIMIT"))
}

// tuneGCWith is tuneGC with the environment injected, so the
// operator-wins contract is testable without mutating process-wide GC state
// from a test.
func tuneGCWith(gogcEnv, memLimitEnv string) {
	if gogcEnv == "" {
		debug.SetGCPercent(gcPercent)
	}
	if memLimitEnv == "" {
		debug.SetMemoryLimit(gcSoftMemoryLimit)
	}
}
