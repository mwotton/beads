# embedded → server migration: is there a supported path?

Answers MAYOR TASK 2. Every procedure below was executed end-to-end on a
throwaway `bd init` repo under `/tmp`, never on a real workspace.

## Answers, short form

| # | Question | Answer |
|---|---|---|
| 1 | Is there a supported embedded → server migration? | **No.** `bd migrate` has four mode transitions; none has an `embedded` edge. |
| 2 | Can server mode point at the existing `embeddeddolt`? | **Yes** — `dolt_data_dir` (relative). On-disk format is identical. Proven, zero-copy, reversible. |
| 3 | What does the stale `.beads/dolt` SQLite file do? | Pure leftover. Breaks only the *naive* flip; the working procedure never touches it. |
| 4 | `bd init --shared-server` on a populated repo? | **Refuses and aborts.** No migration, no data loss, metadata unchanged. |
| 5 | Rollback if it half-completes? | Config-only, so there is no half-state for data. One genuinely unrecoverable scenario exists — see below. |

**There is no supported migration command, but there is a supported
*configuration* that achieves the same thing without moving a byte.** That
distinction matters: it is not a manufactured procedure, it uses a documented
config key (`dolt_data_dir`, "Custom dolt data directory") for a purpose it was
plainly built for, and I ran it end to end.

## 1. No supported embedded → server migration exists

`bd migrate` exposes four mode transitions, all marked EXPERIMENTAL:

```
from-server-to-proxied-server
from-proxied-server-to-server
from-shared-server-to-proxied-server
from-proxied-server-to-shared-server
```

The matrix is **server ↔ proxied-server ↔ shared-server**. There is no
`embedded` vertex. Each command also rejects a non-matching source mode
outright (`cmd/bd/migrate_dolt_mode.go:267`):

```go
return HandleError("repo is not in server mode (dolt_mode=%q); this command only migrates server-mode repos", ...)
```

`grep -i embedded cmd/bd/migrate_dolt_mode.go` returns exactly one hit, an
unrelated comment about lock ordering. So: no command, and no documented
procedure. Confirmed by code, not inference.

## 2. Server mode CAN be pointed at the existing `embeddeddolt` — proven

### Why it works

The two modes resolve their data directory differently:

* **Embedded is hardcoded.** `internal/storage/embeddeddolt/store.go:115,176`
  do `filepath.Join(absBeadsDir, "embeddeddolt")` with no override consulted.
  `internal/doltserver/physical_root.go:319` says so in as many words: *"the
  embedded engine, whose data dir is hardcoded to .beads/embeddeddolt"*.
* **Server mode is configurable.** `Config.DatabasePath()`
  (`internal/configfile/configfile.go:172`) checks `BEADS_DOLT_DATA_DIR`, then
  `dolt_data_dir` from metadata.json, and only then defaults to
  `.beads/dolt`.

So embedded cannot be moved, but **server can be pointed at where embedded
already is**. And the physical format is identical — the same directory serves
both engines unmodified.

### One trap worth knowing

`Config.Save()` (`configfile.go:129`) silently **strips absolute**
`dolt_data_dir` values:

```go
saved := *c
if filepath.IsAbs(saved.DoltDataDir) {
    saved.DoltDataDir = ""
}
```

An absolute path works until the next config write, then vanishes and the repo
silently falls back to `.beads/dolt`. **Use the relative form.** I verified a
relative `"embeddeddolt"` survives repeated writes (4 creates + an
`--append-notes`) with the value intact.

### The procedure

```jsonc
// .beads/metadata.json
{
  "dolt_mode": "server",
  "dolt_data_dir": "embeddeddolt"   // RELATIVE — absolute is stripped on save
}
```

Then `bd dolt start`. No data is copied, no `.beads/dolt` is created.

```
Dolt server started (PID 1004685, port 34203)
  Data: /tmp/bdmig-989789/.beads/embeddeddolt
```

### Evidence

Throwaway repo: `bd init --prefix mig`, embedded, populated with 4 issues
covering the content that matters — a 145-byte multi-line note containing
backticks, double quotes and non-ASCII (`→ ✓`) in the shape of an ENGAGEMENT
record; a `parent-child` dependency; a `blocks` dependency; and the four
statuses open / in_progress / closed.

Integrity check (`/tmp/bdmig-snapshot.py`) dumps **every field** of
`bd show --json` for every issue — id, status, priority, notes, dependencies
(full objects with `dependency_type`), parent, owner, timestamps, counts,
design, acceptance_criteria — sorted into a diffable form. Nothing is stripped:
a truly non-destructive migration should preserve timestamps too.

```
diff BEFORE AFTER  →  *** IDENTICAL: count, ids, statuses, notes, dependencies, all fields ***
```

Then, still in server mode: created an issue, appended a note, added a
dependency — all succeeded. Stopped the server, reverted metadata.json to
`embedded`, and re-snapshotted:

```
*** ROLLBACK CLEAN: server-mode writes visible in embedded mode ***
```

So the round trip is **embedded → server → write → embedded**, with every
server-mode write readable afterwards. Both engines are reading and writing the
same physical store.

## 3. The stale `.beads/dolt` SQLite file is pure leftover

Reproduced the rustfetch condition exactly by planting a 2 MB file at
`.beads/dolt` in the throwaway.

