# Frame Resolution for Reactive Node Graphs

**Status:** design proposal. Hand-off doc for a follow-up session that will expand this into a formal spec.

**Authors:** Patrick + the orchestrator agent during the stores-redesign brainstorm/execute cycle (April 2026).

**Genesis:** The stores-redesign smoke fixture (`test/smoke/stores_redesign_smoke_test.go`, spec §19.2) failed to satisfy its acceptance predicate. Investigation surfaced a deeper architectural gap: rimsky claims to be a reactive node-graph orchestrator but has no mechanism for **frame resolution** — the implicit guarantee that a reactive graph fully resolves between invalidations. Without that guarantee, results are unspecified under invalidation pressure.

This doc captures the problem, the mental model, the design space, and the open questions, so a fresh session can pick it up and write the spec without re-deriving the analysis.

---

## 1. The problem

### 1.1 Symptom: smoke test under-counts terminal commits

The stores-redesign smoke fixture seeds 100 items in a claim store, then issues 100 force-fires on the source node. The §19.2 acceptance predicate asserts ≥100 terminal-node commits (one per item).

Actual result: ~3 terminal commits per 100 fires. The other 97 items get claimed, partially processed, then preempted by the next fire. They eventually get released by orphan-reap and visibility-timeout sweeps, but they never reach the terminal node with their original payload.

The failure is not a bug in any individual store/lock/attribute mechanism. Each store's contract holds; locks release cleanly; no claim leaks. The failure is **architectural**: the cascade engine has no notion of a "frame" of resolution that must complete before the next invalidation lands.

### 1.2 The implicit assumption in reactive node graphs

Reactive node-graph systems — game engines, build systems, spreadsheets, modern UI frameworks — operate under an implicit invariant:

> **The graph resolves to a consistent state between invalidations.**

In a game engine, the scene graph is fully evaluated each render frame; user input invalidates the graph but only takes effect on the next frame. In a spreadsheet, a cell edit triggers a recalculation cycle that completes before the next edit can land. In React/Solid/Svelte, signal propagation is batched into a microtask boundary so multiple state changes coalesce into one re-render.

Without this invariant, downstream nodes can be reading from an inconsistent mix of upstream states — some fresh, some mid-recomputation, some pre-empted. In a synchronous engine you can't observe the inconsistency because resolution is instantaneous (or appears so). In an **asynchronous** engine like rimsky, where each node-execution is a unit of supervisor-scheduled work that takes seconds-to-minutes, the inconsistency is real and observable.

### 1.3 Why rimsky doesn't satisfy the invariant

Today's rimsky cascade is approximately **Rule 2** (see §3) — abort on new invalidation — but implemented badly. Specifically:

1. Invalidates emit messages immediately. There is no "block until resolved" gate.
2. `kill_requested=true` flags an in-flight executor for cancellation, but the runner just bails — there's no "frame restart" semantic. The killed work's partial state (sidecar writes, attribute deltas, claim acquisition) gets unwound through orphan-reap, not as part of a frame protocol.
3. There's no notion of "frame N is complete; frame N+1 may now begin." Multiple concurrent invalidates race against each other and the cascade is whoever wins.
4. Per-node `rimsky_node_attributes.data` is a single mutable cell. Two frames cannot both have their own snapshot of upstream state.
5. Held-claim resolution (`rimsky_claim_holders`) keys on `(claim_id, holder_node_id)` with no frame discriminator. There's no way to distinguish "this terminal-leaf finished frame N" from "this terminal-leaf finished frame N+1."

Under sustained invalidation pressure, the cascade live-locks: each new invalidate kills the in-flight cascade, which never gets a chance to settle. Under sparse pressure, the cascade resolves correctly. The smoke fixture is in the sustained-pressure regime; most rimsky use cases to date have been in the sparse regime, which is why this hasn't surfaced before.

---

## 2. Mental model

### 2.1 Frames as the missing primitive

