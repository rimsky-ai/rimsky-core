---
tension: state-count-drift
category: inconsistent
status: resolved
affects:
  - node-run
  - parked-state
  - node
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

- Reconcile every surface to a single count of five states with `parked` listed inline.
- Reconcile to "4 base states + 1 hold state (`parked`)" and explain the base-vs-hold distinction (see `concept:parked-state`).
- Establish the node-run concept's definition as the single authoritative state enumeration that all other prose references, so the count cannot drift across surfaces (see `concept:node-run`).

## Evidence

- The node-run state enum is now seven values (`pending`, `stale`, `running`, `held`, `parked`, `fresh`, `failed`), not five — the four-phase model plus a separate phase column was collapsed into this single seven-value column. `node-state` as a standalone concept is retired in favor of `concept:node-run`, which is the enum's one authoritative home.

## Resolution

The old node-state model (four or five operator-visible states, tracked partly on the node row) is superseded by the node-run seven-state machine (`concept:node-run`, 2026-06-20): `pending`, `stale`, `running`, `held`, `parked`, `fresh`, `failed`. Nodes themselves carry no runtime state at all — state belongs exclusively to node_run rows, and `concept:node-run` is established as the single authoritative state enumeration other prose references, closing the "which count is right" question this tension raised. The tension's own five-value premise is itself now stale; the accretion it worried about is resolved by a formal state-machine redesign, not a documentation reconciliation.

