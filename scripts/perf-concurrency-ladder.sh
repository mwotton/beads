#!/usr/bin/env bash
# Concurrency ladder: does contention inflate USER CPU, or only serialize wall?
# Runs N concurrent `bd show` against one store, recording each process's own
# real/user/sys, plus the wall time of the whole batch.
#
# usage: ladder.sh <fixture-dir> <label> <id>
set -u
BD=/home/mark/.cache/bd-hotpath-perf/bd-after
FIX="$1"; LABEL="$2"; ID="$3"
OUT=$(mktemp -d)
cd "$FIX" || exit 1

for N in 1 2 3 5; do
  rm -f "$OUT"/*.t
  batch_start=$(date +%s.%N)
  for i in $(seq 1 "$N"); do
    /usr/bin/time -f "%e %U %S" -o "$OUT/$i.t" \
      "$BD" --readonly show "$ID" --json >/dev/null 2>/dev/null &
  done
  wait
  batch_end=$(date +%s.%N)

  # per-process figures
  reals=$(awk '{printf "%.2f ", $1}' "$OUT"/*.t)
  awk -v n="$N" -v lbl="$LABEL" -v bw="$(awk -v a="$batch_start" -v b="$batch_end" 'BEGIN{print b-a}')" -v rl="$reals" '
    {r+=$1; u+=$2; s+=$3; c++}
    END{printf "  %-10s N=%d  batch_wall=%5.2fs   mean/proc: real=%5.2fs user=%5.2fs sys=%4.2fs   totalUserCPU=%5.2fs\n             finishes: %s\n",
        lbl, n, bw, r/c, u/c, s/c, u, rl}' "$OUT"/*.t
done
rm -rf "$OUT"
