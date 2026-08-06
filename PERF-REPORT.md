# bd in-process hot path: profile and fixes

Branch: `bd-hotpath`. Tracking bead `rf-s3d0` (lives in another repo; `bd` does
not resolve here).

Scope: the **user CPU** of a single `bd` read, not the wall-vs-user gap. The
lock contention between concurrent `bd` processes is being characterised
separately. Where contention and CPU touch each other, this report says so and
stops there rather than changing locking behaviour.

## TL;DR

Two code changes cut **29–40% of user CPU** off every embedded-Dolt read, with
byte-identical output. A third finding is operational, not code, and is much
larger: the measured database had a **78 MB un-garbage-collected chunk
journal** whose index is replayed **nine times per command**. Running `bd gc`
on it took `bd show` from 1.33s to 0.24s of user CPU.

| | `bd show` user CPU | vs baseline |
|---|---|---|
| baseline (HEAD `67f812a23`) | 1.328s | — |
| + this branch's two fixes | 0.804s | **−39.5%** |
| + `bd gc` on the store | 0.236s | **−82.3%** |

The remaining ceiling is architectural and is stated at the bottom: **bd opens
and destroys the entire Dolt SQL engine nine times to answer one `bd show`.**
That is the number that should drive the dolt-server decision.

## Method

**Fixture.** The reported 19.8s/2.3s-user `bd show` came from a live,
contended workspace. To measure user CPU without contention I copied a real
production-shaped workspace — the `mayor` repo's `.beads` (162 MB, 1567 Dolt
commits, ~1900 issues) — into an isolated git repo off the live tree. Nothing
in this report was measured against, or written to, a live workspace.

**Binary.** Built to scratch paths only (`~/.cache/bd-hotpath-perf/bd-{before,after}`).
`/home/mark/.local/bin/bd` was never touched.

```
go build -tags gms_pure_go -o <scratch> ./cmd/bd    # gms_pure_go per .buildflags
```

**Profiling.** bd already has the hook: `bd --cpu-profile <cmd>` writes
`bd-profile-<cmd>-<ts>.prof`. It covers `PersistentPreRun` → `PersistentPostRun`,
which is ~92% of the process's user CPU (`bd version`, the store-less floor,
is 0.08s). Read with `go tool pprof -top -cum` / `-peek`, never the http UI.

**Timing.** `matrix.sh` alternates before/after/gc arms *within* each
iteration, n=15 per cell, so a load spike hits all three arms rather than
biasing one. This host is shared and busy: **load average ranged 11–22 during
the final matrix run** (it had been ~376 earlier in the session). Numbers are
reproducible in ranking and rough magnitude, not to the millisecond.

## Where the 2.3 seconds goes

`bd --cpu-profile --readonly show myr-box --json`, baseline binary. Total
samples 1.29s over a 783ms wall window.

| Cost | cum | What it is |
|---|---|---|
| `embeddeddolt.OpenSQL` | 20.2% | opening the Dolt SQL engine — **9× per command** |
| `engine.NewSqlEngine` | 18.6% | (inside the above) `CollectDBs` → `LoadDoltDB` → `ResolveWorkingSet` |
| `runtime.gcBgMarkWorker` | 17.8% | Go GC |
| `nbs.processIndexRecords` | 17.1% | chunk journal index replay, once per engine open |
| `nbs.newOnHeapTableIndex` | 14.7% | table index parse — of which… |
| `runtime/debug.Stack` | 14.7% | …**96.3%** is a debug stack capture |

Note what is *not* here: the actual SQL. Parsing, planning and executing the
queries that answer the question is a minority of the work. Essentially all of
it is store-open overhead, paid nine times.

### Finding 1 — a Dolt debug assertion left on (fixed)

`nbs.newOnHeapTableIndex` was spending 96.3% of its time in `runtime/debug.Stack`:

```
                                             0.26s   100% |   nbs.newOnHeapTableIndex
         0     0%     0%      0.26s 20.47%                | runtime/debug.Stack
                                             0.24s 92.31% |   runtime.Stack
```

`nbs.TableIndexGCFinalizerWithStackTrace` defaults to **`true`** in the Dolt
library (`store/nbs/table_index.go:40`). When set, every table index open
captures a full runtime stack trace so that a leaked index's finalizer can name
its allocation site. The `dolt` CLI turns it off in its own `main`
(`cmd/dolt/dolt.go:239`, gated on `DOLT_VERBOSE_ASSERT_TABLE_FILES_CLOSED`).
beads embeds the library and never did, so bd inherited the debug default —
and bd's deep call stacks plus one index per table file make each capture
expensive.

