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

### Park-flavored signals

Park terminals emit canonical signals per `concept:signal`: `terminal/park/snooze` and `terminal/park/await_callback` (the two `ParkReason` enum values). The freeform `parked_reason_label` is a payload field on both signals (no longer a separate column-form distinction at the subscriber boundary). `AwaitAsyncCallback` is NOT a park (the node stays `running` during the callback wait); it emits `transient/await_async` and is covered under `concept:signal`'s transient subtree.

### Resume context

When the runner re-dispatches a parked node, `ExecuteRequest.resume_context` is populated:

```protobuf
message ResumeContext {
  bytes  payload        = 1;  // verbatim from Park.payload
  string session_token  = 2;  // verbatim from Park.session_token
  string resume_reason  = 3;  // "deadline_elapsed" | "external_invalidate"
}
```

Executors use these fields to resume external work. For example, the `claude-agent` reference impl uses `session_token` as the Claude CLI's `--resume <session_id>` argument; a long-running-job executor uses `payload` to carry a job ID so the resumed dispatch can poll the same job.

Two exit paths populate `resume_reason`:

- **Time-based wake** — `SweepParkedNodes` transitions the run when `resume_at` has passed; `resume_reason: "deadline_elapsed"`.
- **External invalidate** — in-graph or admin invalidate against the parked node transitions it back to `stale` and re-dispatches on the next tick; `resume_reason: "external_invalidate"`.

(The third exit path, watchdog timeout, does not re-dispatch — it forces `failed{error_class: "park_timeout"}` and emits no `ResumeContext`.)

## Purpose

Some workloads (human review, scheduled wake, external event wait) cannot finish in a bounded window. `parked` gives them a first-class hold state with explicit resume semantics, instead of forcing them through `failed`+retry (which loses session context) or keeping a gRPC stream open indefinitely.

## Boundaries

