---
concept: parked-state
status: as-is
aliases:
  - park
  - parked node
---

# Parked state

## What it is

`parked` is the fifth legal node state, entered from `running` when the executor emits a park outcome. While parked, the node is not running and not failed; it carries a `parked_payload`, optional `session_token`, optional `resume_at`, and `parked_reason`. The corresponding node-run phase is `'parked'`.

### Park-flavored signals

Park terminals emit canonical signals per `concept:signal`: `terminal/park/snooze` and `terminal/park/await_callback` (the two park-reason values). The freeform `parked_reason_label` is a payload field on both signals, not a separate column-form distinction at the subscriber boundary. The await-async-callback outcome is NOT a park (the node stays `running` during the callback wait); it emits `transient/await_async` and is covered under `concept:signal`'s transient subtree.

### Resume context

When the runner re-dispatches a parked node, a resume-context is populated on the execute request. It carries three fields: the parked payload (verbatim from the original park), an optional session token (verbatim from the original park), and a resume-reason discriminator (`"deadline_elapsed"` or `"external_invalidate"`).

Executors use these fields to resume external work. For example, an agent executor uses the session token to resume the external session it had open before parking; a long-running-job executor uses the parked payload to carry a job ID so the resumed dispatch can poll the same job.

Two exit paths populate `resume_reason`:

- **Time-based wake** — the parked-node sweep transitions the run when `resume_at` has passed; `resume_reason: "deadline_elapsed"`.
- **External invalidate** — in-graph or admin invalidate against the parked node transitions it back to `stale` and re-dispatches on the next tick; `resume_reason: "external_invalidate"`.

(The third exit path, watchdog timeout, does not re-dispatch — it forces `failed{error_class: "park_timeout"}` and emits no resume context.)

## Purpose

Some workloads (human review, scheduled wake, external event wait) cannot finish in a bounded window. `parked` gives them a first-class hold state with explicit resume semantics, instead of forcing them through `failed`+retry (which loses session context) or keeping a gRPC stream open indefinitely.

## Boundaries

Owns: the hold-state schema (the park fields on the node-run row), the three exit paths (time-wake, external invalidate, watchdog timeout), the resume context passed back on re-dispatch. Does NOT own: held-claim resolution (that's `auto-terminal`); orphan reaping (parked rows are explicitly skipped). Adjacent: `node-run`, `auto-terminal`, `claim-handle` (including its held variant), `blob-backend` (parked_payload spills via the same mechanism).

## Invariants

- Parked nodes emit `terminal/park/*` signals; subscribers decide whether to react (propagation is determined by subscriber matches against the emitted signal, not by sender color).
- The orphan-claim reaper skips `phase='parked'` rows because parked nodes do not heartbeat (`@blessed-invariant 6` exception).
- Time-wake and external-invalidate both transition `parked → stale` (never directly to `running`); the next supervisor tick re-dispatches. Watchdog timeout is the one destructive exit (`parked → failed` with `error_class: "park_timeout"`).
- Held-claim auto-terminal continues to fire correctly across park because the claim-holder's state stays `'active'` while the node is parked.

## Common pitfalls

- **Indefinite human-review park inside an in-flight frame.** A common pattern is "produce a tentative output, then park indefinitely (no `resume_at`) waiting for an operator to invalidate." Authoring this with `parked_reason: OTHER` and `parked_reason_label: "human_review"` is supported and correct — but parking a frame on review serializes parallel work in the same frame and creates long-lived held frames. The recommended idiom is **post-frame review**: the producing frame runs to completion; review happens externally; a follow-on graph or instance kicks off the post-review work. Frame-blocking review should be reserved for cases where downstream genuinely cannot proceed safely without approval (e.g. cross-system commit where the alternative is to reverse-cascade after the fact).
- Citing `parked_reason: "human_review"` as a string-form reason. The proto enum is two values (`AWAIT_CALLBACK` / `SNOOZE`) plus a freeform `parked_reason_label`. The string `human_review` belongs in `parked_reason_label` when `parked_reason: AWAIT_CALLBACK`; it is not a first-class enum value.