A **frame** is a complete pass of the cascade engine over the node graph: starting from an invalidation event (or batch of invalidation events), proceeding through topological order, ending when every transitively-affected node has either resolved (`fresh`) or terminated (`failed`).

Key properties:
- A frame has a **start event** (one or more invalidations) and an **end event** (graph-resolved-or-failed).
- During a frame, downstream nodes read **upstream state at the time the frame started**, not the live state.
- Between frames, the graph is in a "consistent" state — every node is `fresh` (or `failed`), no node is `running`, no node has pending invalidate messages from this frame.
- A frame is the unit at which side effects (claim resolutions, store commits, terminal-leaf events) commit.

### 2.2 Why the existing vocabulary doesn't capture this

Rimsky's existing concepts — node states (fresh/stale/running/failed), invalidate/recalculate messages, dispatch claims, lock holders — are all **per-node** or **per-edge**. There's no concept that spans the cascade.

The closest existing concept is "scheduler tick," but a tick is a wall-clock event ("once per second, look at the dispatch queue"). It's not bounded by graph resolution; it doesn't know when a cascade is "done."

A frame is a graph-spanning unit. It needs its own primitives.

---

## 3. The four resolution rules

We considered four rules during brainstorming, framed as different policies for handling new invalidation events while a frame is in progress:

### Rule 1 — Coalesce (signal-not-event)

**Policy:** Invalidations during an in-flight frame are noted as "graph needs another frame after this one finishes." Multiple invalidates within one frame collapse to a single trailing frame.

**Mechanics:**
- Per-graph (or per-instance) flag: "pending invalidation."
- New invalidate during running frame → set the flag. Don't propagate to nodes yet.
- Frame end → if flag is set, clear it and start a new frame from whichever sources had been invalidated.

**Use case fit:** Data-freshness reactivity. "This resource changed; the graph is stale; recompute." If 100 changes happen during one frame, you just need one more recompute, not 100.

**Smoke fixture under Rule 1:** Still fails (~1–2 review commits per 100 fires). This rule is wrong for event-driven workloads.

**Schema impact:** Minimal. Add a `pending_invalidation_at` (or similar) column to `rimsky_instances` (or whatever scopes a graph). The cascade engine checks this column at frame end.

**Complexity:** Low.

### Rule 2 — Abort (preemptive restart)

**Policy:** New invalidation cancels the in-flight frame and starts a fresh one from the new invalidate's origin.

**Mechanics:**
- New invalidate during running frame → set kill flag on every running node downstream of the invalidate.
- Killed nodes bail; partial work is rolled back (sidecar/discard, lock release, claim release).
- Cascade restarts from the invalidate's source.

**Use case fit:** Hard-real-time priority preemption. "This is more important than what was running."

**Smoke fixture under Rule 2:** Fails harder than today (livelock under sustained pressure).

**Schema impact:** Minimal — kill_requested already exists. Need explicit "frame in progress / aborted" tracking and clean rollback semantics.

**Complexity:** Medium. The rollback discipline is non-trivial.

**Status:** This is approximately what rimsky implements today, badly. Probably wrong as the only rule for any workload.

### Rule 3a — Serial queue (event-not-signal)

**Policy:** Each invalidation is a distinct event that produces its own frame. Frames execute serially; frame N+1 starts only after frame N completes.