**Fix** (`internal/storage/embeddeddolt/open.go`): mirror the dolt CLI exactly,
including honouring the same env var so the assertion stays debuggable.

### Finding 2 — server GC defaults in a one-shot CLI (fixed)

17.8% of the command was `gcBgMarkWorker`/`gcDrain`. bd is a one-shot process:
it opens the store, answers one question, exits. Go's default `GOGC=100` is
tuned for long-running servers, where a small heap pays off over hours. Here
the process exits before any freed memory is reused, so most of that mark work
buys nothing. Engine open allocates hard, so the mark workers run through most
of the command.

Measured (`bd show`, n=5 each):

| GOGC | user CPU | peak RSS |
|---|---|---|
| 100 (default) | 0.836s | 181 MB |
| **400** | **0.595s** | **290 MB** |
| 800 | 0.603s | 307 MB |
| off | 0.570s | 419 MB |

400 is the knee: it captures nearly all the win, and going to `off` buys a
further ~4% for another 130 MB. **Fix** (`cmd/bd/gctune.go`): `GOGC=400` plus a
2 GiB soft memory limit, applied at the top of `main()`.

The memory limit is what makes the higher GOGC safe to ship — GOGC alone is a
ratio with no ceiling, so a pathological workload could grow the heap
unboundedly between collections. `GOGC` and `GOMEMLIMIT` from the environment
both still win, so operators on memory-constrained hosts keep the last word,
and `GOGC=100 bd show` still measures stock behaviour.

This is a `cmd/bd` change only — it is not imposed on beads-as-a-library.

### Finding 3 — the store's chunk journal was never GC'd (operational)

`nbs.processIndexRecords` (17.1%) is Dolt replaying the chunk journal index at
every engine open. Its cost scales with journal size, and it is paid **nine
times per command**. The fixture's store:

```
78 MB   noms/vvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvv   (chunk journal)
1.6 MB  noms/journal.idx
21 MB   + ~40 smaller .darc archive files
```

`bd gc --skip-decay --force` took 1.4s and collapsed all of that into a single
2.1 MB table file (117.8 MB → 58.5 MB on disk; journal eliminated entirely).
`processIndexRecords` then disappears from the profile — total samples for the
same `bd show` fall from 930ms to **170ms**.

This is not a code defect and I have **not** run it against any live workspace
— `bd gc` rewrites stored history, which is outside this task's remit. It is
offered as an operator action, with a caveat from its own output: remote-tracking
refs and tags anchor history, so a history squash wants `bd flatten`/`bd compact`
first. Verified data-preserving on the copy: `bd list --limit 0 --json` is
byte-identical before and after.

The systems point is that **journal size is amplified 9× by finding 4**. A
store that has not been GC'd recently makes every single read progressively
worse, which fits the operator's report of `bd` degrading over time.

## Results

n=15 per cell, arms interleaved, load average 11–22 on a 56-core host.
Concurrent load: this is a shared box running other agents; no `bd` process was
contending for *this* fixture's lock, so these isolate user CPU as intended.

```
bd show myr-box            real        user       vs before
  before                   1.304s      1.328s     —
  after (code)             1.131s      0.804s     −39.5%
  after + bd gc            0.227s      0.236s     −82.3%

bd list --limit 3
  before                   0.754s      0.993s     —
  after (code)             0.818s      0.628s     −36.8%
  after + bd gc            0.226s      0.236s     −76.3%

bd list --limit 0
  before                   0.721s      0.937s     —
  after (code)             0.770s      0.628s     −33.0%
  after + bd gc            0.238s      0.250s     −73.3%

bd ready
  before                   0.546s      0.607s     —
  after (code)             0.527s      0.428s     −29.5%
  after + bd gc            0.246s      0.229s     −62.4%
```

Profile totals for the same `bd show`: **1.29s → 930ms → 170ms** of samples.

Correctness: `bd show`, `bd list --limit 0` and `bd ready` produce
**byte-identical JSON** before and after (76 KB / 500 KB / 315 KB
respectively).

Note `real` barely moves for the code-only arm while `user` drops sharply. That
is expected and is the point: the removed work was parallel GC and stack-walking
across multiple cores. On an idle box it shows up as less CPU burned; on the
**contended** box the operator measured, freeing ~0.5 CPU-seconds per `bd`
invocation across six workers is the part that matters.

## What I did not change, and why

**bd opens the entire Dolt SQL engine 9 times to answer one `bd show`.** This
is the single largest remaining cost (`OpenSQL` is 33% of the post-fix profile)
and I deliberately left it alone. The evidence, from instrumented caller stacks:

