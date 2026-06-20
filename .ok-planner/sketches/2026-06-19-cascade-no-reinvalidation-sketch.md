# Cascade no-re-invalidation — Design Sketch

**Date:** 2026-06-19
**Status:** Sketch (not a spec; not authorization to build)

## Idea

A node-run, once dispatched, is sealed: nothing can re-invalidate it, mutate its state, or rewrite its substituted attribute bag until it settles to a terminal state on its own (success / error / park-then-wake-then-settle / held-then-auto-terminal). Cascade events that target an already-dispatched node-run do not interrupt — they queue a new node-run that the system dispatches in order once the current run finishes. This makes the in-flight node-run's view of the world fixed for its lifetime, which is the natural reading of "this is a single dispatch" and the load-bearing property the parking executor pattern silently relies on.

The model also unifies two columns that today track overlapping concerns (`state` and `phase`) into a single state machine with seven values, makes `held` a state (not just a phase) so the cascade can defer until auto-terminal commit/abandon, and introduces `terminal/error/abandoned` as a real subscribable signal so downstream nodes can react when held work gets rolled back.

This supersedes and corrects the partial work already shipped on this branch (NodeStateResuming + ReasonDeadlineResume + queued-vs-resuming framing in `code:lib/foundation/cascade/state.go` and `code:lib/runtime/wake_parked.go`). That work was a narrower fix scoped to deadline-driven parked-resume only; the design here generalizes the invariant to all in-flight states and all cascade paths, and the partial fix needs to collapse into the larger one.

## Shape

### Core invariant

> A node-run that is no longer in a strictly-pre-dispatch state — running, held, parked, OR pending (waiting on upstreams) — is never re-invalidated, never has its state mutated, never has its substituted attribute bag rewritten by anything other than its own executor's writeback. Cascade events targeting such a node-run cause a NEW node-run to be created for the same (node, run-scope); the new run waits in line.

### Seven-state state machine

| State | Meaning | Has bag? | Dispatch-eligible? |
|---|---|---|---|
| `pending` | created, waiting for upstream cascades to settle (wait-set draining) | no | no |
| `stale` | gates cleared, bag built and persisted, ready to dispatch | yes (frozen) | yes (subject to self-gate) |
| `running` | claimed by dispatcher, executor in flight | yes (frozen) | no (running) |
| `held` | executor returned with held=true claim; cascade paused awaiting auto-terminal commit/abandon | yes (frozen) | no (in-flight via held) |
| `parked` | executor returned park terminal | yes (frozen) | no (in-flight via park) |
| `fresh` | settled successfully (terminal/success or held + auto_commit) — TERMINAL, no outgoing transitions | yes (final) | no (settled) |
| `failed` | settled with terminal/error or held + auto_abandon — TERMINAL, no outgoing transitions | yes (final) | no (settled) |

#### Transitions

```
pending → stale      (gates cleared: wait-set fully drained + no in-flight upstream + no in-flight self)
pending → failed     (instance_killed)

stale → running      (dispatch_claimed)
stale → failed       (instance_killed)

running → fresh      (handler_complete with no held claim)
running → held       (handler_complete WITH held claim)
running → parked     (handler_park)
running → failed     (handler_error after policy give-up, instance_killed)
running → stale      (policy_retry / infra_reenqueue) — creates a new node-run, doesn't mutate this one
                       (technically not a self-transition; the "new node-run" carries forward the previous bag)

held → fresh         (auto_terminal_commit) — at this moment cascade fires terminal/success downstream
held → failed        (auto_terminal_abandon) — at this moment cascade fires terminal/error/abandoned downstream
held → failed        (instance_killed)

parked → stale       (deadline_resume) — bag preserved; this IS the same row, just re-eligible
                       (the deadline-wake case I shipped under NodeStateResuming/ReasonDeadlineResume
                       collapses into this transition; no separate resuming state)
parked → failed      (park_timeout, instance_killed)
```

`fresh` and `failed` have NO outgoing transitions. Today's `fresh + invalidate_received → stale` mutates the existing row; under the new model, cascade events targeting a `fresh` or `failed` node create a NEW node-run instead.

### Phase column drops out; `sequence` and `creation_reason` added

The `phase` column on `rimsky_node_runs` is removed. Every query that today filters by `phase IN ('pending','active','held','parked')` migrates to `state IN ('pending','stale','running','held','parked')`. Two new columns are added:

- `sequence` — monotonic per (node_id, run_scope_id, frame_id), assigned at row creation. Drives dispatcher claim order: lowest unblocked sequence wins. Carried by every row regardless of state.
- `creation_reason` — enum `{cascade, operator_invalidate, policy_retry, infra_reenqueue}`. Determines whether the row participates in cascade-walker accumulation (cascade only), goes through pending (cascade only), and is subject to mode rules (cascade only).

Auxiliary columns stay separate (they track orthogonal concerns):
- `claimed_by`, `claimed_at` — queue ownership for the running-state dispatch
- `last_heartbeat_at`, `last_progress_at` — liveness
- `frame_id` — frame membership
- `prior_dispatch_id`, `prior_dispatch_disposition` — retry chain lineage

### Cascade walker — accumulate-or-queue, never mutate

Cascade walker today has multiple paths: AffirmNodeRunRow for settled receivers, MarkStaleForCascade for in-flight receivers, wakeParkedReceiverInTx for parked receivers, pullForceRefreshUpstreams' upward-wake-parked-upstream branch.

