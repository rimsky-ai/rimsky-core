---
trap: cli-time-window-flags-uniform
release: d977250c
---
# Evidence set — `--since`, `--until`, `--before`, and `--older-than` are accepted uniformly by every time-ordered read (`logs`, `instance events`, `messages tail`, `audit`), with the same timestamp grammar.

Source of the prior: sibling-symmetry — four time-window flags in one flag set over several chronological read verbs

## What the audit ran and observed (assumption record)

`.ok-planner/experiments/assumption-cli-time-window-flags-uniform` — built for
this run — built the acceptance matrix of the four flags across eight
time-ordered reads at the parser, then drove the accepted ones against a live
`rimsky-all-in-one` from this tree's image set to pin the timestamp grammar.

Not one of the four is accepted uniformly. `--since` reaches 3 of the 8 reads,
`--until` 3, `--older-than` 2, `--before` 1. `messages tail`, which the prior
names, defines neither `--since` nor `--until`: a chronological tail with no
window on it. `asset lineage` and `instance nodes` define none of the four.
`--before` exists only on `lineage prune`, which is a write, not a read.

`watch --until` is the sharp edge, because it is accepted and means something
else entirely. `rimsky watch <id> --until 2026-01-01T00:00:00Z` fails with
`--until must be idle or terminated` — on `watch` the flag names an exit
condition, while on `instance events`, pointed at the same instance, it names
an upper time bound. The operator who moves a window from one verb to its
neighbour gets an error that does not mention time.

The fourth read the prior names has no verb at all: `GET /v1/audit` is
reachable over HTTP and `rimsky audit` is not a command, so there is nothing
to put a window on.

Where the flags are accepted the grammar itself is consistent and strict:
`instance events --since` and `lineage prune --before` take RFC3339 and reject
a bare date, a relative duration, and an epoch integer alike. 3 checks, 0
pass, 3 fail.

## Experiment record (experiment:assumption-cli-time-window-flags-uniform)

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

Runnables: `src:.ok-planner/experiments/assumption-cli-time-window-flags-uniform/` at the stamped commit.
