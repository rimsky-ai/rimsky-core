---
concept: parked-state
status: as-is
aliases:
  - park
  - parked node
references:
  - _discover/2026-05-10-parked-state-and-resume.md
  - _discover/2026-05-10-state-machine-no-self-loop.md
  - _discover/2026-05-10-orphan-reaper-no-producer-abandon.md
---

# Parked state

## What it is

`parked` is the fifth legal `node-state` value, entered from `running` when the executor emits `ParkRequested`. While parked, the node is not running and not failed; it carries a `parked_payload`, optional `session_token`, optional `resume_at`, and `parked_reason`. The corresponding `rimsky_node_runs.phase` is `'parked'`.

## Purpose

Some workloads (human review, scheduled wake, external event wait) cannot finish in a bounded window. `parked` gives them a first-class hold state with explicit resume semantics, instead of forcing them through `failed`+retry (which loses session context) or keeping a gRPC stream open indefinitely.

## Boundaries

Owns: the hold-state schema (park columns on `table:rimsky_node_runs`), the three exit paths (time-wake, external invalidate, watchdog timeout), the `ResumeContext` passed back on re-dispatch. Does NOT own: held-claim resolution (that's `auto-terminal`); orphan reaping (parked rows are explicitly skipped). Adjacent: `node-state`, `node-run`, `auto-terminal`, `claim-handle` (including its `### Held variant` subsection), `blob-backend` (parked_payload spills via the same mechanism).

## Invariants

- Cascade does not propagate from `parked` (CLAUDE.md "Held vs failed states").
- The orphan-claim reaper skips `phase='parked'` rows because parked nodes do not heartbeat (`@blessed-invariant 6` exception).
- Time-wake and external-invalidate both transition `parked → stale` (never directly to `running`); the next supervisor tick re-dispatches. Watchdog timeout is the one destructive exit (`parked → failed` with `error_class: "park_timeout"`).
- Held-claim auto-terminal continues to fire correctly across park because `rimsky_claim_holders.state` stays `'active'` while the node is parked.

## Aliases and historical names

The state was added under the platform-extensions design (2026-05-08); migration 006 extends the `phase` CHECK constraint to include `'parked'`.

## Open within this concept

- "No destructive action" (frame-stuck) vs "destructive watchdog timeout" (parked) are sibling timeout disciplines with opposite policies — see `tensions/timeout-policy-asymmetry.md`.