Under the new model, the cascade walker has a simple three-case rule. It never mutates running/held/parked/stale runs; it ONLY creates new pending runs or accumulates wait-set rows into the latest pending one. The accumulation gate is **per-sender-node**: a new cascade row accumulates into the latest pending iff that pending's wait-set does not already cover the sender's node.

```
for each cascade walk targeting receiver R from sender S:
    R_latest = the latest (most-recently-created) pending receiver run
               for (R.node_id, R.run_scope_id, current_frame), if any

    if R_latest exists AND S.node_id NOT IN R_latest.wait_set.sender_nodes:
        # accumulate into the latest pending — diamond / multi-root case
        wait-set INSERT (frame, R_latest.id, S.run_id, topic, scope)
    else:
        # either no pending exists yet, OR the latest pending already covers S's node —
        # create a new pending; subsequent cascades for new sender_nodes will accumulate into it
        R_new = new pending node-run for (R.node_id, R.run_scope_id, current_frame)
        wait-set INSERT (frame, R_new.id, S.run_id, topic, scope)
    (drained_at populated immediately because S just settled — same tx as today)
```

Three observable cases:

- **R has no pending in this frame**: cascade creates R'_1 (pending).
- **R has a pending R'_i, sender's NODE NOT in R'_i's wait-set**: accumulate into R'_i. Diamond at the receiver (B and C both cascade to D → D'_1 has both rows on a single pending) and extra-root diamond (A→B, X→B with A and X both cascading to B → B'_1 has both rows) are this case.
- **R has a pending R'_i, sender's NODE IS in R'_i's wait-set**: create a new pending R'_(i+1). Same-upstream re-cascade during pending phase (U_2 cascades while R'_i is still pending after U_1 already contributed) is this case. R'_(i+1) starts a fresh wait-set; subsequent cascades from other sender_nodes accumulate into it.

Multiple pendings (R'_1, R'_2, …) can coexist when same-upstream re-cascades arrive during a pending phase. The **latest** pending is the accumulation target. Each pending transitions independently when its own wait-set drains and its gates clear; the self-gate (no other run in stale/running/held/parked) serializes their stale/dispatch in arrival order.

Bounds at any moment per (node, run-scope, frame):
- Pending count: unbounded above by same-upstream re-cascade volume during a single in-flight period.
- In-flight count (running ∪ held ∪ parked): ≤ 1 (self-gate).
- Stale count: depends on mode rule — ≤ 1 under `most-recent` (delete-on-transition); can grow under `sequenced` and the idempotent variants.

**Worked example: extra-root diamond** (A→B, X→B, A→C, B→D, C→D):

1. A settles → cascade fires for B and C.
   - B: no pending → create B'_1 (pending), wait-set row for A.
   - C: no pending → create C'_1 (pending), wait-set row for A.
2. X settles → cascade fires for B.
   - B: B'_1 exists, X's node NOT in `B'_1.wait_set.sender_nodes` → accumulate.
     B'_1 now has wait-set rows for A and X.
