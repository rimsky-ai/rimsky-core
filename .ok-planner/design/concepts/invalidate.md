---
concept: invalidate
status: as-is
aliases: []
references:
  - _discover/2026-05-10-frame-resolution-model.md
  - _discover/2026-05-10-cascade-fires-on-last-outcome.md
  - _discover/reactive-loops-and-lifecycle-handlers.md
---

# Invalidate

## What it is

`invalidate` is the sole graph-level message that the scheduler / control-api / handler emits to mark a node `stale`. There are three emit sites: operator API (`POST /admin/instances/.../invalidate`), error-types policy invalidate (`error_types:` block), and lifecycle-handler `invalidate:` slot. Each carries the target node-types (or `self`) and a `frame: in | next` setting (default `next`).

## Purpose

Rimsky deliberately ships exactly one inter-node message. The richer behaviors (recalculate, retry, parked-wake) are derived from this one message plus scheduler-side rules. This keeps the runtime's effective vocabulary small.

## Boundaries

Owns: the message itself, the three emit sites, the `frame: in | next` discipline. Does NOT own: cascade firing (see `cascade`), schedule fires (see `schedule`), frame creation (see `frame`). Adjacent: `frame`, `cascade`, `lifecycle-handler`, `error-policy`, `parked-state` (admin-invalidate also wakes parked nodes).

## Invariants

- Only one graph-level message exists: `invalidate` (recalculation is a scheduler action, not a service message — CLAUDE.md "Vocabulary").
- Operator-originated invalidates do not preempt running work; they enqueue or coalesce into a frame.
- `frame: in | next` default is `next`; the three emit sites configurable in templates are operator-API, error-types policy, lifecycle-handler.

## Aliases and historical names

The verb "invalidate" replaced an earlier richer message set under the v3 redesign.

## Open within this concept

(no open items distinct from the parent `cascade`/`frame` concepts)

