---
tension: state-count-drift
category: inconsistent
status: open
affects:
  - node-state
  - parked-state
---

# Node-state vocabulary count drifts across prose

## What is muddy

The node-state enum is currently five values: `fresh | stale | running | failed | parked`. But the count cited across documents varies:

- CLAUDE.md "What this repo is": "5 node states (`fresh`, `stale`, `running`, `failed`, `parked`)" — correct.
- `docs/concepts/node-state.md` describes "the four operator-visible states (`fresh`, `stale`, `running`, `failed`)" and treats `parked` as added separately (`_discover/2026-05-10-state-machine-no-self-loop.md` Observations).
- Older sketches and pre-platform-extensions prose still cite four.

The discrepancy isn't an error in any one place; it's a vocabulary that accreted `parked` later and hasn't been globally reconciled.

## Why it matters

A reader counting from one surface gets a stale picture: they may build mental models around four states and miss the parked path's hold-state semantics. A code review touching `NextState` benefits from a single authoritative count.

## Resolution candidates (do NOT pick)

- Reconcile every prose to "5 states" with `parked` listed inline.
- Reconcile to "4 base + 1 hold state (`parked`)" and explain the distinction.
- Add a single authoritative table in `docs/concepts/node-state.md` that prose references.

## Evidence

- `_discover/2026-05-10-state-machine-no-self-loop.md` Observations.
- `_discover/2026-05-10-parked-state-and-resume.md` Description.
- `foundation/cascade/state.go:110-117` — the five legal states inline.

