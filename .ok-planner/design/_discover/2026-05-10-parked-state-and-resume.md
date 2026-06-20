---
topic: parked-state-and-resume
kind: schema
---

# `parked` is a non-terminal hold state with executor-driven session-resume; cascade does not propagate from parked

## Description

Some agentic workloads can't finish in a bounded window — they wait for human input, an external event, or a scheduled wake. A node that needs to wait could keep its gRPC stream open indefinitely (impractical), report `Errored` and retry (loses session context), or enter a hold state with explicit resume semantics. Rimsky's choice: a fifth node state, `parked`, with executor-emitted `ParkRequested` as the entrance and three exit paths.

**State-machine**: `parked` is the fifth legal node state (`foundation/cascade/state.go:110-117`). Entered from `running` under `handler_park` (executor emitted `ParkRequested`). Exits via:

1. **Time-based wake** — `SweepParkedNodes` (`foundation/integration/sweep_parked.go`) processes `resume_at`. Transitions parked → stale; next supervisor tick picks up the stale row and re-dispatches with `ResumeContext.resume_reason = "deadline_elapsed"`.
2. **External invalidate** — an in-graph or admin invalidate against the parked node transitions parked → stale. Re-dispatch carries `resume_reason = "external_invalidate"`. The unified invalidate handler treats parked-node invalidates the same as admin-invalidates.
3. **Watchdog timeout** — `max_park_duration` is the operator's safety cap; when `parked_at + max_park_duration < now()`, the watchdog forces parked → failed with `error_class: "park_timeout"`.

Cascade does NOT propagate from `parked` (CLAUDE.md "Held vs. failed states"). Held claims are retained across the park boundary; the orphan-claim reaper skips `phase='parked'` rows because heartbeating is paused during park (the orphan-cutoff adjustment per CLAUDE.md "Non-obvious gotchas").

**Schema**: `rimsky_worker_request` adds the following columns (migration 006):

- `parked_at` — when the executor emitted `ParkRequested`.
- `resume_at` — optional; deadline for time-based wake.
- `parked_payload_inline` / `parked_payload_handle` / `parked_payload_handle_backend` — opaque payload, inline below threshold or spilled via `BlobBackend`.
- `session_token` — opaque token executors use to correlate across the park.
- `parked_reason` — operator-facing reason string ("human_review", "rate_limit", etc.).
- `wake_reason` — set on resume; mirrors `resume_reason` in `ResumeContext`.

The phase CHECK is extended to include `'parked'` alongside `'pending' | 'active' | 'held' | 'completed'`.

`ParkRequested` (`protocols/proto/v1/executor.proto:167-190`) carries opaque payload + optional `session_token` + optional `resume_at`. On resume, rimsky passes back `ResumeContext` populated from these values:

```protobuf
message ResumeContext {
  bytes  payload        = 1;  // verbatim from ParkRequested.payload
  string session_token  = 2;  // verbatim from ParkRequested.session_token
  string resume_reason  = 3;  // "deadline_elapsed" | "external_invalidate"
}
```

Executors use these to resume external work — `executors/claude-agent/` uses `session_token` as the Claude CLI's `--resume <session_id>` argument.

`docs/concepts/parked.md` is the operator-facing rendering, including the "human review = indefinite park" idiom: an executor produces a tentative output, parks indefinitely (no `resume_at`) with `reason: "human_review"`. Operators inspect via `GET /admin/diagnostics/parked-nodes`, act externally, and call admin-invalidate to wake. The "mid-frame human review is antipattern" advice steers operators toward post-frame review when possible.

## Code surface

- `foundation/cascade/state.go:110-117` — five legal states.
- `foundation/cascade/state.go:177-193` — parked transition rules.
- `foundation/persistence/postgres/migrations/006-platform-extensions-park-blob-events.sql:13-40` — schema.
- `foundation/integration/runner_terminal_park.go` — `ParkRequested` handler.
- `foundation/integration/sweep_parked.go` — time-based wake.
- `foundation/integration/wake_parked.go` — external-invalidate wake.
- `protocols/proto/v1/executor.proto:167-208` — `ParkRequested`, `ResumeContext`.
- `modeling/controlapi/diagnostics.go` — `GET /admin/diagnostics/parked-nodes`.

## Prose surface

- `docs/concepts/parked.md` — concept doc (the most thorough prose for this).
- `docs/concepts/holding-subgraph.md` — held claims persist across park.
- `CLAUDE.md` "Held vs. failed states" — distinction.
- `CLAUDE.md` "Non-obvious gotchas" — "Parked nodes do not heartbeat."
- `.ok-planner/specs/2026-05-08-platform-extensions-for-agent-consumers-design.md` — design that added park.

## Adjacent topics

- `2026-05-10-auto-terminal-aggregate-resolution` — auto-terminal still works across park.
- `2026-05-10-blob-spill-pluggable-backends` — parked payloads spill via BlobBackend.
- `2026-05-10-worker-request-phase-lifecycle` — `parked` phase value.
- `2026-05-10-state-machine-no-self-loop` — `parked → fresh` is explicitly rejected.

## Observations

- Three exit paths, two of which (time-based wake and external invalidate) route through `stale`, never directly to `running`. This is so the next supervisor tick's `SelectCandidates` picks up the wake — the wake supervisor doesn't have to be the one that has an executor pool. The third path (watchdog timeout) routes parked → failed.
- The `wake_reason` column on the worker_request row records what woke the node, available for audit. The `resume_reason` in `ResumeContext` is the executor-facing version of the same value.
- `parked → fresh` is explicitly rejected (CLAUDE.md "Non-obvious gotchas"); a parked node re-enters work via the standard dispatch path. This means `last_outcome` only updates on the eventual `Complete`, not on the park transition.
- The held-claim auto-terminal mechanism continues to fire correctly across park because the `rimsky_claim_holders` row stays in `state='active'` while the node is parked. A long park therefore extends the producer's `Open` lifetime indefinitely — producer's TTL needs to be longer than `max_park_duration` to be safe.
- The phrase "no destructive action" appears in `frame-stuck-is-advisory.md` as a parallel discipline; the watchdog timeout for parked is the exception that does take destructive action (parked → failed). The two timeouts are about different things: frame-stuck observes lack of progress on a frame; park-timeout observes time elapsed while parked.