**Mechanics:**
- Frame queue. Producer emits an invalidate → queue entry.
- Cascade engine dequeues one frame, runs it to completion, then dequeues the next.
- Each frame carries its own input data (e.g. for claim-driven workloads, the specific claim's payload).

**Use case fit:** Event-driven workloads where each invalidate represents a discrete piece of work. Queue workers, message-driven pipelines, the smoke fixture.

**Smoke fixture under Rule 3a:** Passes. 100 fires → 100 frames → 100 review commits, processed serially.

**Schema impact:** New table or column for the frame queue. `frame_id` on `rimsky_dispatch` (so the supervisor knows which frame a dispatch belongs to). No per-frame attribute snapshot needed (frames don't overlap).

**Complexity:** Medium. Requires queue management discipline.

### Rule 3b — Parallel with per-frame buffering

**Policy:** Each invalidation produces a frame. Multiple frames may be in flight concurrently. Topological ordering is enforced per-frame; downstream-of-X reads X's same-frame attributes.

**Mechanics:**
- Frame queue (as in 3a) but frames don't have to wait for predecessors to finish before starting.
- Per-node, per-frame attribute snapshot. Upstream message passing is cached per-frame and applied to the corresponding frame's attributes.
- A node's frame N+1 dispatch is gated on its upstream nodes having either resolved frame N+1 or having a determinable invariant that frame N+1 won't propagate from them.

**Use case fit:** High-throughput event-driven workloads where serial 3a is too slow.

**Smoke fixture under Rule 3b:** Passes with throughput. Reviews can commit in parallel.

**Schema impact:** Significant. `frame_id` on `rimsky_node_attributes`, `rimsky_claim_holders`, `rimsky_dispatch`, `rimsky_lock_holders`. Topological-gating logic in dispatch eligibility. Per-frame attribute history. Held-claim resolution keyed on frame_id.

**Complexity:** High. The hardest of the four to get right.

---

## 4. Cross-cutting concerns

### 4.1 Upstream message passing must be cached per frame

The user's framing: "upstream message passing must also be cached and applied after current-frame resolution."

This is the **per-frame attribute snapshot** under Rules 3a/3b — when frame N reads `{{deps.upstream.field}}`, it reads upstream's attribute value **as of frame N**, not as of "now." Under 3a, frames don't overlap so this is automatic (the live attribute IS the frame's attribute). Under 3b it requires real per-frame storage.

Under Rule 1, this also matters: invalidates that arrive mid-frame must NOT cause downstream nodes to be re-read with mid-frame upstream state. The signal must wait for frame end.

Under Rule 2, abort semantics must include rolling back partial attribute writes from the killed frame so the next frame starts clean.

### 4.2 Held claims and frames

Held claims (spec §5.6.3) are anchored at the source and resolved at terminal-leaves. They span the cascade.

- Under Rule 1: a held claim is per-frame; one frame per coalesced trailing frame. Resolved at terminal-leaf commit of THAT frame.
- Under Rule 2: a held claim from an aborted frame must be released as part of abort. New frame starts with no leftover holders.
- Under Rule 3a: one held claim per frame. `rimsky_claim_holders` doesn't need a frame_id column because frames don't overlap; the existing `(claim_id, holder_node_id)` key is sufficient.
- Under Rule 3b: held claims need `frame_id` to disambiguate which frame's claim is being resolved. Schema change required.

### 4.3 Operator-originated invalidates

Operator-initiated invalidates (`POST /nodes/:id/invalidate`) are a different kind of event from regular cascade invalidates. They represent "human said: re-run this node." Under all four rules, they need a clear semantic:

- Under Rule 1: operator invalidate joins the coalesce pool for the next trailing frame.
- Under Rule 2: operator invalidate aborts the current frame.
- Under Rule 3a / 3b: operator invalidate enqueues a new frame.

Operator invalidates may also want priority: jump the queue, abort the in-flight frame, etc. Worth flagging as a config option, not a hard rule.

### 4.4 Schedule-driven invalidates

Today, scheduled nodes (cron) are how `claim-topic` re-fires. Under the new model:

- Rule 1: scheduled fire = signal. Coalesces if many fire during a frame.
- Rule 2: scheduled fire = abort.
- Rule 3a / 3b: scheduled fire = new frame.

Smoke fixture's force-fire mechanism is essentially "schedule a new frame, now." Under 3a, 100 force-fires queue 100 frames cleanly.

### 4.5 The cron coalescing question (spec §14)

Spec §14 already documents that scheduled cron firing doesn't backfill missed slots — a long outage produces a single trailing fire on recovery. This is a Rule-1-like coalescing built into the schedule ticker. Under a unified frame model, this becomes a configurable property of the frame queue: "scheduled invalidates coalesce if more arrive while one is queued."

---

## 5. Recommendations

### 5.1 Pick a default and make it configurable

Rimsky probably needs to support **at least Rule 1 and Rule 3a**, because they correspond to fundamentally different workload classes:

- **Rule 1 workloads:** "the graph models a system; recompute it when state changes." Rate of state-change >> rate of meaningful recomputation. Coalescing is the right call.
- **Rule 3a workloads:** "the graph processes events; each event is a discrete piece of work." Rate of events ≈ rate of work units. Queueing is the right call.

The smoke fixture is a Rule 3a workload. The original design doc's "data-freshness pipeline" concept is a Rule 1 workload. Same engine should handle both.

**Configuration shape:** per-template (the cascade behavior is graph-wide). Add a top-level `frame_resolution: coalesce | serial_queue | parallel_buffered` to template specs. Default `serial_queue` (most mechanically deterministic; matches the queue-driven workloads that motivate rimsky's design).

Rule 2 is probably wrong as a primary mode. It's useful as a flag on individual frames ("operator says: abort current and run THIS now") but not as the default cascade behavior.

Rule 3b is post-v1 — significant schema change, complex eligibility logic.

### 5.2 Schema additions (sketch)

Even pre-v1, this redesign justifies dev-DB-nuking. Likely additions:

- `rimsky_frames` table. Per-instance, per-frame-id, with state (queued/running/completed/aborted). The cascade engine's source of truth for frame ordering.
- `rimsky_dispatch.frame_id` — which frame this dispatch belongs to.
- `rimsky_node_attributes`: under Rule 1/3a, no schema change. Under Rule 3b, add `frame_id` to the primary key.
- `rimsky_claim_holders`: under Rule 3a, no change. Under Rule 3b, add `frame_id`.
- `rimsky_instances` or `rimsky_templates`: add `frame_resolution` column or per-instance config row.

### 5.3 Smoke fixture under the new model

Smoke fixture under Rule 3a (default): 100 force-fires → 100 frames → 100 terminal-node commits. Acceptance predicate passes naturally. No change to the §19.2 assertions needed.

Per-instance configuration in the smoke fixture: `frame_resolution: serial_queue` (explicit, even if default).

### 5.4 Migration path from current "Rule 2 badly"

Pre-v1 → break freely. Migration is "rewrite the cascade engine; nuke the dev DB; ship the new model." No operator-side migration needed since there are no production deployments.

The work decomposes:
1. Cascade engine refactor: introduce frame_id; serialize cascade events by frame; make the dispatcher frame-aware.
2. Schema additions per §5.2.
3. Per-template `frame_resolution` config in the YAML grammar.
4. Implement Rule 1 and Rule 3a. Defer Rule 3b. Drop Rule 2 (or make it a per-invalidate flag, not a default).
5. Update the smoke fixture's assertions if needed (under Rule 3a it should pass without changes; under Rule 1 the assertion would change to count claim resolutions, not terminal commits).
6. Documentation: rewrite spec §11.4 / §13 to express the frame-resolution semantics.

---

## 6. Open questions for the spec session

1. **Default rule.** 3a (most mechanically deterministic), 1 (matches scheduler's existing coalescing), or per-template-required (no default; explicit declaration mandatory)?
2. **Frame queue depth.** Under 3a, can the queue grow without bound? What's the back-pressure mechanism for producers?
3. **Frame timeout.** What if a frame's cascade gets stuck? Today's heartbeat-loss + orphan-reap handles per-node deadlocks. Per-frame deadlock needs a frame-level timeout.
4. **Frame aborts.** Even under 3a, sometimes you want "kill the current frame; advance to the next." Is this a separate API? An action on the current frame?
5. **Operator-invalidate priority.** Should operator invalidates jump the queue under 3a? Or always go through the queue?
6. **Held claims under Rule 3b.** Per-frame `claim_holders`. How does the §5.6.4 algorithm change? Does fan-out have per-frame leaves or per-claim leaves?
7. **Per-store frame semantics.** Do all stores see frames? Or is this a graph-engine concept that doesn't reach into store implementations? (Probably the latter — stores don't know about frames; they see one acquire/release per frame's dispatch.)
8. **Async handoff (§12.4) and frames.** An executor returns AsyncAccepted in frame N. Frame N+1 starts. The async callback arrives. It belongs to frame N. Does the engine distinguish? (Yes, via dispatch_id which is implicitly frame-specific under §13.3.)
9. **Quality-rule failures and frames.** A frame whose terminal-leaf commit fails quality validation: does the frame fail? Does the next frame proceed regardless? (Probably the former — frame is the unit of "did this complete or not.")
10. **Migration testing.** What scenarios need to be added to `test/scenarios/` to exercise frame semantics? At minimum: a per-rule scenario showing each rule's expected behavior under sustained invalidation pressure.

---

## 7. Glossary

- **Frame** — a complete pass of the cascade engine over a graph. Starts with one or more invalidations, ends when all affected nodes have resolved or failed. The unit of "the graph is in a consistent state."
- **Frame queue** — under Rules 3a/3b, the ordered list of frames waiting to execute or in-flight.
- **Coalesce** — Rule 1 behavior: multiple invalidations collapse to a single trailing frame.
- **Abort** — Rule 2 behavior: new invalidation cancels the in-flight frame.
- **Serial queue** — Rule 3a behavior: each invalidation produces a frame; frames execute one at a time.
- **Parallel buffered** — Rule 3b behavior: frames can overlap; per-frame attribute snapshots; topological gating per frame.
- **Per-frame snapshot** — under Rule 3b, each frame has its own copy of upstream attributes; downstream reads pin to the same-frame upstream.
- **Frame-resolution invariant** — "the graph resolves to a consistent state between invalidations." The implicit contract every reactive node-graph system depends on.

---

## 8. References

- **Stores redesign spec:** `docs/specs/2026-04-25-stores-redesign-design.md` (the design that surfaced this gap).
- **Smoke fixture:** `test/smoke/stores_redesign_smoke_test.go` (the acceptance test that revealed the gap).
- **Conversation thread:** the brainstorm + execute chat that produced this analysis. Specifically: spec §19.2 design discussion, smoke run results showing 3 review commits per 100 fires, the rule-1/2/3a/3b enumeration.
- **Game-engine analog:** scene-graph render frames; React Concurrent Mode's batched renders; Solid/Svelte's signal-propagation cycles; spreadsheet recalc cycles. Different domains, same invariant.
- **Existing coalescing in rimsky:** `core/scheduler/schedule_ticker.go`'s missed-fire policy (no backfill on outage recovery) is a Rule-1-like coalescing already in place for scheduled nodes. The unified frame model would generalize this.

---

## 9. Notes for the next session

The next session should:

1. **Read this doc end-to-end before writing the spec.** The §3 rules and §4 cross-cutting concerns are the design space; the spec needs to commit to a specific shape.
2. **Walk the spec author through the §6 open questions.** Especially: default rule, configuration shape, schema additions, abort semantics under 3a.
3. **Decide post-v1 scope explicitly.** Rule 3b is probably out for v1. Rule 2 may be out as a default but in as a per-invalidate flag.
4. **Cross-reference with the stores-redesign spec.** Held-claim resolution, dispatch atomicity, attributes substitution all interact with frame semantics. The frame-resolution spec needs to express how it modifies / overrides those mechanisms.
5. **Consider whether to ship together or sequence.** The stores redesign is in the working tree but uncommitted; merging without frame resolution leaves the smoke gap open. Options: (a) ship stores redesign now with smoke-fixture predicate adjusted to count claim resolutions instead of terminal commits, then ship frame resolution as a follow-up; (b) hold stores redesign until frame resolution lands; (c) merge frame resolution into the stores redesign as a single bigger ship.
6. **Don't conflate this with the holding-subgraph DAG walk (§11.4).** That algorithm is per-frame correct; the gap is at the cascade-engine level, not at the DAG-walk level.

End of doc.