* **Naive flip** (`dolt_mode: server`, no `dolt_data_dir`) reproduces the
  mayor's error verbatim:
  ```
  Error: initializing dolt database: creating dolt directory:
  mkdir /tmp/bdmig-989789/.beads/dolt: not a directory
  ```
* **Procedure B with the file still in place**: server starts normally against
  `embeddeddolt`, and the integrity snapshot is identical. The squatter is
  never opened, statted, or written.

It is not load-bearing for embedded mode either — embedded hardcodes
`embeddeddolt` and never looks at `.beads/dolt`. It can be moved aside, but
procedure B does not require it. **I did not move rustfetch's copy**; it is
still there, untouched, 2076672 bytes dated Jul 13.

One caveat I could not fully rule out: `physical_root.go`'s *default* branch
(no metadata.json at all) probes `.beads/dolt` and treats it as server layout
**if it is a directory**. A file fails that `IsDir()` check, so it is inert on
that path too. But a workspace with no metadata.json is a different scenario
than any tested here.

## 4. `bd init --shared-server` on a populated repo refuses

Run against a copy of the populated throwaway:

```
⚠ Found existing Dolt database: dolt server at 127.0.0.1:3308
This workspace is already initialized.
...
Aborting.
```

metadata.json unchanged, all 5 issues intact. It does not migrate, does not
ignore, does not start empty — it detects and aborts. **`bd init` is not a
migration path**, and helpfully cannot be mistaken for one.

## 5. Rollback, and the one unrecoverable case

Procedure B moves no data, so **there is no half-migrated data state**. The
failure modes are:

| Failure | Result | Recovery |
|---|---|---|
| metadata edited, server fails to start | Reads fail; data untouched | Revert metadata.json |
| Server crashes mid-write | Ordinary Dolt transaction semantics; same as embedded | None needed |
| Want to go back | Revert metadata.json, `bd dolt stop` | Proven above |

**The unrecoverable scenario is a divergent fork, and procedure B avoids it by
construction.** If instead you *copy* `embeddeddolt` → `dolt` (the obvious
migration) and any process keeps writing to the old copy while others write the
new one, you get two Dolt databases with a shared ancestor and independent
commits. There is no merge path back, and picking one silently discards the
other's issues. Procedure B has a single physical store, so this cannot arise.

### The real operational hazard: propagation, not corruption

`.beads/metadata.json` is **committed to git** (`.beads/.gitignore` ignores
`dolt/` and `embeddeddolt/` but not `metadata.json`). So flipping `dolt_mode`
propagates to every clone on the next pull — and until a clone pulls, it stays
on `embedded` and will open the same physical directory directly.

I tested that stale-clone case: with the server holding the store, an
embedded-mode open **retries the lock indefinitely**:

```
level=warning msg="failed to load database with error: the database is locked by another dolt process"
(repeats; killed at 180s — bd's backoff sets MaxElapsedTime=0, i.e. wait forever)
```

Data was intact afterwards (8 issues, all fields). So it is **safe but hangs** —
and hanging is precisely the pathology the fleet is already suffering from, so
it would be easy to misdiagnose. A rollout must land the metadata change in
every clone and worktree of a repo at once, and no stale `bd` process may still
be running against it.

## What I could not determine

* **Whether `bd dolt push`/pull, federation, and sync behave identically under
  server mode.** Not tested — the throwaway had no remote. This is the same gap
  flagged in `PERF-REPORT.md` and it is the one that most needs closing before
  any rollout.
* **Behaviour with multiple databases under one `.beads`** (reeve and
  hs-crawl-processor both have a second store). `dolt_data_dir` points at a
  directory containing per-database subdirectories, so a server should see all
  of them, but I did not test the multi-DB case.
* **Whether `bd doctor` understands this configuration.** Several doctor checks
  are gated on embedded vs server mode (`cmd/bd/doctor.go:273`); a
  server-mode-pointed-at-embeddeddolt workspace is a combination it may not
  anticipate.
* Long-running stability, auto-GC behaviour, and what happens on host reboot
  with an auto-started server.

## Reproducing

```bash
S=/tmp/bdmig-$$; mkdir -p $S && cd $S && git init -q .
bd init --prefix mig                       # embedded
# ... create issues with notes and dependencies ...
python3 bdmig-snapshot.py "$(command -v bd)" BEFORE > /tmp/snap.before
# edit .beads/metadata.json: dolt_mode=server, dolt_data_dir=embeddeddolt
bd dolt start
python3 bdmig-snapshot.py "$(command -v bd)" AFTER  > /tmp/snap.after
diff <(tail -n +2 /tmp/snap.before) <(tail -n +2 /tmp/snap.after)
```

Snapshot tool: `scripts/perf-migration-snapshot.py` in this branch.

## Constraint compliance

* No real repo's `.beads` was migrated, modified, or written to. Verified after
  the run: rustfetch is still `dolt_mode: embedded`, its `.beads/dolt` leftover
  is byte-for-byte as found (2076672 bytes, Jul 13), `embeddeddolt` untouched.
* All work done in `/tmp/bdmig-*` throwaways, created by `bd init`.
* Scratch dolt servers stopped; zero `dolt` processes remain.
* `/home/mark/.local/bin/bd` executed only, never written.
* Branch pushed to `mwotton` only.
