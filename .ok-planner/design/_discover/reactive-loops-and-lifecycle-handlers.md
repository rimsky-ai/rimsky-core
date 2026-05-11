---
topic: reactive-loops-and-lifecycle-handlers
kind: concept
---

# Four lifecycle handlers + `on_event` map drive the supervisor's terminal-event response

## Description

A node's reactive policy is expressed as a set of declarative handlers in the template DSL. Each handler maps one event from the executor protocol (or one lifecycle event) onto a small action vocabulary: `pass | retry | error` (most slots) plus a complete-time vocabulary `by_changed | always_propagate | never_propagate` plus the named-event `on_event` map. The supervisor's terminal pipeline routes every event through the matching handler's `resolve` to decide cascade behavior, and an optional `invalidate` slot fires unconditionally alongside `resolve`.

`docs/concepts/handlers.md` is the canonical concept document. The five slots:

- **`on_acquire_unavailable`** — when any required claim's `Open` returns `Unavailable`. Resolves: `pass | retry | error`.
- **`on_executor_complete`** — when the executor emits `Complete`. Resolves: `by_changed | always_propagate | never_propagate`.
- **`on_executor_blocked`** — when the executor emits `Blocked`. Resolves: `pass | error`.
- **`on_executor_errored`** — when the executor emits `Errored`. Resolves: `pass | retry | error`.
- **`on_event`** — per-event-name map keyed by names declared in the executor's `Capabilities.declared_events`. Each entry has the same shape: `resolve` + optional `invalidate`. Non-terminal: a node may emit any number of named events between start and its terminal event.

`docs/concepts/handlers.md` "Resolve verdicts" tabulates valid verdicts per slot:

| Verdict             | Cascade behavior                                      | Valid in                                  |
|---------------------|-------------------------------------------------------|-------------------------------------------|
| `pass`              | Treats the event as no-op for cascade.                | acquire_unavailable, blocked, errored, on_event |
| `retry`             | Re-enqueues the dispatch after a backoff.             | acquire_unavailable, errored, on_event    |
| `error`             | Forces the node to `failed` with `error_class`.       | acquire_unavailable, blocked, errored, on_event |
| `by_changed`        | Default for Complete: cascade fires iff `changed=true`. | complete                                |
| `always_propagate`  | Force `fresh_changed`; cascade fires regardless of `changed`. | complete                          |
| `never_propagate`   | Force `fresh_unchanged`; cascade does not fire even if `changed=true`. | complete                  |

The handler `invalidate` slot fires unconditionally when its handler runs. Targets are template node types or `self`; `frame` is `in` (same frame the source dispatched in) or `next` (default — a fresh frame). The invalidate-emitted target can be a parked node — the unified invalidate handler wakes it the same way `POST /admin/instances/.../invalidate` does (with `resume_reason: "external_invalidate"`).

Implementation lives across multiple files:

- `foundation/integration/runner_terminal.go` — main terminal switchboard.
- `foundation/integration/runner_terminal_handlers.go` — per-slot resolution logic.
- `foundation/integration/runner_terminal_errors.go` — `on_executor_errored` policy chain (error-types).
- `foundation/integration/runner_terminal_park.go` — `ParkRequested` handler.
- `foundation/integration/runner_terminal_release.go` — release-side bookkeeping.
- `foundation/integration/runner_named_events.go` — `NamedEvent` → `on_event` dispatch.
- `foundation/integration/on_error.go` — error-policy chain.

The validation at template registration cross-checks `on_event` keys against the executor's `Capabilities.declared_events`. Templates referring to undeclared events are rejected at registration when the executor is reachable (`modeling/observability/discovery.go`). When the executor is unreachable, the cross-check is skipped silently — unknown event names at runtime are treated as no-ops (CLAUDE.md "Non-obvious gotchas").

The `pass` and `error` resolutions on `on_acquire_unavailable` / `on_executor_blocked` / `on_executor_errored` call `Abandon` on already-Open'd claims (matching `handleOrphanedClaim` semantics — CLAUDE.md "Non-obvious gotchas"). The verb fires before the state-transition tx; producer-side state is cleaned up first.

Per-emit `frame: in | next` discipline applies to every invalidate emit (operator-API, error-types policy, lifecycle-handler). Default is `next`.

## Code surface

- `foundation/integration/runner_terminal.go`, `runner_terminal_handlers.go`, `runner_terminal_errors.go`, `runner_terminal_park.go`, `runner_terminal_release.go`, `runner_named_events.go`, `on_error.go` — entire family of files.
- `modeling/node/` — template-node spec (handler declarations live here).
- `modeling/observability/discovery.go` — `declared_events` cross-check site.
- `protocols/proto/v1/executor.proto:131-208` — terminal-event variants.

## Prose surface

- `docs/concepts/handlers.md` — concept-doc treatment (the primary surface).
- `docs/concepts/error-policy.md` — `error_types` chain.
- `docs/concepts/cascade.md` — `on_executor_complete` resolution gates cascade.
- `docs/concepts/invalidate.md` — `frame: in | next` per-emit discipline.
- `CLAUDE.md` "Vocabulary" — 4 declarable lifecycle handlers + `on_event` map.
- `CLAUDE.md` "Non-obvious gotchas" — pass/error on Unavailable/Blocked/Errored fires `Abandon`.
- `.ok-planner/specs/2026-05-05-reactive-loops-and-lifecycle-handlers-design.md` — the design.

## Adjacent topics

- `terminal-resolution` — Stage 3 in the end-to-end terminal flow; this entry is Stage 3 internals.
- `2026-05-10-cascade-fires-on-last-outcome` — `on_executor_complete` resolutions feed `last_outcome`.
- `2026-05-10-attribute-substitution-grammar` — `on_event` reads `declared_events`.
- `2026-05-10-event-log-append-only-jsonb` — `rimsky_node_events` carries the named-event payloads.
- `2026-05-10-observability-optional-protocols` — `declared_events` source of truth.
- `error-policy-retry-loop-cap` — `max_retries_without_progress` plays into `on_executor_errored`.

## Observations

- "Four handlers + `on_event` map" makes the structure five slots in code but four in some prose (`docs/concepts/handlers.md` says "five slots"; CLAUDE.md "Vocabulary" says "4 declarable lifecycle handlers + on_event handler map"). Both are correct framings; the `on_event` map is shaped differently (key-indexed) but uses the same resolve+invalidate vocabulary.
- The `on_event` map is keyed by names declared in `Capabilities.declared_events`. The runtime treats unknown event names as no-ops (CLAUDE.md) — this allows graceful evolution but means a template typo in an event name silently does nothing.
- The `invalidate` slot's `frame: in | next` defaults to `next`. `docs/concepts/invalidate.md` calls out three sites where the default applies: operator API, error-types policy invalidate, lifecycle-handler invalidate. Cascade-on-commit and pure-cascade scheduler walks are NOT configurable — they're scheduler actions.
- The handler-emitted `Abandon` on `pass`/`error` of already-Opened claims is a subtle interaction with `2026-05-10-orphan-reaper-no-producer-abandon`: the handler knows what was Open'd (just like the bail path), so it can fire `Abandon` explicitly; the periodic reaper cannot.
