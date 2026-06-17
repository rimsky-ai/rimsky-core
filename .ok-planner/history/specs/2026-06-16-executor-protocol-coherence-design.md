---
spec: executor-protocol-coherence
date: 2026-06-16
---

# Executor protocol coherence

## Summary

Refactor the executor protocol surface to remove redundant mechanisms (the resume-context channel duplicates attribute carry-forward; the streaming Execute RPC carries only a single terminal verdict in practice; the NamedEvent stream event is processed batch-at-terminal anyway), unify the settling-terminal shape (Success / Error / Park all carry `attributes_delta` and a `tags` set), and replace the streaming protocol with unary RPC plus async callback. Liveness becomes opt-in via writeback or a dedicated keepalive endpoint, with three orthogonal deadlines (`sync_rpc_deadline`, `max_quiet_period`, `max_runtime`) all accepting `0` to disable.

This is a refactor / simplification / clarification spec — no new user-observable capability. The mechanism shifts substantially but the running product behaves the same for operators and template authors. Executor authors see a smaller, more uniform protocol surface.

## Mechanism

### The single dispatch verb

Today's executor protocol uses server-streaming gRPC: the supervisor calls `Execute(req)`, the executor opens a stream and pushes `Heartbeat`, `NamedEvent`, and a final `StreamClose` carrying the outcome. In practice the stream carries only heartbeats (a weak liveness signal — easy to fake with a separate ticker goroutine that pings even when the main task stalls) and the eventual terminal verdict. `NamedEvent` processing is already batched at terminal time (`code:lib/runtime/runner_named_events.go::processNamedEvents#45`), so its streaming-ness is cosmetic.

Under the new protocol, `Execute` becomes unary: `Execute(req) → Outcome`. The Outcome is one of `Success | Error | Park | AwaitAsyncCallback`. The stream concept is gone entirely.

Sync executors run the request, do the work, return the verdict. The supervisor's gRPC client holds the connection open until the response or the `sync_rpc_deadline` expires. The RPC connection state IS the liveness signal: process death breaks the connection, deadline cancels the RPC, both surface to the supervisor immediately. Nothing separate to fake.

Async executors return `AwaitAsyncCallback` with an `async_ack_id` immediately. The eventual real verdict arrives via HTTP POST to `route:POST /v1/callback/{async_ack_id}` carrying an `AsyncCallbackBody` whose `outcome` is one of `Success | Error | Park`. The callback registry moves from in-memory (today's `code:lib/runtime/callback.go::CallbackRegistry#88`) to persistent storage so supervisor restart does not lose pending registrations.

### Uniform settling terminals

The three settling terminals — Success, Error, Park — gain a uniform shape:

- `attributes_delta: google.protobuf.Struct` — universal attribute writeback, committed atomically with the verdict
- `tags: repeated string` — set semantics (deduplicated at decode), replacing `NamedEvent`'s `name` field
- `scratch: bytes` — opaque per-dispatch state (already on all three)
- Type-specific fields: `changed` / `change_summary` for Success; `error_class` / `payload` for Error; `reason` / `resume_at` / `reason_note` / `reason_label` for Park

`Park.payload` and `Park.session_token` are removed — resume state moves to attribute carry-forward.

`NamedEvent` is removed entirely from the protocol. `Heartbeat` is removed entirely. `ExecuteEvent` and `StreamClose` go away (the unary RPC's response is the outcome directly). `AsyncCallbackBody.events` is removed (per-emission events fold into the outcome's `tags` set, with per-emission data riding `attributes_delta`).

### Tags replace named events end-to-end

A node that today emits `NamedEvent{name: "data_ready", payload: ...}` and then closes with Success becomes `Success{tags: ["data_ready"], attributes_delta: {...}}` — the per-emission data rides `attributes_delta` directly instead of inside an opaque payload.

`table:rimsky_node_events` (the named-event ledger) is removed entirely. Per-emission persistence collapses into the dispatch row's `tags` column (or junction table; the persistence layer's choice).

The signal taxonomy in `concept:signal` removes the `event/<name>` leaf. Subscribers that today subscribe to `event/<name>` from a sender shift to `subscribes: [{node: <sender>, type: terminal/*, when: "<tag>" in payload.tags}]`. The CEL `when:` filter on tag presence replaces the type-path leaf.

The substitution path `nodes.<emitter>.event.<name>.<json_path>` is removed. Per-emission data lives in `attributes_delta`, available via the existing `nodes.<emitter>.attributes.<key>` substitution path.

### Liveness: three deadlines plus two opt-in keepalive channels

Three deadlines per dispatch, orthogonal, each accepting `0` to disable:

1. **`sync_rpc_deadline`** (executor-template-level, default 30s). Cancels the unary `Execute` RPC if exceeded. Failure surfaces immediately as the RPC's error path.
2. **`max_quiet_period`** (executor-template-level or per-node, default 0 = disabled). Maximum time between liveness signals during an async dispatch. Sweep-fails the dispatch with `error_class: "executor_quiet"` if exceeded.
3. **`max_runtime`** (per-node, default 0 = disabled). Absolute upper bound on dispatch wall-clock runtime. Sweep-fails the dispatch with `error_class: "max_runtime_exceeded"` if exceeded.

Two liveness signals an async executor can use, opt-in per dispatch:

- **Dedicated keepalive**: `route:POST /v1/runs/{run_id}/keepalive`. No payload, no side effects beyond bumping `last_progress_at` on the dispatch row. Authenticated by the existing `cancel_token`. Cheap and explicit.
- **Attribute writeback as incidental keepalive**: the existing §12.5 `route:POST /v1/runs/{run_id}/attributes` callback bumps `last_progress_at` as a side effect of any genuine attribute write. Executors that report real progress via attributes get liveness for free.

A sync executor needs no liveness configuration — the RPC connection IS the liveness signal. An async executor that opts in to neither keepalive nor writeback gets only the `max_runtime` enforcement; that's a valid choice for fire-and-forget async dispatches with a known absolute deadline.

### Orphan reaper under the new model

Today's orphan-claim reaper keys on heartbeat-loss (`OrphanedClaimTimeout = 5 × HeartbeatTimeout`). With heartbeats gone:

- For sync dispatches: orphan detection is in-band on the supervisor's gRPC client. When the outgoing `Execute` RPC's connection breaks, the supervisor knows immediately and cleans up the claim. Cross-process orphan (the original supervisor died holding a claim) is detected by a different supervisor seeing a stale claim past the dispatch's `max_runtime`.
- For async dispatches: orphan = no writeback / keepalive within `max_quiet_period` (when set) AND no callback within `max_runtime` (when set). The existing parked-sweep-style periodic check handles it.