| # | Caller | Purpose |
|---|---|---|
| 1 | `openReadOnly` | schema drift check |
| 2 | `SearchIssues` ← `utils.ResolvePartialID` | resolve `myr-box` → full ID |
| 3 | `GetIssue` ← `resolveAndGetIssueWithRouting` | fetch the issue |
| 4 | `GetIssue` ← `workapi.GetIssueOrWisp` ← `storeReader.Get` | **fetch the same issue again** |
| 5–9 | `GetLabels`, `GetDependenciesWithMetadata`, `CountDependents`, `CountDependencies`, `CountIssueComments`, all ← `workapi.BuildIssueDetails` | detail panel |

Counts for other verbs: `bd list` 6 opens, `bd ready` 3. Identical with and
without `--readonly`.

Each open is a full `NewSqlEngine` → `CollectDBs` → `LoadDoltDB` → journal
replay → `ResolveWorkingSet`, then a complete teardown. The engine is cached
**per-Connector** (`driver/v2.Connector.se`), and `EmbeddedDoltStore.withConn`
creates and closes a Connector every call — so the cache never survives a
single query.

This is deliberate. `store.go:36` states the rationale:

> Each method call opens a short-lived connection […] This minimizes the time
> the embedded engine's write lock is held, reducing contention when multiple
> processes access the same database concurrently.

That rationale is real: opening the engine acquires an **exclusive database
lock** (`driver/v2/retryable_open_err.go` retries on `nbs.ErrDatabaseLocked`)
held for the engine's lifetime. Caching it per-process would hold that lock for
the whole command instead of nine short windows.

So this is exactly the boundary the task drew: reducing it is a change to
concurrency behaviour, not a performance-only change, and it interacts directly
with the contention investigation being run separately. I am not making that
call unilaterally. But it is the finding that should inform it, in two ways:

1. **It is the CPU ceiling.** With the journal GC'd, `bd show` is 0.24s of user
   CPU and `OpenSQL` is still 29% of it. Everything below that is engine setup,
   not query work.
2. **It plausibly amplifies the contention, rather than limiting it.** Nine
   acquire/release cycles per read means nine chances to collide and nine
   exponential-backoff retries under load, versus one longer hold. The
   short-lived model minimises *hold duration* at the cost of maximising
   *acquisition count* — and the symptom being reported (single reads stalling
   for 80s while a daemon and TUI poll) is an acquisition-storm shape. Worth
   testing directly before the dolt-server decision is made, because if it
   holds, a single-engine-per-process change would help both axes.

There is also an unambiguous redundancy independent of all this: **`GetIssue`
is called twice for the same ID** (opens 3 and 4). I left it because the fix
belongs in the `workapi`/`storereader` read path rather than in storage, and it
is worth ~1/9 of the open cost — small next to the item above, and better done
as its own change.

## Reproducing

```bash
go build -tags gms_pure_go -o /tmp/bd-perf ./cmd/bd
cd <a copy of a dolt-backed workspace>
/tmp/bd-perf --cpu-profile --readonly show <id> --json
go tool pprof -top -cum /tmp/bd-perf bd-profile-show-*.prof
```

Artifacts kept at `~/.cache/bd-hotpath-perf/`: `bd-before`, `bd-after`,
`cpu-{before,after,after-gc}.prof`, `matrix.sh`, and both fixtures.

## Tests

- `./scripts/test.sh ./internal/storage/embeddeddolt/...` — pass (34.9s)
- `./scripts/test.sh ./cmd/bd/...` — pass except `TestBootstrapNoWorkspace` and
  `TestWhereNoWorkspace`, which **fail identically on clean HEAD** in this
  environment (they assert "no workspace found", and the runner's `TMPDIR` here
  sits under `/home/mark`, which has a `~/.beads`). Not a regression.
- `./scripts/test.sh ./internal/...` — 75 packages pass. Four packages fail
  (`internal/beads`, `internal/config`, `internal/formula`,
  `internal/storage/dbproxy/proxy`) with an **identical failure set on clean
  HEAD** — same environmental cause (workspace/config discovery walking up into
  `/home/mark/.beads`, plus process-lock tests). None of these packages is
  touched by this branch. Baseline log: `~/.cache/bd-hotpath-perf/baseline-fails.log`.
- New: `TestTableIndexStackTracesDisabledByDefault` /
  `TestTableIndexStackTracesFollowVerboseAssertEnv`,
  `TestTuneGCAppliesCLIDefaults` / `TestTuneGCDefersToEnvironment`.

The two new assertion tests are regression guards, not micro-benchmarks: they
fail if a future dependency bump or refactor silently re-enables the debug
stack capture or drops the GC tuning.