3. B'_1's wait-set drains (A and X settled). Gates clear. B'_1 transitions to stale; dispatcher claims; B dispatches; B settles.
4. B's cascade → D: no pending → create D'_1 (pending), wait-set row for B.
5. C'_1's wait-set drained back in step 1 (A was C's only subscribed upstream). C'_1 already transitioned to stale, dispatched, settled — independent of B's path.
6. C's cascade → D: D'_1 exists, C's node NOT in `D'_1.wait_set.sender_nodes` → accumulate. D'_1 now has wait-set rows for B and C.
7. D'_1's wait-set drains. Gates clear. D'_1 transitions stale; dispatch; D settles.

Single D'_1 with rows for both B and C. Extra-root accumulation (X feeding B at the top) is handled identically to the single-root diamond — rule (a) cares about distinct sender_nodes-in-wait-set, not number of roots upstream. The "new pending" branch fires only when the same sender_node re-cascades during a single pending phase (e.g., A re-runs while D'_1 is still pending, B re-cascades to D from the A re-run, then D'_2 is created because B is already in D'_1's wait-set).

No mode logic at this site. No state mutation of existing runs.

### Gate evaluator: cascade-driven pending → stale

The cascade-driven pending state has a single transition trigger: a wait-set row for the pending row drains. The gate evaluator (existing `drainWaitSetOnSettled` site) walks the affected receivers and checks the gates:

```
for each cascade-driven pending R' whose wait-set was touched by this drain:
    if R'.wait-set has no undrained rows
    AND no subscribed upstream of R has an in-flight run in this frame:
        # gates cleared — build bag, apply mode rule, transition pending → stale
```

"In-flight subscribed upstream" means: any subscribed upstream of R has a run in (pending, stale, running, held, parked) for the same (run-scope, frame).

The serialization gate ("no other run for same (node, run-scope) in running/held/parked") does NOT live here. It moves to the dispatcher: at claim time, the dispatcher refuses to claim a stale row if another run for the same (node, run-scope) is in (running, held, parked). Multiple stale rows can coexist (cascade-driven + non-cascade); the dispatcher serializes them via sequence-ordered claim.

The gate evaluator is THE site where:
1. Bag is built (carry-forward + cascade-overlay, per the next section).
2. Bag is persisted to `NodeAttributes(R'.id)`.
3. Mode rule applies (decides whether this R' becomes stale or gets dropped).
4. State transitions `pending → stale` (if not dropped).

After transition, the dispatcher picks up `stale` runs on its normal sweep, claims by lowest unblocked sequence, loads the persisted bag, dispatches. No "build at dispatch" path remains anywhere.

### Bag composition at cascade-driven pending→stale

When R'_i's gates clear, the bag is built before the mode rule fires:

1. Find the immediately-prior run by sequence for same (node_id, run_scope_id) — the highest-sequence run with `sequence < R'_i.sequence`. (Could be a queued stale ahead of R'_i, or the most recent settled, or nothing for a first-ever dispatch.)
2. Carry-forward: bag = predecessor's persisted bag (or empty if no predecessor).
3. Overlay: for each drained wait-set row on R'_i, fetch the sender's current `NodeAttributes`, and OVERRIDE the bag's entry for that sender's nodeType.
4. Resulting bag = R'_i's input bag, persisted to `NodeAttributes(R'_i.id)`.

This composition is the same for every cascade mode. The mode rule decides what happens to R'_i *after* the bag is built. (Non-cascade rows carry forward at creation time and skip the wait-set overlay entirely — see "Bag source variants" below.)

### Mode rules at cascade-driven pending→stale

Four modes, configured per-template (or per-node, TBD — see Open questions). All mode rules apply ONLY to cascade-driven rows (`creation_reason = cascade`); non-cascade rows (operator_invalidate / policy_retry / infra_reenqueue) are immune — they are never deleted by `most-recent` and never deduped by the idempotent variants.

- **`most-recent`** (DEFAULT). If a prior cascade-driven stale-not-claimed run exists for this (node, run-scope), DELETE it (CAS-protected against the dispatcher's concurrent claim transaction); R'_i takes its place. Non-cascade stales coexisting are left untouched. Cascade-stale depth ≤ 1 at a time. Effect: M cascade rounds during a single in-flight period produce 1 post-settle cascade dispatch with the latest view (plus any non-cascade dispatches operator/policy/infra requested).

- **`sequenced`** (opt-in). No delete, no dedup. R'_i transitions to stale alongside any prior stales. Cascade-stale queue can grow indefinitely; dispatch order follows sequence. Effect: M distinct cascade rounds → M post-settle dispatches, each with its own bag from its own moment.

- **`idempotent-queue`** (opt-in). Same queue behavior as `sequenced`, but at the transition: if a prior cascade-driven stale-not-claimed exists, JCS-canonicalize (RFC 8785 — already used in this codebase for `code:lib/graph/template/canonical::CanonicalSpecHash`) R'_i's bag and the prior stale's bag. If equal, DROP R'_i (don't transition). Else transition. Comparison ignores non-cascade stales. Effect: queue dedups consecutive identical-bag cascade entries; non-identical entries queue and dispatch normally. If no prior cascade stale exists (first-ever or just-cleared), no comparison — always transition.

- **`idempotent-settled`** (opt-in). Same as `idempotent-queue`, but the JCS comparison ALSO covers the most recent fresh-settled predecessor when no cascade-driven stale-not-claimed exists. Effect: the executor never re-runs for identical inputs across either queue or fresh boundaries, for cascade-driven re-runs. The "executor pure function" strictness still doesn't apply to explicit operator/policy/infra-initiated runs.

The two idempotency variants differ only in scope of comparison — `idempotent-queue` prevents redundant cascade-queue depth; `idempotent-settled` prevents redundant cascade invocation against any prior input.

**Implementation note for both idempotency variants**: the comparison wants the predecessor's INPUT bag (pre-dispatch) — what the executor saw — not the predecessor's CURRENT bag (post-executor-writeback). Today's `NodeAttributes.MergeDelta` overwrites the dispatched bag with writeback values, so input is lost once the run settles. Both idempotent modes require preserving the input bag separately (a column or sidecar on `NodeAttributes`, written at pending→stale and never overwritten by writeback). Carry-forward reads the live (current) bag; idempotency reads the input bag.

### Bag source variants

Three creation paths, distinguished by `creation_reason`:

- **Cascade-driven creation** (`creation_reason = cascade`, dominant path): walker creates a row in state `pending`. Bag is built at pending→stale transition from carry-forward (predecessor's persisted bag) + overlay (drained wait-set rows). Standard substitution-context-builder logic, relocated from the dispatcher to the gate evaluator. Subject to mode rules. Subject to walker accumulation rule (a).

- **Non-cascade re-run creation** (`creation_reason ∈ {operator_invalidate, policy_retry, infra_reenqueue}`): row is created directly in state `stale` with bag = carry-forward from the immediately-prior run by sequence (or empty if first-ever). Skips pending entirely. No wait-set rows. Not a walker accumulation target (walker rule (a) only looks at the latest pending). Not subject to mode rules. Dispatched by the dispatcher in sequence order, gated by "no other run for same (node, run-scope) in running/held/parked."
  - `policy_retry`: previous run errored, policy says retry. Same bag as the errored run (which is what carry-forward returns).
  - `infra_reenqueue`: executor crashed or heartbeat-failed. Same shape as `policy_retry`.
  - `operator_invalidate`: operator forces a re-run. If attribute overrides are needed, operator calls `set_attribute` first (separate debug action), then `invalidate_node`. The `set_attribute` mutates the live bag of the most recent settled run; the subsequent `invalidate_node` creates an operator-stale row that carries forward that (now-mutated) bag.

- **Initial creation** (instance start): just a regular cascade — the empty-message virtual sender's cascade walk creates the initial pending runs. `creation_reason = cascade`. Same drained-wait-set path as cascade-driven creation. No special case.

**Cascade-then-operator interaction.** If a cascade-driven pending R'_1 (seq 1) exists and operator-invalidate fires, an operator stale R'_2 (seq 2) is created. R'_1 continues its pending→stale path independently; when its gates clear, it transitions to stale (seq 1 still). Dispatcher claims seq 1 first, then seq 2 after the seq-1 run settles. Two dispatches, in arrival order — defensible default (operator explicitly asked); a "skip operator-invalidate if a cascade is about to run" mode is possible but not v1.

### `held` as state + cascade defer for held

Today: when a node-run's terminal includes a held=true claim, the cascade walker fires the terminal signal downstream IMMEDIATELY (before auto-terminal commit/abandon). Downstream may dispatch based on provisional held data. If auto-terminal later abandons, downstream has already acted on rolled-back state — no retract mechanism.

Under the new model:

- `held` becomes a state (not just a phase). When the executor returns held=true, the run transitions `running → held` and the cascade walk does NOT fire yet.
- When auto-terminal Commit fires, the run transitions `held → fresh` AND the cascade walks at this moment with the standard terminal/success signal. Downstream subscribers see the now-committed result.
- When auto-terminal Abandon fires, the run transitions `held → failed` AND the cascade walks at this moment with a new `terminal/error/abandoned` signal. Downstream subscribers that subscribe to `terminal/error/abandoned` (or to `terminal/error/*`) react. This is the "the work was lost, react to that" path.

`terminal/error/abandoned` is a real cascade-firable signal in the same shape as other `terminal/error/<class>` signals. The class is `"abandoned"` (or possibly a distinct `terminal/abandoned` root — see Open questions).

**Async-callback note.** Per `concept:parked-state`, the await-async-callback outcome keeps the node-run in `running` state (it is NOT a park) for the duration of the callback wait. Under the seven-state model, this means async-wait nodes already enjoy the running-state in-flight protection: cascade events targeting them queue new pendings per the walker rule, just as for any other running node-run. No special case for async-wait — it is one of the in-flight states the core invariant covers, full stop.

### Wait-set semantics under the new model

The wait-set schema is unchanged: `(frame, receiver_run, sender_run, topic_kind, subscription_scope, drained_at)`. Wait-set rows continue to be inserted at cascade-walk time, keyed by the receiver_run that owns them (either the latest pending Ri being accumulated into per rule (a), OR a newly-created R(i+1)). Drain continues to happen via `drainWaitSetOnSettled` when the sender settles. Non-cascade rows have no wait-set rows.

Each cascade-driven receiver run Ri carries its OWN wait-set rows for the cascade events that contributed to it. There is no row sharing across runs: D' has its rows, D'' has its (different) rows. Both follow rule (a) during their pending phase.

Two things move from dispatch-time to elsewhere:
- **Cascade-driven dispatch-eligibility** (today's pre-dispatch predicate: "no undrained wait-set rows + no in-flight subscribed upstream") becomes the gate evaluator's pending→stale gate check. For cascade-driven rows, state `stale` means input bag built and upstream cleared.
- **Serialization gate** ("no other run for same (node, run-scope) in (running, held, parked)") lives at dispatcher claim time. Multiple stales can coexist (cascade-driven + non-cascade); the dispatcher serializes via sequence-ordered claim with a CAS on the chosen stale. Stale is NOT in the serialization gate's exclusion set — that's the whole point of allowing the queue to exist.

Multiple invalidation cycles of an upstream produce new sender_runs and therefore new wait-set rows naturally — the wait-set already handles "multiple sender_runs per receiver" since rows are keyed by sender_run, not sender_node. No schema change needed for the multi-cycle case.

### Eligibility-as-state observability win (partial)

Today: a node-run in state `stale` may or may not be dispatch-eligible; the dispatcher's two-clause predicate decides at SELECT time. An operator inspecting "why isn't this dispatching?" has to manually run the predicate.

Under the new model: state `pending` explicitly says "cascade-driven, waiting on upstream cascades to drain." State `stale` explicitly says "input bag built; upstream dependency cleared; waiting only for the dispatcher's serialization gate (no in-flight for same (node, run-scope)) and a free dispatcher slot." Two stales for same (node, run-scope) are now possible (cascade + non-cascade queued); an operator inspecting "why isn't the second stale dispatching?" sees the first stale or in-flight run via the same row, ordered by `sequence`. A real diagnostics improvement over today's "predicate hidden in dispatcher SELECT," even if not the full state-tells-everything story.

**What moves to gate-eval vs stays at SELECT.** The upstream-dependency predicate (no undrained wait-set rows + no in-flight subscribed upstream) moves to the gate evaluator and is checked once at pending→stale transition; the dispatcher's SELECT no longer evaluates it. What stays at dispatcher SELECT time: `state = 'stale' AND no current claim`, with the serialization gate (`no other run for same (node, run-scope) in (running, held, parked)`) enforced as a CAS check at claim attempt (multiple stales may match the SELECT; sequence-order and the serialization-CAS together select the single winner per (node, run-scope)). The operator's "why isn't this dispatching?" reduces to two answers: another run for same (node, run-scope) is in (running, held, parked), or all dispatcher workers are busy. The upstream-dependency question never reaches the operator — it's already answered by the row being in `stale` at all.

### Implementation cost in rough strokes

- Migration: add `pending` to state CHECK; ensure `held` is a value in state too (was phase-only); drop the `phase` column. Add a `dispatch_input_bag` column or sidecar on `NodeAttributes` (preserves input-bag for idempotency comparison, separate from the writeback-mutated live bag). Add `sequence` (BIGINT, monotonic per (node_id, run_scope_id, frame_id)) and `creation_reason` (TEXT enum: `cascade | operator_invalidate | policy_retry | infra_reenqueue`) columns to `rimsky_node_runs`. The existing partial index `idx_node_runs_pending_idx` migrates from `WHERE phase = 'pending'` to a dispatcher-perf index `WHERE state = 'stale'`. Pre-v1 break-freely applies. SQLite needs the table-rebuild dance.
- State machine in `lib/foundation/cascade/state.go`: add `pending` and `held` states; redefine transitions; kill `NodeStateResuming` and `ReasonDeadlineResume`.
- Cascade walker (`runner_terminal.go::cascadeSubscribersStaleInTx`, `runner_terminal.go::pullForceRefreshUpstreams`, `message_delivery.go::cascadeMessageVirtualNodeSettleInTx`): collapse to rule (a) accumulation. Walker only creates cascade-driven pending rows (never touches stale rows, including non-cascade stales). Remove `MarkStaleForCascade` and `wakeParkedReceiverInTx` from these call sites.
- Gate evaluator: new function called from `drainWaitSetOnSettled` site. Walks affected cascade-driven pending receivers, evaluates gates (wait-set drained + no in-flight subscribed upstream), builds bag (carry-forward + cascade-overlay), applies mode rule (scoped to cascade-driven), transitions to stale.
- Dispatcher: drop `resolveAttributes` + `upsertAttributesPreDispatch` from the claim path. Replace with `loadBagByRunID` (already wired up as `loadResumeAttributes` for the parked-resume case — just generalize it). Add the serialization gate: refuse to claim a stale row if another run for same (node, run-scope) is in (running, held, parked). Add sequence-ordered claim: lowest unblocked sequence wins.
- Held cascade defer: in `runner_terminal.go` terminal handler, when the terminal includes held=true, transition to `held` state and skip the cascade walk. Add the cascade walk to the auto-terminal Commit (signal: terminal/success) / Abandon (signal: terminal/error/abandoned) handlers.
- Non-cascade re-run paths: convert `debug_override.go::applyDebugOverride` (operator-invalidate) and the policy-retry / infra-reenqueue sites in `runner_error_policy.go` to create a new row directly in state `stale` with the carry-forward bag, sequence assigned at creation, `creation_reason` set appropriately. Skips pending entirely. No wait-set rows.
- Per-template/per-node mode config: new field in the template node-spec, four enum values (`most-recent`, `sequenced`, `idempotent-queue`, `idempotent-settled`), threaded through to the gate evaluator so it knows which mode rule to apply.
- Already-shipped collapse: delete `NodeStateResuming`, `ReasonDeadlineResume`, the `IsResume` field on acquisition, the `loadResumeAttributes` branch — replaced by the general "every dispatch loads the persisted bag" rule.

### Design artifact plan

Implementation will be direct (no `/brainstorm` → `/write-plan` → `/execute-plan`), but the design layer under `.ok-planner/design/` still gets updated alongside the code. Each story anchors a scenario test via `// @story:` annotation; each decision records a non-obvious pivot via `// @decision:` annotation; each concept doc is the canonical statement of the model the code enforces.

**Stories** (`.ok-planner/design/stories/`):

- *Existing*: `story:resume-preserves-snapshot` — survives the redesign (user-outcome holds; mechanism generalizes from distinct-resuming-state to "every dispatch loads persisted bag"). Edit to remove any distinct-resuming-state framing.
- *New*: `story:cascade-defers-during-flight` — node in running/held/parked is not interrupted by upstream cascades; cascade queues a new node-run that dispatches after the current settles. (Core invariant in observable form.)
- *New*: `story:held-commit-cascades-success` — downstream subscribers see `terminal/success` only when held work commits, not the moment the held terminal arrives.
- *New*: `story:held-abandon-cascades-abandoned` — downstream subscribers see `terminal/error/abandoned` when held work rolls back.
- *New*: `story:operator-invalidate-queues-during-flight` — operator-invalidate while a run is in-flight produces a stale row that dispatches after the in-flight run settles, with carry-forward bag at invalidate-moment.
- *New*: `story:most-recent-coalesces-cascades` — default mode coalesces multiple cascade rounds during a single in-flight period into one post-settle dispatch with the latest bag.
- *New*: `story:sequenced-preserves-cascade-rounds` — opt-in mode dispatches M times for M cascade rounds, each with its own bag.
- *New*: `story:idempotent-mode-dedupes` — opt-in modes (both variants) drop re-runs whose input bag JCS-equals the predecessor's.

**Decisions** (`.ok-planner/design/decisions/`):

- *Retire*: `decision:parked-resume-distinct-state` → `_retired/`. The distinct resuming state is collapsed into the general "every dispatch loads persisted bag" rule.
- *New*: `decision:walker-rule-per-sender-node` — rule (a) over rule (b); makes sequenced mode meaningful at the right granularity.
- *New*: `decision:non-cascade-direct-to-stale` — skip pending entirely for operator/retry/reenqueue; immune to mode rules.
- *New*: `decision:held-as-state-not-phase` — held promoted from phase to state with cascade-defer.
- *New*: `decision:terminal-error-abandoned-as-error-class` — `terminal/error/abandoned` over a new `terminal/abandoned` root signal.
- *New*: `decision:mode-default-most-recent` — default coalescing over sequenced or strict-once.

(Mechanical-consequence pivots — serialization-gate-at-dispatcher, input-bag-preservation-column — are not promoted to standalone decision docs; they live inside the affected concept docs.)

**Concept doc updates** (`.ok-planner/design/concepts/`):

- `concept:cascade` — invariants: replace "all-in-flight-upstreams-resolve-first" with "in-flight runs are sealed; cascade creates new pending." Add per-sender-node walker rule.
- `concept:node-run` — state machine: replace state×phase product with the seven-state machine. Add `sequence` and `creation_reason` columns. Document transitions.
- `concept:parked-state` — resume-context: remove distinct-resuming-state framing. Generalize "every dispatch loads persisted bag." Time-wake transitions parked→stale (subject to dispatcher serialization).
- `concept:auto-terminal` — cascade fires from auto-terminal handlers (Commit → `terminal/success`, Abandon → `terminal/error/abandoned`), not from the held-run's terminal.
- `concept:claim-handle` — held-claim cascade-defer interaction. Held is a node-run state, not just a phase.
- `concept:wait-set` — gate-evaluator semantics. Multiple pendings per (node, run-scope). Per-sender-node accumulation.
- `concept:signal` — add `terminal/error/abandoned` to the canonical taxonomy.
- `concept:frame` — clarify whether `pending` counts as in-flight for frame-end purposes (resolves a sketch Open question; resolution TBD during implementation).

Possibly one new concept doc, deferred until the modes prove they need it:

- `concept:cascade-mode` — the four-mode config (`most-recent`, `sequenced`, `idempotent-queue`, `idempotent-settled`). Default home: section under `concept:cascade`. Promote to standalone only if the mode surface grows.

**Annotation grammar reminder.** Per the plumbline citation-resolution check, every `// @story:`, `// @decision:`, `// @concept:` annotation in source code must resolve to a real file. The order of operations during implementation is: write the design artifact first, then add the annotation in source. (The reverse order tripped the lint hook earlier in this branch's history.)

### What stays as-is

- The cascade walker's edge-matching logic (subscription-edge map, `force_upstream_refresh` walks).
- Frame model (per-instance serialization, message-driven frame creation).
- Claim handle mechanics (acquisition, held lifetime, auto-terminal commit/abandon).
- Wait-set schema.
- The substitution-context-builder itself (just moves from dispatch-time to drain-time).

## Open questions

- **Where does the per-template mode config live?** Field on the template node-spec is the natural home. Per-node-type OR per-instance OR per-template? Default `most-recent`. Field naming: not `parked_cascade_mode` since the rule applies to running/held cascades too — something like `cascade_queue_mode` or `cascade_mode` reads more accurate. Four values: `most-recent`, `sequenced`, `idempotent-queue`, `idempotent-settled`.

- **`terminal/error/abandoned` shape: subclass or root?** Either `terminal/error/<class>` with `class=abandoned` (matches existing error-class mechanism, subscriber subscribes via `terminal/error/abandoned` or `terminal/error/*`) OR a distinct `terminal/abandoned` root signal. Marginal preference for the former — uniform with existing error signals.

- **`pending → failed` on `instance_killed` — what does the bag look like?** Probably just leaves the bag unbuilt (the pending state never built one). The lineage record needs to handle "killed before built" gracefully.

- **Drain-handler complexity and locking.** The drain handler runs per-terminal, walks all receivers whose wait-set was touched, evaluates gates, potentially builds bags and transitions states. The bag-build can be substantial work. This needs to happen in the terminal-handler transaction OR be deferred to a separate worker. If deferred, there's a window where a settled sender exists but its dependent receivers haven't yet transitioned to stale.

- **`pending` runs and frame holds.** A pending run with no wait-set rows yet (just-created, hasn't had any cascades fire to it) is effectively "waiting indefinitely." Does this hold the frame open the way parked does? The held-frame model needs to clarify whether pending counts as in-flight for frame-end purposes.

- **Sequenced mode's wait-set semantics across cycles.** When R'_2 is created after R'_1, both pending, their wait-set rows reference DIFFERENT sender_runs. R'_2's wait-set has rows for the new senders. R'_1's wait-set retains its old rows. As each clears its gates independently, they transition in order. This seems to work naturally but needs a clean test that exercises it.

- **Operator-invalidate while a run is in-flight.** Operator invalidates node R. R has a running predecessor. New stale R' is created (`creation_reason=operator_invalidate`, next sequence). The dispatcher's serialization gate prevents claiming R' until the predecessor settles. Is this the right operator UX, or does operator-invalidate need a "force" mode that kills the in-flight predecessor first?

- **Mode rule per-cascade-source.** Could imagine wanting different mode rules for different cascade sources (e.g., most-recent for high-frequency attribute changes, sequenced for message-driven events). The current design has one mode per node. Open whether that's enough.

- **Migration ordering for existing data.** Pre-v1 says drop-and-recreate is OK. But the migration would need to map existing rows (state, phase) → new (state) before dropping phase. Concretely: any row with `phase='completed'` → state='fresh'; `phase='held'` → state='held'; etc. Straightforward, just needs to be written.

## Resolution log

Items raised during inline review pre-compact and resolved in the post-compact discussion. Kept as an audit trail of the design pivots.

### Gaps to resolve

**Gap 1: Walker rule consistency.** ✅ RESOLVED. Rule (a) — per-sender-node accumulation gate — is confirmed and inlined into the cascade walker section above. The pseudo-code and the three-case description both encode (a). Multiple pendings can coexist; the latest is the accumulation target; the self-gate serializes their stale/dispatch.

**Gap 2: Non-cascade re-run paths lack a drain-event trigger.** ✅ RESOLVED via direct-to-stale. Non-cascade paths (operator-invalidate, policy-retry, infra-reenqueue) skip pending entirely and create rows directly in state `stale` with the carry-forward bag built at creation moment. Two supporting fields added to `rimsky_node_runs`: `sequence` (monotonic per (node, run-scope, frame), drives dispatcher claim order) and `creation_reason` (`cascade | operator_invalidate | policy_retry | infra_reenqueue`). Walker rule (a) targets only the latest pending — non-cascade stales aren't accumulation targets by definition, so no carve-out needed. The serialization gate ("no other run for same (node, run-scope) in running/held/parked") moves from the pending→stale transition to the dispatcher's claim site; multiple stales (cascade + non-cascade) coexist, dispatcher claims by sequence. Mode rules scoped to `creation_reason = cascade` only — non-cascade runs are immune to most-recent's delete and to idempotent variants' dedupe. This deliberately produces two dispatches when operator-invalidate fires during a cascade-driven pending: defensible (operator asked, operator gets a run); a "skip-if-cascade-pending" mode is possible but not v1.

**Gap 3: Queue depth claims depend on which walker rule applies.** ✅ RESOLVED via Gap 1 confirmation. Under (a) the bounds hold as stated: pending count unbounded above by re-cascade volume; in-flight ≤ 1 via self-gate; stale ≤ 1 under `most-recent` (delete-on-transition), unbounded under `sequenced` and idempotent variants.

### Smaller items to add or address

- **Extra-root diamond example** (A→B, X→B, A→C, B→D, C→D): ✅ ADDED. Inline worked walk-through in the cascade walker section after the bounds list. Demonstrates accumulation across multiple roots and contrasts with the same-upstream re-cascade case that triggers a new pending under rule (a).
- **Async-callback semantics**: ✅ ADDED. Inline note in the held-as-state section confirming async-wait nodes stay `running` and inherit running-state in-flight protection without a special case.
- **SELECT-time vs drain-time eligibility distinction**: ✅ ADDED. Inline paragraph in the eligibility-as-state observability section makes the split explicit: upstream-dependency moves to gate-eval; `state = stale AND no claim` stays at dispatcher SELECT; serialization gate enforced as CAS at claim attempt.
- **Existing partial index `idx_node_runs_pending_idx`**: ✅ ADDRESSED. Now called out in the implementation cost section: migrates from `WHERE phase = 'pending'` to `WHERE state = 'stale'`. Same dispatcher-perf role, different column.
- **Multi-row-same-sender-node bag-build**: ✅ RESOLVED via Gap 1. Under rule (a), at most one wait-set row per sender_node per pending; overlay is unambiguous, no tie-breaker needed.

### Decisions made (re-confirmed post-compact)

Settled design choices. These are the load-bearing pieces the implementation will be built on:

1. **Core invariant**: no re-invalidation of dispatched node-runs. Cascade events targeting in-flight runs queue new node-runs; never mutate state or bag of existing runs.
2. **Seven-state state machine**: `pending`, `stale`, `running`, `held`, `parked`, `fresh`, `failed`. `phase` column dropped; `state` is the unified column. Two new columns added: `sequence` (monotonic per (node, run-scope, frame), drives dispatcher claim order) and `creation_reason` (`cascade | operator_invalidate | policy_retry | infra_reenqueue`).
3. **Walker rule (a)**: per-sender-node accumulation gate. Cascade walker creates a new pending iff the latest pending's wait-set already covers the sender's node; otherwise accumulates into the latest pending. Multiple pendings can coexist; the latest is the accumulation target.
4. **Held as state with cascade defer**: cascade fires from `held` only at auto-terminal commit (signal: `terminal/success`) or abandon (signal: `terminal/error/abandoned`). Downstream sees only committed/abandoned signals, never provisional.
5. **`terminal/error/abandoned` as a cascade-firable signal**: shape is `terminal/error/<class>` with `class=abandoned` (uniform with other error signals); subscribers via `terminal/error/abandoned` or `terminal/error/*`.
6. **Bag composition for cascade-driven rows**: at pending→stale transition, carry-forward from immediately-prior run by sequence + overlay from drained wait-set rows. Non-cascade rows carry forward at creation moment without overlay.
7. **Four modes, scoped to cascade-driven rows** (`creation_reason = cascade`):
   - `most-recent` (default): delete prior cascade-driven stale-not-claimed; R'_i takes its place. Cascade-stale depth ≤ 1 (non-cascade stales untouched).
   - `sequenced` (opt-in): no delete, no dedup; queue grows.
   - `idempotent-queue` (opt-in): JCS-compare to prior cascade-driven stale; drop self if equal.
   - `idempotent-settled` (opt-in): also compares against most recent fresh-settled predecessor.
   Non-cascade rows are immune to all mode rules.
8. **Input-bag preservation column** on `NodeAttributes` for idempotency comparison: separate from the live (writeback-mutated) bag. Carry-forward reads live; idempotency reads input.
9. **Non-cascade re-run paths skip pending entirely** (operator-invalidate, policy-retry, infra-reenqueue): create row directly in state `stale` with carry-forward bag at creation moment. Not walker accumulation targets. Not subject to mode rules. Dispatched in sequence order. Operator overrides via separate `set_attribute` debug action.
10. **Initial creation is a regular cascade** (empty-message virtual sender's cascade walk); `creation_reason = cascade`; no special case.
11. **Gate evaluator is the bag-build + transition site** for cascade-driven creation. Single trigger: wait-set row drain (`drainWaitSetOnSettled`). Non-cascade paths build their bag at creation site and write `stale` directly.
12. **Eligibility-as-state (partial)**: pending = "cascade-driven, waiting on upstreams to drain"; stale = "input bag built, upstream cleared, waiting only on dispatcher serialization gate." Upstream-dependency check moves to gate-eval; `state = stale AND no claim` stays at dispatcher SELECT; serialization gate enforced as CAS at claim attempt.
13. **Serialization gate at dispatcher, not at pending→stale transition**: "no other run in (running, held, parked)." Stale is excluded (multiple stales coexist — cascade-driven + non-cascade — and the dispatcher serializes by `sequence`-ordered claim with CAS). Pending is excluded (accumulation state, not a serialization gate).
14. **Wait-set keyed per receiver_run** (today's schema, no change): each cascade-driven pending Ri carries its own wait-set rows. No row sharing across runs. Non-cascade rows have no wait-set rows.
15. **Already-shipped collapse**: `NodeStateResuming`, `ReasonDeadlineResume`, `IsResume`, `loadResumeAttributes`, migration 015 all get absorbed into the new design. The bag-load mechanism (right) becomes the general dispatcher path; the state machine is redesigned around it.

## Risks / unknowns

- **Scope is large.** This redesign touches the cascade walker, the dispatcher, the state machine, the persistence layer, the terminal handler, the auto-terminal handler, the debug-override path, the policy-retry path, and several scenario tests. The "rip everything out and rewire" is bigger than the loose-ends fix I started with. Implementing inline as discussed is feasible but a multi-day refactor at minimum.

- **Cascade defer for held changes existing test semantics.** The `parked_lifecycle` tests with held+inheritor patterns expect inheritor to dispatch promptly after acquirer's terminal. Under cascade-defer-for-held, inheritor doesn't see acquirer's terminal until auto-terminal commit. Those tests need re-walking and possibly redesign.

- **Held throughput.** Any node that produces a held terminal blocks its downstream until the held scope (subgraph) commits. Held scopes spanning long subgraphs meaningfully delay downstream. This is correct under the invariant but a real behavior change that operators using held-claim patterns will notice.

- **Drain handler in the hot path.** Today's drain is a bulk SQL UPDATE. Under the new model, it ALSO walks the affected receivers and evaluates gates and potentially builds bags. The terminal-handler tx grows. Care needed to keep this bounded — maybe gate evaluation is synchronous but bag-building is deferred to a worker that polls for "wait-set drained but bag not built."

- **Sequenced and idempotent-queue/settled modes open up unbounded queues.** Default `most-recent` collapses the queue to ≤1 stale at a time. The opt-in modes allow the queue to grow. A stuck in-flight node-run (parked indefinitely, held forever) plus rapid upstream cascades could grow the pending-and-stale queue unboundedly. Worth defining queue caps before the opt-in modes are actually exposed to users.

- **What I already shipped on this branch.** NodeStateResuming, ReasonDeadlineResume, IsResume, loadResumeAttributes, migration 015 — all of it needs to collapse into the new design. Either revert before starting fresh, OR absorb the partial pieces into the bigger change (e.g., the bag-load mechanism becomes the general dispatcher path; the deadline-resume transition reason becomes part of the parked→stale transition). My lean: absorb, since the bag-load mechanism is right; the state machine just needs to be redesigned around it.

- **Mode-rule "delete" race conditions.** Under `most-recent`, when R'_i transitions to stale and a prior stale-not-claimed exists, we DELETE the prior. But the dispatcher might be in the middle of claiming the prior. Needs CAS on the prior's state column: if the CAS loses to the dispatcher's claim transaction, the prior becomes running, R'_i queues behind, "most-recent missed this one boundary" — defensible, won't violate any invariant.

- **The conversation that produced this sketch was long.** Multiple design pivots happened (the snapshot-at-creation bug, the pending-state need, the held-as-state realization). The final design is internally consistent, but spotting any remaining inconsistencies needs careful walk-through by fresh eyes — possibly via `/brainstorm` if we want a formal spec.

## What this is not

- Not a spec. The Open questions section names what still needs deciding before this could go through `/brainstorm`.
- Not a migration plan. The schema migration is sketched at a high level but the actual SQL hasn't been written.
- Not a backwards-compat story. Pre-v1 means we break freely; users on the prior schema upgrade by re-creating their dev databases. No compatibility shims.
- Not scope-creep into adjacent concerns. The cascade walker, the state machine, the dispatcher, and the wait-set are in scope. Frame model, claim handles, executor protocols, message delivery wire shape — all out of scope for this redesign, even though they touch the affected code.
- Not a deletion of the wait-set mechanism. The wait-set IS the right data structure for tracking dependencies; this design just moves WHEN it's consulted (drain-time instead of dispatch-time) and adds a new self-gate alongside it.
