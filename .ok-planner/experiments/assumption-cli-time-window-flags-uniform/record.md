---
experiment: assumption-cli-time-window-flags-uniform
commit: d977250c
---

# The four time-window flags across the time-ordered reads

## What it ran against

The acceptance matrix of `--since`, `--until`, `--before`, `--older-than`
against eight time-ordered reads — `logs`, `instance events`, `messages tail`,
`watch`, `parked list`, `lineage prune`, `asset lineage`, `instance nodes` —
settled at the parser with the endpoint pointed at a closed port, so
"connection refused" means accepted and "flag provided but not defined" means
not. The timestamp grammar of the accepted ones is then driven against a live
`rimsky-all-in-one` from this tree's image set.

## What was observed

No flag is accepted uniformly. `--since` reaches 3 of the 8 reads (`logs`,
`instance events`, `watch`), `--until` 3, `--before` 1 (`lineage prune`, a
write), `--older-than` 2 (`parked list`, `lineage prune`). `messages tail`,
which the assumption names, defines neither `--since` nor `--until` — a
chronological tail with no window at all. `asset lineage` and `instance nodes`
define none of the four.

`watch --until` is the trap inside the trap: it is accepted, and it means
something else. `rimsky watch <id> --until 2026-01-01T00:00:00Z` fails with
`--until must be idle or terminated`, because on `watch` the flag names an
exit condition while on `instance events` — reachable from the same instance —
it names an upper time bound.

`GET /v1/audit`, the platform's own chronological read and the fourth verb the
assumption names, has no CLI verb at all, so there is nothing to put a window
on.

Where the flags are accepted the grammar is consistent and strict: `instance
events --since` and `lineage prune --before` take RFC3339 and reject a bare
date, a relative duration, and an epoch integer. 3 checks, 0 pass, 3 fail.
