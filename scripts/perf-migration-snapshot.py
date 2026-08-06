#!/usr/bin/env python3
"""Canonical integrity snapshot of a bd repo: ids, statuses, notes, deps, all fields.

Usage: bdmig-snapshot.py <bd-binary> <label>
Prints a stable, diffable dump to stdout. Run before and after a migration and
diff the two. Volatile-by-design fields are NOT stripped: a migration that is
truly non-destructive should preserve them too, and if it doesn't we want to see it.
"""
import json
import subprocess
import sys

bd, label = sys.argv[1], sys.argv[2]


def run(*args):
    p = subprocess.run([bd, *args], capture_output=True, text=True, timeout=600)
    if p.returncode != 0:
        return None, p.stderr.strip()
    return p.stdout, None


# --limit 0 returns non-closed only; --status all is needed for the full set.
out, err = run("--readonly", "list", "--limit", "0", "--status", "all", "--json")
if out is None:
    out, err = run("--readonly", "list", "--limit", "0", "--json")
if out is None:
    print(f"FATAL {label}: cannot list: {err}")
    sys.exit(1)

try:
    issues = json.loads(out)
except Exception as e:
    print(f"FATAL {label}: unparseable list output: {e}")
    sys.exit(1)

ids = sorted(i["id"] for i in issues)
print(f"# snapshot {label}")
print(f"count={len(ids)}")
print(f"ids={','.join(ids)}")

for iid in ids:
    out, err = run("--readonly", "show", iid, "--json")
    if out is None:
        print(f"{iid}: SHOW-FAILED {err}")
        continue
    d = json.loads(out)
    if isinstance(d, list):
        d = d[0] if d else {}
    # Dump every scalar/collection field, sorted, so any drift shows up.
    for k in sorted(d.keys()):
        v = d[k]
        if isinstance(v, (dict, list)):
            v = json.dumps(v, sort_keys=True, ensure_ascii=False)
        print(f"{iid}.{k}={v}")