Owns: the hold-state schema (park columns on `table:rimsky_node_runs`), the three exit paths (time-wake, external invalidate, watchdog timeout), the `ResumeContext` passed back on re-dispatch. Does NOT own: held-claim resolution (that's `auto-terminal`); orphan reaping (parked rows are explicitly skipped). Adjacent: `node-state`, `node-run`, `auto-terminal`, `claim-handle` (including its `### Held variant` subsection), `blob-backend` (parked_payload spills via the same mechanism).

## Invariants

> **Retracted 2026-05-23** — under the subscriber-driven cascade-fire model introduced by spec 2026-05-23-signal-taxonomy-and-policy-decoupling-design, propagation is determined by subscriber matches against the emitted signal, not by sender color. Parked nodes emit `terminal/park/*` signals; subscribers decide whether to react. The matching retraction lives on `concepts/cascade.md`.

- The orphan-claim reaper skips `phase='parked'` rows because parked nodes do not heartbeat (`@blessed-invariant 6` exception).
- Time-wake and external-invalidate both transition `parked → stale` (never directly to `running`); the next supervisor tick re-dispatches. Watchdog timeout is the one destructive exit (`parked → failed` with `error_class: "park_timeout"`).
- Held-claim auto-terminal continues to fire correctly across park because `rimsky_claim_holders.state` stays `'active'` while the node is parked.

## Aliases and historical names

The state was added under the platform-extensions design (2026-05-08); migration 006 extends the `phase` CHECK constraint to include `'parked'`.

## Open within this concept

- "No destructive action" (frame-stuck) vs "destructive watchdog timeout" (parked) are sibling timeout disciplines with opposite policies — see `tensions/timeout-policy-asymmetry.md`.

## Common pitfalls

- **Indefinite human-review park inside an in-flight frame.** A common pattern is "produce a tentative output, then park indefinitely (no `resume_at`) waiting for an operator to invalidate." Authoring this with `parked_reason: OTHER` and `parked_reason_label: "human_review"` is supported and correct — but parking a frame on review serializes parallel work in the same frame and creates long-lived held frames. The recommended idiom is **post-frame review**: the producing frame runs to completion; review happens externally; a follow-on graph or instance kicks off the post-review work. Frame-blocking review should be reserved for cases where downstream genuinely cannot proceed safely without approval (e.g. cross-system commit where the alternative is to reverse-cascade after the fact).
- Citing `parked_reason: "human_review"` as a string-form reason. Per the 2026-05-22 taxonomy collapse the proto enum is two values (`AWAIT_CALLBACK` / `SNOOZE`) plus a freeform `parked_reason_label`. The string `human_review` belongs in `parked_reason_label` when `parked_reason: AWAIT_CALLBACK`; it is not a first-class enum value.


## Notes

- 2026-05-14: `parked_reason` is now typed (proto enum `ParkReason`); the column stores the snake_case form (`time_wait` / `signal_wait` / `awaiting_human` / `retry_backoff`). New `parked_reason_note` column carries the free-form human annotation. The diagnostics endpoint `?reason=` filter validates against the enum. See spec Piece 2 `.ok-planner/specs/2026-05-14-subscription-cascade-and-quality-of-life-design.md`.
- 2026-05-15: **4-reason taxonomy + freeform label**. The proto enum is `PARK_REASON_UNSPECIFIED | PARK_REASON_TIME_WAIT | PARK_REASON_CALLBACK_WAIT | PARK_REASON_RETRY_BACKOFF | PARK_REASON_OTHER`. The column stores the storage form (`time_wait` / `callback_wait` / `retry_backoff` / `other`); `parked_reason_label` carries the freeform label (required when `parked_reason = other`). The watchdog consults a per-reason `max_park_duration` config (`time_wait: 1h`, `callback_wait: 7d`, `retry_backoff: 1h`, `other: 1h` defaults); timeout produces `failed{error_class: "park_timeout"}`. Bundled emitter updates: long-running-job executors emit `CALLBACK_WAIT`, time-based polling executors emit `TIME_WAIT`, rate-limit-aware executors emit `RETRY_BACKOFF`. See `.ok-planner/specs/2026-05-15-data-platform-extensions-design.md` §Parked-state taxonomy.
- [2026-05-18] Folded content from former `docs/concepts/parked.md` (now retired) — ResumeContext proto snippet added as a subsection under "What it is"; human-review-as-indefinite-park antipattern added as a Common-pitfalls section. The retired doc's `reason: "human_review"` example was a stale string-form reason; rewritten to the modern enum + freeform-label idiom per the 2026-05-15 taxonomy (further superseded by the 2026-05-22 collapse).
- 2026-05-22 — ParkReason enum collapsed from 7 values to 2 (`AWAIT_CALLBACK`, `SNOOZE`) per spec `.ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md`. `PARK_REASON_UNSPECIFIED` and `PARK_REASON_OTHER` removed entirely; `TIME_WAIT` / `RETRY_BACKOFF` collapse to `SNOOZE` (the supervisor-scheduled-wake case); `SIGNAL_WAIT` / `AWAITING_HUMAN` / `CALLBACK_WAIT` collapse to `AWAIT_CALLBACK` (the external-signal case). Executor mapping guidance: long-running-job → `AWAIT_CALLBACK`; time-based polling → `SNOOZE`; rate-limit-aware → `SNOOZE`; awaiting-human → `AWAIT_CALLBACK`. Per-reason `max_park_duration` defaults: `AWAIT_CALLBACK` unbounded (or 24h max); `SNOOZE` capped at `resume_at + grace_window`. Schema `col:rimsky_node_runs.parked_reason` carries a CHECK constraint restricting values to `'await_callback' | 'snooze'`; the runtime's `applyTerminalPark` rejection of `PARK_REASON_UNSPECIFIED` becomes dead code (proto wire layer catches it before the handler runs) and is removed.
- 2026-05-23 — Reshape per spec 2026-05-23-signal-taxonomy-and-policy-decoupling-design. Park terminals emit signals (terminal/park/snooze, terminal/park/await_callback — one per ParkReason value). `parked_reason_label` moves to signal payload. `AwaitAsyncCallback` is not a park (node stays running) — it emits `transient/await_async`; see `concept:signal`. The "Cascade does not propagate from parked" invariant retracts (matching retraction on `concepts/cascade.md`).
- 2026-05-24 — concept:breakpoint is the operator-injected sibling to executor-emitted parked-state. Breakpoint pause-mode blocks the runner at supervisor checkpoints; parked-state is the executor's own hold via Park terminal. The two are distinct primitives serving different control directions; per spec 2026-05-24-instance-debugger-design.
