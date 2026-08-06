# MAYOR TASK 2 — embedded → server migration path (now the higher-value question)

This SUPERSEDES the hot-path profiling as your top priority. Finish or park whatever you
have, write down what you found, then work this. The profiling still matters, but it
addresses the *smaller* half of the problem (see the numbers below); this question
unblocks the structural fix.

## What we established while you were working

`bd` supports four server modes (`internal/doltserver/servermode.go`): `embedded` (called
the legacy path in your own source), `owned` (bd's DEFAULT when nothing says otherwise),
`external`, and `shared-server`. **Every dolt-backed repo on this host is explicitly
pinned to `embedded`** — not by choice, it turns out, but because `dolt` was not installed
on this machine at all until an hour ago.

Measured on rustfetch, embedded, quiet host, unchanged binary:

- 1-way `bd show`: real ~1.2s, user ~1.3s
- 5-way concurrent: finishes at 1.15 / 2.45 / 3.97 / 5.59 / 8.32s — a **perfect ~1.45s
  staircase**, user CPU FLAT at ~1.3s throughout.

That is exact serialization: one call proceeds at a time. Extrapolated to ~14 concurrent
agent callers it predicts ~20s, and the original pathological sample was 19.845s. The
mechanism is fully accounted for. **The O(N) serialization is the structural problem; the
~1.3s per-call constant is the smaller half.**

## What blocked the experiment

`dolt` v2.2.0 is now installed at `~/.local/bin/dolt`, deliberately matched to bd's pinned
`github.com/dolthub/dolt/go v0.40.5-0.20260715172757-a6690826d767` (v2.2.0 was published
14 minutes after that commit timestamp). So the dependency is settled.

Flipping rustfetch's `.beads/metadata.json` to `dolt_mode: server` then failed:

```
initializing dolt database: creating dolt directory:
mkdir /home/mark/lambdalabs/rustfetch/.beads/dolt: not a directory
```

Because the on-disk layout differs:

- `.beads/embeddeddolt` — **156M, the LIVE issue data** used by embedded mode
- `.beads/dolt` — a **2.0M stale SQLite file** from the pre-Dolt era (dated Jul 13),
  squatting on the exact path server mode wants as a *directory*
- `.beads/backup` (39M, `.darc` archives), `.beads/.br_history` (38M), `.beads/beads.db`
  (2M) — further archaeology from the br/SQLite era

I reverted to `embedded` immediately and verified integrity (54 open issues, matching the
pre-test baseline exactly). **No repo is currently in a broken state.**

## Your questions, in priority order

1. **Is there a supported embedded → server migration in bd?** A command, a documented
   procedure, or a code path. If it does not exist, say so plainly — that is a complete
   answer and it changes the decision.
2. **Does server mode require `.beads/dolt/`, or can its data directory be pointed at the
   existing `.beads/embeddeddolt`?** Is the on-disk format identical between the two modes,
   or does embedded use a different physical layout? `internal/doltserver/physical_root.go`
   looked relevant.
3. **What does the stale `.beads/dolt` SQLite file do to any of this** — is it load-bearing
   for anything current, or pure leftover that can be moved aside?
4. **What happens to an EXISTING populated repo under `bd init --shared-server`** or
   `bd config set dolt.shared-server true`? Does it migrate, ignore, or start empty?
5. **What is the rollback** if a migration half-completes? Assume the worst case and say
   what would be unrecoverable.

## HARD CONSTRAINTS — these are about not destroying the fleet's issue tracker

- **DO NOT migrate, modify, or even write to ANY real repo's `.beads` directory.** Not
  rustfetch's, not any other repo's on this host. Every repo's live issue state is in
  there and there is no second copy of some of it.
- **PROVE THE PROCEDURE ON A THROWAWAY.** Create a scratch repo somewhere under `/tmp`,
  `bd init` it in embedded mode, populate it with enough synthetic issues to be meaningful,
  and attempt the migration THERE. A procedure that has not been executed end to end on a
  scratch DB is a guess, and I will treat it as one.
- Your integrity check must be real: compare issue COUNT **and** content (ids, statuses,
  notes, dependencies) before and after, not just that the command exited 0. Include a
  case with notes and dependencies, since those are what our engagement records live in.
- Still: **never overwrite `/home/mark/.local/bin/bd`**, and **never push to `origin`**
  (public upstream `gastownhall/beads`) — the `mwotton` fork only.

## Deliverable

Append to `PERF-REPORT.md` (or a new `MIGRATION-REPORT.md`) covering: whether a supported
path exists, the exact procedure if one does, the evidence from your scratch-repo run,
the rollback story, and what you could NOT determine. A clear "there is no supported
migration and here is what would have to be built" is a valuable result — do not
manufacture a procedure to have something to report.
