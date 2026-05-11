---
topic: cascade-fires-on-last-outcome
kind: invariant
---

# Cascade-firing gate is `last_outcome == fresh_changed`, not the raw `changed` bool

## Description

When a node completes successfully, two questions decide whether its dependents get stale-marked: (a) did the node actually change (the executor's `changed: bool` declaration on `Complete`), and (b) does the node's `on_executor_complete` handler override that declaration (`by_changed` / `always_propagate` / `never_propagate`)?

The cascade-firing gate reads the resolved `last_outcome`, not the raw `changed` flag. `rimsky_nodes` carries a `last_outcome` TEXT column alongside `state` (migration `004-last-outcome-and-progress.sql`). The supervisor's terminal-handler path (`foundation/integration/runner_terminal_handlers.go`) computes `last_outcome` from the resolved `on_executor_complete` resolution and writes it in the same UPDATE that lands the row in `fresh` or `failed`. The cascade then fires `cascadeChildrenStaleInTx` and `fanoutRecalculate` (`foundation/integration/cascade_invalidate.go`) only when `last_outcome == "fresh_changed"`.

Five `last_outcome` values (`docs/concepts/node-state.md`):

- `fresh_changed` — node committed and propagated; cascade fires.
- `fresh_unchanged` — node committed without change; cascade does not fire.
- `passed` — handler resolved `pass` (Unavailable / Blocked / Errored skipped without error routing).
- `pure_cascade` — node transitioned `stale → fresh` via dependency cascade only (no executor invocation).
- `failed` — node landed in `failed` via give_up policy or dispatch_impossible.

Under the default `on_executor_complete: { resolve: by_changed }` (also the implicit default when no handler is declared), this is functionally identical to the prior `t.Changed`-based gate. The two non-default resolutions diverge: `always_propagate` forces `last_outcome=fresh_changed` even on `changed:false`; `never_propagate` forces `last_outcome=fresh_unchanged` even on `changed:true`.

`TransitionReason` values like `ReasonPureCascade` and `ReasonInfraReenqueue` (`foundation/cascade/state.go:28-44`) are the audit-trail variant: they distinguish "the cascade itself transitioned this row" from "an executor terminal transitioned this row" — useful in the event log without needing to inspect the `last_outcome` field.

The split between `state` (5-value: `fresh | stale | running | failed | parked`) and `last_outcome` (5-value as above) is deliberate. Packing outcome into state would balloon the state vocabulary to 10+ values and make "is this row eligible for dispatch" a multi-state predicate. Splitting them keeps `state` small (the legal transitions fit on one screen at `cascade/state.go::NextState`) while still capturing the rich vocabulary needed for cascade decisions and observability.

`docs/concepts/cascade.md` is explicit: "`last_outcome` is observability metadata, not a dispatch gate." The dispatch gate (eligibility) reads only `state`; the cascade-firing gate (downstream propagation) reads `last_outcome`. The two run in different code paths.

## Code surface

- `foundation/cascade/state.go:14, 28-44` — `TransitionReason` constants and `LastOutcome` constants.
- `foundation/persistence/postgres/migrations/004-last-outcome-and-progress.sql` — adds the column.
- `foundation/integration/runner_terminal_handlers.go` — terminal-handler resolution → `last_outcome` assignment.
- `foundation/integration/cascade_invalidate.go` — cascade fires gated on `last_outcome == fresh_changed`.
- `foundation/integration/runner_terminal.go` — the main terminal switchboard.

## Prose surface

- `docs/concepts/cascade.md` — "The cascade-firing gate (lazy + last_outcome-driven)" section.
- `docs/concepts/node-state.md` — `last_outcome` table.
- `docs/concepts/handlers.md` — the `on_executor_complete` resolutions (`by_changed`, `always_propagate`, `never_propagate`).
- `CLAUDE.md` "Non-obvious gotchas" — "Cascade-firing gate is last_outcome == fresh_changed, not t.Changed."
- `.ok-planner/specs/2026-05-05-reactive-loops-and-lifecycle-handlers-design.md` — the design that introduced `last_outcome`.

## Adjacent topics

- `2026-05-10-state-machine-no-self-loop` — the state vocabulary that `last_outcome` complements.
- `2026-05-10-frame-resolution-model` — frames carry `last_progress_at`, refreshed per state transition.
- `reactive-loops-and-lifecycle-handlers` — the four handlers, including `on_executor_complete`.

## Observations

- Two distinct vocabularies are at play here: `last_outcome` (5 values: `fresh_changed`, `fresh_unchanged`, `passed`, `pure_cascade`, `failed`) and `TransitionReason` (a larger set including `ReasonHandlerComplete`, `ReasonHandlerError`, `ReasonPureCascade`, `ReasonInfraReenqueue`, `ReasonScheduleFire`, etc.). They overlap conceptually but live in different columns and serve different consumers. The event log records both.
- `docs/concepts/cascade.md` says "lazy + last_outcome-driven"; CLAUDE.md says "fires on last_outcome == fresh_changed, not t.Changed." Both are consistent but use slightly different framings. The actual code at `cascade_invalidate.go` reads the `last_outcome` column directly.
- The `pure_cascade` value documents the no-executor-invocation transition path — a node going from `stale → fresh` because all its upstream values resolved to `fresh_unchanged`. This is distinct from `fresh_changed` (cascade fires) and `fresh_unchanged` (cascade halts at this node). The "halt" semantics are why a single `last_outcome == fresh_changed` predicate suffices for the gate.
- The `last_outcome` column was added as a separate column in migration 004 (post-001-initial). This was a Phase-5-or-later refinement; earlier code referenced a `t.Changed` field directly. The CLAUDE.md gotcha is the cleanup pointer.