The replacement keys on observable signals (connection state, persistent writeback / keepalive timestamps) instead of a fakeable mid-stream signal (heartbeats).

### Claude-agent under the new model

`claude-agent` is the canonical async case. Profile after this spec:

- Dispatch path: async. On `Execute(req)`, the executor returns `AwaitAsyncCallback` immediately and spawns the CLI subprocess in the background.
- Liveness: `max_quiet_period` set generously (e.g., 5 minutes — the CLI takes a while between tool-call events). `max_runtime = 0` (never artificially kill an LLM mid-thought). Keepalive POSTs sent on natural CLI milestones (tool call, turn boundary).
- Session resume: session token rides `attributes_delta` on every terminal (Success in the loop case, Park in the rate-limit case). On resume dispatch, the executor reads `req.attributes.session_token` and starts the CLI with `--resume <token>`. The existing dual-path code at `code:lib/services/executors/claude-agent/src/server.ts::resolveEffectiveResumeContext#955` collapses to the attribute branch only.
- Terminal verdict: arrives at the supervisor via the persisted async-callback registry once the CLI subprocess settles.

### Recovery-disposition rename

The prior-dispatch disposition enum (`proto:executor.proto::PriorDispatchDisposition`) currently has `PRIOR_HEARTBEAT_STALE` for the case where the supervisor's heartbeat-loss sweep transitioned the prior run. With heartbeats gone, the equivalent recovery cause is "quiet-period stale" (async dispatch went silent past `max_quiet_period`) or "rpc-broken stale" (sync dispatch's RPC died unexpectedly). The enum value renames to `PRIOR_STALE_RECOVERY` to cover both cases without overspecifying which signal failed.

## Technical decisions

> **TD-execute-rpc-unary** — Execute becomes unary, not server-streaming.
> **Choice:** `rpc Execute(ExecuteRequest) returns (Outcome)` — unary. `Outcome` is a oneof of `Success | Error | Park | AwaitAsyncCallback`.
> **Rationale:** Today's stream carries only Heartbeats (weak liveness signal) and the eventual terminal verdict. With NamedEvent collapsed and heartbeats removed, nothing actually streams. Unary is honest about the dispatch pattern and removes the stream-reader code path on both sides.
> **Alternatives:** Keep server-streaming with NamedEvent/Heartbeat removed (rejected — preserves protocol complexity without any payoff).

