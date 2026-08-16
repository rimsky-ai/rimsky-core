---
assumption: cli-time-window-flags-uniform
commit: d977250c
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# `--since`, `--until`, `--before`, and `--older-than` are accepted uniformly by every time-ordered read (`logs`, `instance events`, `messages tail`, `audit`), with the same timestamp grammar.

As operator narrowing a query, I would take it that `--since`, `--until`, `--before`, and `--older-than` are accepted uniformly by every time-ordered read (`logs`, `instance events`, `messages tail`, `audit`), with the same timestamp grammar.

## Source

sibling-symmetry — four time-window flags in one flag set over several chronological read verbs

## What a run would observe

apply `--since`/`--until` to each chronological read verb and record which reject the flag.

## Measured

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