> **TD-collapse-named-event-to-tags** — NamedEvent removed; settling terminals carry a `tags` set.
> **Choice:** Remove the `NamedEvent` message and the `ExecuteEvent.named_event` variant. Add `repeated string tags` (set semantics, deduplicated at decode) to `Success`, `Error`, and `Park`. Rename the observability-protocol capability `ObservabilityCapabilities.declared_events` to `declared_tags` (same wire purpose — the executor's advertised vocabulary that the template-registration gate validates against — under the new name).
> **Rationale:** NamedEvent's existing runtime semantics are batch-at-terminal — the runner captures events during the stream but processes them only at terminal time. The streaming-ness was cosmetic. Tags collapse multi-emit into a set-on-terminal, removing the parallel ledger, the `event/<name>` signal taxonomy leaf, the substitution path, and the per-event audit emit. Per-emission data moves to `attributes_delta`.
> **Alternatives:** Bundle NamedEvent into the terminal body as `repeated NamedEvent events` (rejected — preserves NamedEvent's overhead without buying anything; tags are cleaner).

> **TD-attributes-delta-on-all-settling-terminals** — Success, Error, and Park all carry `attributes_delta`.
> **Choice:** Add `google.protobuf.Struct attributes_delta` to `Error` and `Park`. (`Success` already has it.)
> **Rationale:** All three are cascade-firing terminals that settle the dispatch atomically. Attribute writeback should ride the same transaction as the verdict commit, uniformly. Today's asymmetry forces executors that want to write attributes on Error or Park to use the §12.5 mid-dispatch callback, creating a parallel mechanism for what should be uniform.
> **Alternatives:** None considered (the alternative is the current asymmetry).

> **TD-remove-resume-context** — Resume context channel removed; executors carry resume state via attribute carry-forward.
> **Choice:** Remove `Park.payload`, `Park.session_token`. Remove `ExecuteRequest.resume_context` and the `ResumeContext` message. Executors that need state across a park-and-resume use `attributes_delta` to commit it at terminal and `ExecuteRequest.attributes` to read it on resume.
> **Rationale:** The resume-context channel duplicated what attribute carry-forward already provides. `decision:claude-agent-session-attribute` already shows the attribute path works for the canonical use case. Two parallel mechanisms violate Plumbline's "one idiom per job."
> **Alternatives:** Keep resume context as the primary channel for Park-specific state (rejected — redundant with attribute carry-forward).

> **TD-persist-async-callback-registry** — Async-callback registry moves from in-memory to persistent.
> **Choice:** Add `async_ack_id text` and `async_ack_registered_at timestamp` columns to `table:rimsky_node_runs`; index on `async_ack_id` for the callback handler's lookup. On AwaitAsyncCallback the supervisor writes the registration in the same tx as the dispatch-state mutation; on callback the handler looks up the dispatch row by `async_ack_id`.
> **Rationale:** With AwaitAsyncCallback as a primary mode, the in-memory registry's restart-fragility is unacceptable. A callback arriving after a supervisor restart must land on the correct dispatch row; the in-memory map cannot survive.
> **Alternatives:** Separate `rimsky_async_callbacks` table (rejected — a column on the dispatch row is sufficient and avoids a cross-table join on the hot lookup path).

> **TD-three-dispatch-deadlines** — Three orthogonal dispatch deadlines, all accept `0` = disabled.
> **Choice:** `sync_rpc_deadline` (executor-template-level, default 30s), `max_quiet_period` (executor-template-level or per-node, default 0 = disabled), `max_runtime` (per-node, default 0 = disabled). Each is independently enforced by the supervisor / scheduler sweeps.
> **Rationale:** Sync needs a deadline on the RPC; async needs a way to detect "executor went quiet"; both benefit from an absolute upper bound for safety. The three answer different questions, so they need to be independently configurable. `0 = disabled` is necessary for workloads where artificial caps are anti-features (LLM-driven work, multi-day human review).
> **Alternatives:** Single unified deadline (rejected — conflates three orthogonal concerns); `0` meaning "use default" (rejected — collides with the disable semantic the LLM / human-review use cases need).

> **TD-keepalive-endpoint** — Dedicated keepalive endpoint, no payload.
> **Choice:** New `route:POST /v1/runs/{run_id}/keepalive` endpoint. Authenticated via the existing `cancel_token`. No request body. Returns 204 No Content on success (matching §12.5's convention), 401 on auth failure, 404 on unknown run. Side effect: bumps `last_progress_at` on the dispatch row.
> **Rationale:** Async executors that don't have meaningful attribute updates need an explicit liveness primitive that doesn't pollute the attribute bag with dummy values. Dedicated endpoint keeps the liveness purpose distinct from the attribute-writeback purpose.
> **Alternatives:** Reuse §12.5 attribute writeback only (rejected — forces meaningless writes for liveness); reverse polling via a `Ping` RPC on the executor (rejected — supervisor-side polling load, executor-side state burden).

> **TD-writeback-bumps-progress** — §12.5 attribute writeback bumps `last_progress_at` as a side effect.
> **Choice:** Each `route:POST /v1/runs/{run_id}/attributes` call updates `last_progress_at` on the dispatch row in the same tx as the attribute write.
> **Rationale:** A genuine attribute writeback IS a liveness signal — the executor did real work and is reporting it. Avoid an extra round-trip when writeback is already happening.
> **Alternatives:** Require explicit keepalive even alongside writeback (rejected — redundant when the writeback already proves liveness).

> **TD-subscription-grammar-shift** — Subscription `type:` shifts from `event/<name>` to `terminal/<kind>` plus CEL tag filter.
> **Choice:** Remove the `event/<name>` leaf from `concept:signal`'s taxonomy. Subscribers that today express `subscribes: [{node: <sender>, type: event/<name>}]` shift to `subscribes: [{node: <sender>, type: terminal/*, when: "<name>" in payload.tags}]`. The CEL `when:` filter on `payload.tags` (bound to the terminal's tag set) replaces the type-path leaf. The CEL environment for `terminal/*` payloads binds `tags: list<string>` for `in` predicates.
> **Rationale:** With tags collapsing the per-emission ledger into terminal-level metadata, the subscription surface has to follow. Type-path subscription can no longer express "this specific named event" — it expresses "this terminal kind" plus a CEL filter on the tag set.
> **Alternatives:** Synthesize `event/<name>` as a virtual leaf computed from tag presence (rejected — adds parser complexity for a taxonomy that no longer matches the persistence).

> **TD-remove-event-substitution-path** — `nodes.<emitter>.event.<name>.<json_path>` substitution path removed.
> **Choice:** Remove the named-event substitution path entirely. Per-emission data lives in `attributes_delta`, available via the existing `nodes.<emitter>.attributes.<key>` substitution path.
> **Rationale:** With named events collapsed and `table:rimsky_node_events` removed, the substitution path has nothing to read from. Per-emission data was always more honestly attribute data.
> **Alternatives:** None — fully redundant once the ledger is removed.

> **TD-orphan-reaper-no-heartbeat** — Orphan reaper keys on RPC connection state (sync) or writeback / keepalive quiet (async).
> **Choice:** For sync dispatches, the supervisor's gRPC client failure drives in-band claim cleanup. For async dispatches, the parked-sweep-style periodic check keys on `now - last_progress_at > max_quiet_period` (when set) and `now - dispatched_at > max_runtime` (when set). Heartbeat-loss detection is removed entirely.
> **Rationale:** Heartbeat-loss is gone with streaming; the replacements are honest signals — connection-state observation (sync) and persistent quiet-period detection (async). Both observe real signals rather than easily-faked heartbeats.
> **Alternatives:** Synthesize a "soft heartbeat" from §12.5 writeback alone (rejected — covered above by the keepalive endpoint + writeback dual mechanism).

> **TD-claude-agent-session-attribute-only** — claude-agent's session token rides attribute carry-forward only.
> **Choice:** claude-agent writes `session_token: runId` to `attributes_delta` on every settling terminal (Success in the loop case, Park in the rate-limit case). On resume, the executor reads `req.attributes.session_token` and starts the CLI with `--resume <token>`. The dual-path code in `code:lib/services/executors/claude-agent/src/server.ts::resolveEffectiveResumeContext#955` collapses to the attribute branch only.
> **Rationale:** Required by TD-remove-resume-context — `Park.sessionToken` and `ExecuteRequest.resume_context` no longer exist. The attribute path was already wired and proven for the loop case; this extension covers the Park case.
> **Alternatives:** None — required by TD-remove-resume-context.

> **TD-prior-stale-rename** — `PRIOR_HEARTBEAT_STALE` renames to `PRIOR_STALE_RECOVERY`.
> **Choice:** Rename the enum value `proto:executor.proto::PriorDispatchDisposition.PRIOR_HEARTBEAT_STALE` to `PRIOR_STALE_RECOVERY`. The disposition covers both async quiet-period stale and sync RPC-broken stale, without overspecifying which signal failed.
> **Rationale:** With heartbeats removed, the enum value's name is misleading. The recovery semantics are the same regardless of which detection signal fired; the enum should reflect "the prior run was reaped as stale" without naming the specific stale-detection mechanism.
> **Alternatives:** Two enum values, one per cause (rejected — the executor's recovery logic is the same either way; the discriminator carries no useful information).

## Design changes

- Concept: mutate `concepts/executor.md` in place. Replace the "What it is" section with: *An executor implements the gRPC executor's unary Execute method plus an optional executor-observability protocol. Implementations come in two forms — in-process handlers registered with the dispatch pool, and out-of-process services (gRPC or HTTP-bridge) — and the protocol surface (execute, the four outcome variants, the observability handshake) is identical across both. The executor receives one execute request and returns one Outcome — exactly one of `Success | Error | Park | AwaitAsyncCallback`. The Park outcome carries an inner park reason from the closed two-value set `AWAIT_CALLBACK | SNOOZE` (per `concept:parked-state`). The AwaitAsyncCallback outcome defers the verdict to a later HTTP POST against the supervisor's callback URL carrying an `AsyncCallbackBody` whose own outcome is one of the three settling terminals. Production-side reference implementations live on the consumption side, outside the platform. The stub test-double executor and the bundled in-process loop-counter handler are the in-rimsky implementations.* Replace the "Scratch" section with: *Every executor receives a scratch field on its execute request carrying the dispatch row's currently persisted scratch bytes (empty on first dispatch). The executor may write scratch in two ways — mid-dispatch by POSTing to a scratch HTTP callback route (paralleling the executor protocol's existing attributes incremental-writeback HTTP callback), or by attaching scratch bytes to the settling outcome (success, error, or park; the await-async-callback outcome carries no scratch since it does not settle the dispatch). Both writes persist on the dispatch row. The bytes are opaque to rimsky — the inertness invariant (`concept:inertness` / `@blessed-invariant 21`) extends to scratch — and scratch carries forward to a successor dispatch of the same node only via the three recovery enqueue paths that stamp `prior_dispatch_id` on the new row (stale-recovery, retry-after-error, recalculate, per `concept:node-run`). Normal cascade re-fires of the same node do not stamp `prior_dispatch_id` and so do not carry scratch; for that case same-node state rides the attribute carry-forward channel (per `concept:attribute`).* Replace the "Boundaries" section's Owns list with the new list: the per-dispatch work, the four-variant outcome vocabulary, the observability protocol surface, per-dispatch executor-attached opaque scratch bytes, the three orthogonal dispatch deadlines (`sync_rpc_deadline`, `max_quiet_period`, `max_runtime`), the dedicated keepalive endpoint, the persistent async-callback registry; drop the "stream-close outcome vocabulary" and "userdata interpretation" references; drop `named-event` from the adjacent-concepts list. Rewrite "Invariants" to: every dispatch returns exactly one Outcome (no stream); every settling terminal carries `attributes_delta` and `tags` uniformly; the resume-context channel does not exist (executors carry resume state via attribute carry-forward); the AwaitAsyncCallback outcome registers an `async_ack_id` and the eventual settling terminal arrives via HTTP POST to `/v1/callback/{async_ack_id}`; the async-callback registry persists across supervisor restarts; the executor-template-level `sync_rpc_deadline` (default 30s, 0 = disabled) bounds sync dispatches; per-node `max_quiet_period` and `max_runtime` (default 0 = disabled) bound async dispatches; the declared-tags list reported via observability (formerly `declared_events`) is the source of truth for tag-name template validation.

- Concept: mutate `concepts/parked-state.md` in place. Drop all references to `parked_payload`, `session_token`, `resume_reason`, and `ResumeContext`. Replace the "Resume context" subsection with: *Parked nodes carry no dedicated resume context. On re-dispatch (whether time-wake via the parked sweep or cascade-invalidate via an upstream event), the executor receives the dispatch with the standard `ExecuteRequest.attributes` populated by attribute carry-forward. Executors that need state across a park-and-resume cycle write it to `attributes_delta` on the Park terminal and read it from incoming attributes on re-dispatch. The two exit paths (time-wake when `resume_at` has passed; external invalidate via admin endpoint or in-graph subscription match) still emit `terminal/park/<reason>` signals as before, but no per-row payload or session token is threaded through to the re-dispatch.* Update "Invariants" to drop the resume-reason discriminator references AND drop the `@blessed-invariant 6`-heartbeat-exemption phrasing — heartbeats no longer exist; the orphan-reaper exemption for parked rows now rests on "parked rows are settled with respect to liveness" (no quiet-period or RPC connection state to observe). Drop the `parked_payload spills via the same mechanism` parenthetical from the Adjacent list — `parked_payload` is gone.

- Concept: mutate `concepts/signal.md` in place. Remove the `event/<name>` leaf from the taxonomy entirely — drop the dedicated `### event/<name>` subsection and any text describing it. Replace the "Signal type-path taxonomy" section's listing of top-level kinds to enumerate four: `terminal/*`, `transient/*`, `attribute/<key>/changed`, and `message/*`. Add to "Payload schemas" that `terminal/*` payloads bind a `tags: list<string>` field for CEL `in` predicates over `payload.tags`. Drop the `| named-event payload | event_payload |` row from the field-naming-convention table — `event_payload` is no longer a signal field. Rewrite the wait-set projection invariant to say: *The wait-set `topic_kind` discriminator is a faithful projection of the signal top-level kind: each of the four canonical kinds (terminal, transient, attribute, message) maps to its own `topic_kind` value; the message-kind row is admitted by the protocol-level signal taxonomy but not in this storage projection (`message` was retired from the wait-set CHECK by migration 012); `state` is admitted as a defensive fallback for unrecognized rows. With `event/<name>` retired, the `event` `topic_kind` value is dropped from the storage CHECK by the migration this spec introduces.* Update "Invariants" to reflect that `terminal/*` leaves carry tags (matched via CEL `when:` filter on `payload.tags`) rather than the prior `event/<name>` split.

- Concept: mutate `concepts/node-subscription.md` in place. Replace the subscription-grammar examples to use `subscribes: [{node: <sender>, type: terminal/*, when: "<tag>" in payload.tags}]` for the equivalent of today's `event/<name>` subscription. State that the subscription `type:` is one of `terminal/*`, `transient/*`, `attribute/*`, or `message/*` — the `event/*` form is no longer valid.

- Concept: mutate `concepts/blob-backend.md` in place. Replace the "three surfaces" enumeration with: *The blob-backend interface is the abstraction that backs spilled byte streams from one surface: attribute values.* The named-event-payload and parked-node-payload spill sites are gone (named-event ledger removed; Park.payload removed).

- Concept: mutate `concepts/auto-terminal.md` in place. The doc references neither `parked_payload` nor `session_token`, so the parked-state surface change requires no edit. But the Invariants list mentions "Active-row mutations (Promote, heartbeat-extend, the ownership-bail delete inside the unified engine) are claimant-guarded by matching the holder-supervisor id against the acting supervisor." Replace `heartbeat-extend` with `liveness-extend` to reflect the new model — active rows are extended by §12.5 writeback / keepalive bumps to `last_progress_at`, not by heartbeats. Held-claim auto-terminal behavior itself (the verb-dispatch sequence) is unchanged.

- Concept: mutate `concepts/supervisor.md` in place. Rewrite the "What it is" trailing sentence — strike *Heartbeats are queryable timestamps on the persisted node-run rows and claim-handle rows it owns* and replace with: *Per-dispatch liveness is observable via `last_progress_at` on the persisted node-run row (bumped by §12.5 writeback and keepalive POSTs) and via the supervisor's outgoing gRPC client connection state for sync dispatches.* In the Owns list, strike `heartbeating` and replace with `liveness tracking (last_progress_at maintenance via writeback and keepalive callbacks)`.

- Concept: mutate `concepts/claim-handle.md` in place. In the lock-state enumeration, rewrite the `active` state definition from *currently held by a supervisor, heartbeating* to *currently held by a supervisor; liveness is observed via the supervisor's outgoing dispatch RPC (sync) or the dispatch row's `last_progress_at` (async)*. In the Does NOT own boundary line, replace `heartbeats (those are on concept:node-run)` with `liveness tracking (those are on concept:node-run)`. In Invariants, replace `Every active-row mutation (promote, heartbeat-extend, the ownership-bail delete) matches the holding supervisor in its predicate` with `Every active-row mutation (promote, liveness-extend, the ownership-bail delete)…`.

- Concept: mutate `concepts/orphan-reaper.md` in place. Rewrite "What it is" to: *A periodic sweep that hard-deletes stale rows from the node-run ledger and the claim-handle ledger. The runtime carries a family of sweep functions — stale-recovery, orphaned-node-run, ready, and orphaned-claim-handle sweeps. Cutoff: the per-dispatch `max_runtime` deadline for orphaned-row detection (absolute upper bound), with the supervisor's outgoing gRPC client failure driving in-band cleanup for sync dispatches and the persistent `last_progress_at` quiet-period check (`now - last_progress_at > max_quiet_period`, when set) driving cleanup for async dispatches. A claimant-guarded delete predicate ensures live owners are never clobbered.* Rewrite "Purpose" to drop the "its heartbeat stops" wording — supervisor crash detection now relies on RPC-connection drop (in-band) or absent writeback / keepalive (out-of-band quiet-period sweep). Replace the heartbeat-cutoff invariant ("Sweep cutoff is `5 × heartbeat_interval` (`@blessed-invariant 6`). Same cutoff for both row types.") with: *Sweep cutoff for active rows is `max_runtime` (the per-dispatch absolute-deadline, when set); the supervisor's gRPC client failure drives in-band cleanup for sync RPCs without waiting for the sweep. For async dispatches, the quiet-period check (`now - last_progress_at > max_quiet_period`, when set) is an early-trigger before max_runtime. Claim-handle and node-run sweeps share the same cutoff.* Update the "parked rows don't heartbeat" invariant to: *`phase='parked'` rows are explicitly skipped (parked is settled with respect to liveness; no quiet-period or RPC-state to observe).*

- Concept: `concepts/transition-reason.md` — no mutation required. The doc body does not name `ReasonHandlerResume`, `resume_reason`, `deadline_elapsed`, or `external_invalidate` (those are code-level Go symbols and runtime field values, captured implicitly under TD-remove-resume-context). The reason-code semantic (parked → stale on resume) is unchanged.

- Concept: mutate `concepts/node-run.md` in place. The doc body does not enumerate individual columns by name, but it does describe the parked-state fields the row carries. Rewrite the parked-state portion to drop references to "parked payload" / "session token" / "wake reason" — parked rows carry only `parked_reason`, `parked_reason_label`, `parked_reason_note`, and `resume_at`. Drop the "last-heartbeat timestamp" from the row-fields enumeration. Drop "heartbeat fields" from the Owns list (replace with "liveness fields — `last_progress_at`, `async_ack_id`, `async_ack_registered_at`"). Drop the two heartbeat-related Invariants ("Orphan reaper covers only `phase='active'` rows; parked rows skipped explicitly (they don't heartbeat)" and "Heartbeat cutoff is `5 × heartbeat_interval` (`@blessed-invariant 6`), same as claim-handle"). Replace with: *Orphan reaper covers only `phase='active'` rows; parked rows skipped explicitly (settled with respect to liveness). For active rows the orphan signal is the supervisor's gRPC client connection state (sync dispatches) or quiet-period exceeded (`now - last_progress_at > max_quiet_period`) plus absolute-deadline exceeded (`now - dispatched_at > max_runtime`), each enforced only when the corresponding deadline is non-zero.* Update the `prior-dispatch dispositions` parenthetical "(heartbeat-stale, retry-after-error, recalculate)" to "(stale-recovery, retry-after-error, recalculate)" to match the rename. Add to the row-description: every dispatch row carries an indexed `async_ack_id` (nullable, populated when the executor returns AwaitAsyncCallback and consulted by the callback handler to correlate inbound POSTs) and a `last_progress_at` timestamp (bumped by §12.5 attribute writebacks and by keepalive POSTs). Note that the dispatch row carries a `tags` representation populated from the settling terminal (the storage form is the persistence driver's choice — array column or junction table). The actual SQL column drops (`parked_payload_inline`, `parked_payload_handle`, `parked_payload_handle_backend`, `session_token`, `wake_reason`) and adds (`async_ack_id`, `async_ack_registered_at`, `last_progress_at`, plus tags storage) ride the migration entry below. Also drop the "stream-close" wording from the scratch description in node-run.md — scratch attaches to the settling terminal Outcome (not a stream-close event).

- Concept: mutate `concepts/cascade.md` in place. Wording cleanup — drop any mention of `event/<name>` as a fire source; cascade-fire now keys on `terminal/*` signals with CEL `when:` tag filters for the discrimination that named events previously provided.

- Concept: mutate `concepts/wait-set.md` in place. No body change needed — `wait-set.md` does not enumerate topic-kind values in its body. The `topic_kind` storage-CHECK update is captured under the migration entry below (drop `event` from the allowed set); the signal-side projection invariant is captured in the `concepts/signal.md` mutation.

- Concept: mutate `concepts/attribute.md` in place. Add: *`attributes_delta` is carried on every settling terminal (Success / Error / Park) and is committed atomically with the verdict in the same persistence transaction. The §12.5 incremental writeback callback (`POST /v1/runs/{run_id}/attributes`) bumps `last_progress_at` on the dispatch row as a side effect of any write, supplying incidental liveness for async executors that report real progress.* Drop `nodes.<X>.event.<name>.<field-path>` from the substitution-grammar closed-enumeration invariant. The remaining source kinds are: `nodes.<X>.attribute.<field-path>`, `claim.<alias>.{address|scope|payload.<field-path>}`, `params.<field-path>`, `trigger.message.payload.<field-path>`, `child.partition_key`. Per-emission data that previously lived under `nodes.<X>.event.<name>.<field-path>` now lives in the emitter's `attributes_delta` and is read via `nodes.<X>.attribute.<field-path>`.

- Concept: mutate `concepts/breakpoint.md` in place. Drop any references to `event/<name>` as a matcher type-path; matchers that previously matched `event/<name>` now match `terminal/*` with a CEL `when:` filter on `payload.tags`.

- Concept: mutate `concepts/lineage-record.md` in place. Drop named-event-payload citations; lineage records cite settling terminals only post-collapse.

- Concept: mutate `concepts/rimsky.md` in place. Update the high-level executor-protocol surface description to reflect unary RPC plus async callback (no streaming, no heartbeats, no mid-stream named events).

- Concept: mutate `concepts/terminal-resolution.md` in place. Rewrite the "four decisions" framing to drop named-event-related decisions; the surface is now: settling terminal (from the unary RPC's Outcome, or from the async callback body's outcome) → cascade-fire via the `terminal/*` signal with the verdict's `tags` set as the discriminator.

- Concept: mutate `concepts/inertness.md` in place. Drop `named-event payloads` from the "Carrier streams the discipline governs" enumeration. Drop `named-event payloads` from the Structural-inertness applies-to list (the streams `attribute values`, `message payloads`, `executor error payloads` remain). Drop `concept:named-event` from the adjacent-concepts list. Rewrite the §21 invariant text to: *blob content (carried by the blob-backend interface) and executor error payloads are structurally inert* — drop the named-event-payloads reference. Drop `named-event payloads` from the substitution-leaf-path-walk sanctioned-read-site enumeration (the remaining streams traversed at substitution time are claim payload, attribute values, and message payloads). Update the "pattern-matches prohibition" sub-clause to enumerate two streams instead of three: *The 'pattern-matches' prohibition still binds for the two streams without a matcher-style sanctioned site (message payloads, executor error payloads); attribute values gained a sanctioned matcher read site via the shared matcher evaluator described below.* Update the "Scratch wire-attach + row-persist + lineage-copy" sanctioned-read-site description to drop the "stream-close" wording — scratch attaches to the settling terminal Outcome (not a stream-close event).

- Concept: mutate `concepts/event-log.md` in place. Drop the parenthetical "NOT bound by `@blessed-invariant 21` (which governs the named-event ledger)" — the named-event ledger is gone, so the contrast no longer applies. Replace with: *The `payload` is rimsky's own JSON — readable by rimsky for the dashboard and audit consumers.* Drop the "the named-event ledger (see `named-event` 'Ledger storage' subsection)" reference from the "Does NOT own" boundary. Drop `named-event (sibling append-only ledger with a different opacity discipline)` from the adjacent-concepts list.

- Concept: mutate `concepts/observability.md` in place. Drop `named-event` from the adjacent-concepts list.

- Concept: mutate `concepts/message.md` in place. Drop the "(see `concept:named-event`)" parenthetical and the "event emissions from executors" entry from the "Does NOT own" boundary — named events no longer exist as a separate emission surface. The boundary line collapses to: cross-frame coupling owned by `concept:message-emitter-node`, cascade walks owned by `concept:cascade`, etc.

- Concept: create `concepts/terminal-tag.md` from the template in `skills/_shared/artifact-definitions.md`. Definition: *A terminal tag is a string member of the `tags` set carried on a settling terminal verdict (Success / Error / Park). Tags are deduplicated at decode (set semantics), inert to rimsky, and serve as the discriminator subscribers match on via CEL `when:` filters over `payload.tags` on the `terminal/*` signal. The tag name MUST appear in the emitting executor's observability-advertised declared tags (the same validation surface that previously gated declared named-event names).* Purpose: *Provide a topology-visible, ledger-free discriminator on terminal verdicts that replaces the prior named-event ledger. Per-emission data lives in `attributes_delta` alongside the tag, so the discriminator (tag) and the data (attributes) cleanly separate concerns.* Boundaries: *Owns: the set-semantics decode rule, the declared-tags validation against the executor's observability list, the substitution access at `nodes.<emitter>.attributes.<key>` for per-emission data. Does NOT own: the tag name vocabulary (the executor's observability declaration is the registry), the subscription mechanism (`concept:node-subscription`), the cascade-fire mechanism (`concept:cascade`). Adjacent: `concept:executor`, `concept:signal`, `concept:node-subscription`, `concept:observability`, `concept:attribute`. Distinct from `concept:tag` (alias `template-tag`), which is a movable string alias for a `template_hash`; the two nouns share the word but have no overlap in meaning, scope, or carrier.* Invariants: *Tags are inert; rimsky reads them only at cascade-walk CEL evaluation and at terminal persistence. The tag name appears in the executor's observability-declared tag set at registration; emissions of undeclared names are rejected at the supervisor's terminal handler.*

- Concept: retire `concepts/named-event.md`. Move to `concepts/_retired/named-event.md` with a one-line note in the file body: *Retired in spec `2026-06-16-executor-protocol-coherence-design.md`. The capability folds into the settling-terminal's `tags` set field on the verdict, plus per-emission data on `attributes_delta` (see `concept:terminal-tag`).* Update `concepts.md` (the TOC) to list this under "Retired concepts" with a one-line description and add `terminal-tag` to the live-concepts list.

- Decision: mutate `decisions/claude-agent-session-attribute.md` in place. Replace the dual-path framing with single-path. New Choice section: *claude-agent's expected-attributes schema carries a session-token property: a string-typed, executor-written carry-forward attribute, empty by default, describing the agent CLI session token. On dispatch the executor reads `req.attributes.session_token`; if non-empty, launches the CLI with `--resume <token>`. On every settling terminal (Success in the loop case, Park in the rate-limit case), the executor writes the current dispatch's run id to `attributes_delta.session_token`. The next dispatch in the same RunScope receives that value via carry-forward and resumes the prior CLI conversation. A sub-graph invocation arrives with no session-token attribute, yielding a fresh CLI conversation.* Update Rationale to drop the "park resume-context stays in place" framing.

- Decision: mutate `decisions/async-callback-outcome-oneof.md` in place. Add: *The outcomes inherit the uniform settling-terminal shape — each carries `attributes_delta` and `tags` alongside the type-specific fields. The `AsyncCallbackBody.events` field is removed; per-emission events fold into the outcome's `tags` set.*

- Decision: mutate `decisions/async-callback-post-json.md` in place. Wording cleanup — the body shape evolves with the post-collapse terminal shape (no `events` field, uniform `attributes_delta` and `tags` on outcomes).

- Decision: mutate `decisions/scratch-protocol.md` in place. Replace "stream-close" / "stream-close attach" wording with "terminal outcome" — scratch is attached to the unary RPC's Outcome (or the async callback body's outcome), not to a stream-close event.

- Decision: mutate `decisions/scratch-column.md` in place. Wording cleanup paralleling `decisions/scratch-protocol.md` — no semantic change.

- Decision: mutate `decisions/scratch-recovery.md` in place. Update the recovery-disposition references to the renamed `PRIOR_STALE_RECOVERY` enum value (and the storage-form `stale_recovery`). Replace the "stale-heartbeat sweep" wording in the four-call-sites enumeration with "stale-recovery sweep" — the supervisor no longer detects stale via heartbeat-loss; the sweep keys on `last_progress_at` quiet-period or RPC connection state instead.

- Decision: mutate `decisions/loop-counter-shape.md` in place. Replace the "Two declared named events: a step-iteration event and a done event" line with: *Two declared tags: a step-iteration tag and a done tag.* Replace the dispatch behavior description's "emit the step-iteration event if the new count is below the maximum, otherwise emit the done event" with: *include the step-iteration tag on the Success outcome if the new count is below the maximum, otherwise include the done tag.* Update Rationale wording so "Both events are observable" becomes "Both tags are observable from downstream subscriptions via `terminal/success` with a CEL `when:` filter on `payload.tags`."

- Decision: create `decisions/executor-unary-rpc.md` from the template, capturing TD-execute-rpc-unary.

- Decision: create `decisions/terminal-tags.md` from the template, capturing TD-collapse-named-event-to-tags.

- Decision: create `decisions/uniform-attributes-delta.md` from the template, capturing TD-attributes-delta-on-all-settling-terminals.

- Decision: create `decisions/no-resume-context.md` from the template, capturing TD-remove-resume-context.

- Decision: create `decisions/async-callback-persistent-registry.md` from the template, capturing TD-persist-async-callback-registry.

- Decision: create `decisions/three-dispatch-deadlines.md` from the template, capturing TD-three-dispatch-deadlines.

- Decision: create `decisions/keepalive-endpoint.md` from the template, capturing TD-keepalive-endpoint.

- Decision: create `decisions/writeback-bumps-progress.md` from the template, capturing TD-writeback-bumps-progress.

- Decision: create `decisions/tag-based-subscription.md` from the template, capturing TD-subscription-grammar-shift.

- Decision: create `decisions/no-event-substitution.md` from the template, capturing TD-remove-event-substitution-path.

- Decision: create `decisions/orphan-reaper-connection-state.md` from the template, capturing TD-orphan-reaper-no-heartbeat.

- Decision: create `decisions/claude-agent-attribute-only-session.md` from the template, capturing TD-claude-agent-session-attribute-only.

- Decision: create `decisions/prior-stale-recovery-rename.md` from the template, capturing TD-prior-stale-rename.

- Proto: rename the observability-protocol field `ObservabilityCapabilities.declared_events` to `declared_tags` (file: `lib/protocols/proto/v1/executor_observability.proto`). Same field number and same wire purpose; the rename brings the capability name into agreement with the new terminology. Per the pre-v1 break-freely rule, no compat shim is needed.

- Persistence migrations: write a new migration in `lib/foundation/persistence/postgres/migrations/` and the parallel `lib/foundation/persistence/sqlite/migrations/` directory (appended at the next sequential number per the existing append-only convention; the migration runner does not require backwards-compatibility shims per project rules pre-v1). The migration: (a) drops the `rimsky_node_events` table; (b) drops the columns `parked_payload_inline`, `parked_payload_handle`, `parked_payload_handle_backend`, `session_token`, `wake_reason` from `rimsky_node_runs`; (c) adds `async_ack_id text NULL` (indexed), `async_ack_registered_at timestamptz NULL`, `last_progress_at timestamptz NULL`, plus the chosen tags storage (the implementation picks between `tags text[]` and a junction table `rimsky_node_run_tags`); (d) rewrites the `rimsky_node_runs.prior_dispatch_disposition` CHECK constraint to accept `('stale_recovery', 'retry_after_error', 'recalculate')` in place of `('heartbeat_stale', 'retry_after_error', 'recalculate')`; (e) rewrites the wait-set `topic_kind` CHECK constraint to remove `'event'` from the allowed set, leaving `('state', 'attribute', 'transient', 'terminal')`. The migration drops + recreates rather than threading compat shims, per `.claude/rules/rules.md`'s pre-v1 directive.

- Story: mutate `stories/executor-protocol.md` in place. Replace the Capability section with: *Public executor protocol surface — a unary `Execute(req) → Outcome` verb plus the observability handshake (see `concept:executor`) — that any service author implements; rimsky drives discovery, schema validation, dispatch, terminal resolution, and error-class-aware routing against it.* Replace the Acceptance section with: *A custom executor implementing the public protocol, registered with rimsky's executor catalog, can be referenced from a template; on instance dispatch, the executor receives a real unary request with resolved attributes, returns a settling terminal directly (Success / Error / Park with `attributes_delta` and `tags`) or defers via AwaitAsyncCallback and POSTs the eventual verdict to the callback URL; errors with advertised classes route per the template's error-policy; tags on the settling terminal are visible to downstream subscribers via the `terminal/*` signal with a CEL `when:` filter on `payload.tags`.* Replace the Falsifier section with: *A registered executor advertising a declared error class emits it but the policy router treats it as generic, OR the unary RPC's response shape is rejected by the supervisor, OR a tag emitted on a settling terminal does not appear in downstream subscription matches, OR an async-callback POST is dropped after the supervisor that registered it restarts.* The Proof section is unchanged in form (Example) — an updated worked walkthrough.

- Story: mutate `stories/loop-counter-cap.md` in place. Replace the Role and capability section with: *As a template author, I can use the bundled loop-counter utility node kind with a maximum-count input attribute, and observe it emit a `loop` tag on each dispatch while count is below max and a `done` tag when count reaches max, so I can express bounded iteration without authoring a custom executor.* Replace the Acceptance section with: *I declare a node of the loop-counter kind with a maximum count of three; cascade re-fires the node three times via a subscription on the `loop` tag; on the third dispatch the node emits the `done` tag instead of the `loop` tag; a downstream subscriber on the `done` tag fires.* Replace the Falsifier section with: *The loop-counter carries the `loop` tag after reaching the maximum count. OR: it carries the `done` tag before reaching the maximum count. OR: count does not carry across dispatches and the `done` tag never fires.* Replace the Proof section with: *Demo — scenario test wiring a loop-counter node (maximum count of three) to a sink subscriber via `subscribes: [{node: <emitter>, type: terminal/success, when: "loop" in payload.tags}]` and a different sink subscriber on `"done" in payload.tags`; observes the `loop` tag fires three times then the `done` tag fires once.*

- Story: mutate `stories/cascade-signal-blind.md` in place. Replace the Role section's enumeration of cascade-firing signal types — drop `event/<name>` — to: *`terminal/success`, `terminal/error/<class>`, `transient/retry/<n>/<class>`, `attribute/<key>/changed`*. Replace the Capability section's same enumeration the same way. Replace the Acceptance section's "For each cascade-firing signal type in the canonical taxonomy" sweep so the iterated set is the post-collapse taxonomy (terminal kinds, transient kinds, attribute changes; no `event/<name>` leaf). Add to Acceptance: *Subscriptions on `terminal/*` with a CEL `when:` filter over `payload.tags` (e.g., `"data_ready" in payload.tags`) fire when a sender's settling outcome carries the named tag.* Falsifier's "any single cascade-firing signal type" sweep stays structurally the same but the enumerated set drops `event/<name>`. Proof unchanged in form (table-driven executable proof) but the table's row for `event/<name>` is replaced with one for `terminal/<kind>` with a CEL tag-filter assertion.

- Story: mutate `stories/opaque-executor-scratch.md` in place. Replace the Acceptance section with: *I write an executor that writes scratch — either mid-dispatch via the executor-protocol scratch callback (mirroring the attributes incremental-writeback pattern) or by attaching scratch bytes to its terminal outcome (the unary RPC's Outcome, or the async-callback body's outcome); rimsky persists the scratch on the dispatch row that received it; when a successor dispatch row is created under one of the three recovery dispositions that link to the prior dispatch — stale-recovery, retry-after-error, or recalculate — the enqueue path copies the prior dispatch's scratch onto the new row, and the successor's incoming request carries the original scratch bytes verbatim. Normal cascade re-fires go through the cascade walker's stale-mark path, which does not stamp a prior-dispatch reference and so does not carry scratch; same-node state across normal cascade re-fires rides the attribute carry-forward channel, not scratch.* (Note: `heartbeat-stale recovery` becomes `stale-recovery` to match `PRIOR_STALE_RECOVERY`.) Falsifier and Proof unchanged in form; the proof artifact (executable round-trip test) is updated to use the unary / callback verdict path and the renamed disposition.

## Proof changes

- **STORY-executor-protocol** — B. Shift the intent. The story's Acceptance, Falsifier, and Capability all rest on the streaming-execute-stream contract ("server-streaming execute verb," "the executor receives a real execute stream," "can emit heartbeats and named events"). The spec removes the streaming verb and the heartbeats / named-events surface entirely. The Acceptance / Falsifier / Capability rewrites land under `## Design changes` above. The proof artifact (the shipped executor reference paired with the worked walkthrough) will be substantively refactored to exercise unary execute, async-callback registration plus delivery, error-class routing, and tag-based subscription.

- **STORY-loop-counter-cap** — B. Shift the intent. The Acceptance, Falsifier, and Proof all use named-event language ("step-iteration named event," "done named event," "subscription on its step-iteration event"). The spec collapses named events into tags. The Acceptance / Falsifier / Proof rewrites land under `## Design changes` above. The proof artifact (scenario test) will be updated to use the tag-based subscription grammar and assert against terminal tag presence.

- **STORY-opaque-executor-scratch** — B. Shift the intent (light). The Acceptance phrasing references "stream-close outcome" and "heartbeat-stale recovery" which both go away. The Acceptance rewrite lands under `## Design changes` above. Falsifier and Proof are structurally unchanged but the proof artifact is updated to exercise the unary / callback terminal path and the renamed `PRIOR_STALE_RECOVERY` disposition.

- **STORY-cascade-signal-blind** — B. Shift the intent. The story's Role, Capability, Acceptance, Falsifier, and Proof all explicitly enumerate `event/<name>` as one of the cascade-firing signal types the cascade engine is "blind" to. With `event/<name>` removed from the taxonomy (TD-collapse-named-event-to-tags + TD-subscription-grammar-shift), the enumeration changes shape. The Role / Capability / Acceptance / Falsifier rewrites land under `## Design changes` above; the Proof's table-driven shape is preserved, but the iterated set is the post-collapse taxonomy and the `event/<name>` row becomes a `terminal/<kind>` + CEL tag-filter row.

## Manifest

### Technical decisions
- **TD-execute-rpc-unary** — Execute becomes unary
- **TD-collapse-named-event-to-tags** — NamedEvent collapses into terminal tags
- **TD-attributes-delta-on-all-settling-terminals** — Success / Error / Park uniformly carry attributes_delta
- **TD-remove-resume-context** — Resume context channel removed
- **TD-persist-async-callback-registry** — AwaitAsyncCallback registry persists across supervisor restart
- **TD-three-dispatch-deadlines** — Three orthogonal deadlines, all 0 = disabled
- **TD-keepalive-endpoint** — Dedicated keepalive endpoint
- **TD-writeback-bumps-progress** — §12.5 writeback bumps last_progress_at
- **TD-subscription-grammar-shift** — Subscriptions move to terminal/* + CEL tag filter
- **TD-remove-event-substitution-path** — event/<name> substitution path removed
- **TD-orphan-reaper-no-heartbeat** — Orphan reaper keys on connection state / quiet-period
- **TD-claude-agent-session-attribute-only** — claude-agent session token rides attributes only
- **TD-prior-stale-rename** — PRIOR_HEARTBEAT_STALE renames to PRIOR_STALE_RECOVERY

### Stories (mutated; spec is story-less)
- **STORY-executor-protocol** — mutated (B. Shift the intent)
- **STORY-loop-counter-cap** — mutated (B. Shift the intent)
- **STORY-opaque-executor-scratch** — mutated (B. Shift the intent, light)
- **STORY-cascade-signal-blind** — mutated (B. Shift the intent)

### Design changes
- Concept mutations: executor, parked-state, signal, node-subscription, blob-backend, orphan-reaper, node-run, cascade, attribute, breakpoint, lineage-record, rimsky, terminal-resolution, inertness, event-log, observability, message, supervisor, claim-handle, auto-terminal
- Concept creation: terminal-tag
- Concept retirement: named-event (moved to `_retired/`)
- Decision mutations: claude-agent-session-attribute, async-callback-outcome-oneof, async-callback-post-json, scratch-protocol, scratch-column, scratch-recovery, loop-counter-shape
- Decision creations: executor-unary-rpc, terminal-tags, uniform-attributes-delta, no-resume-context, async-callback-persistent-registry, three-dispatch-deadlines, keepalive-endpoint, writeback-bumps-progress, tag-based-subscription, no-event-substitution, orphan-reaper-connection-state, claude-agent-attribute-only-session, prior-stale-recovery-rename
- Proto rename: `ObservabilityCapabilities.declared_events` → `declared_tags`
- Persistence migrations: drop `rimsky_node_events` table, drop parked-payload / session_token / wake_reason columns from `rimsky_node_runs`, add `async_ack_id` / `async_ack_registered_at` / `last_progress_at` / tags storage, rewrite `prior_dispatch_disposition` CHECK (`heartbeat_stale` → `stale_recovery`), rewrite wait-set `topic_kind` CHECK (drop `event`)
