# Cascade no-re-invalidation — Design Sketch

**Date:** 2026-06-19
**Status:** Sketch (not a spec; not authorization to build)

## Idea

A node-run, once dispatched, is sealed: nothing re-invalidates it, mutates its state, or rewrites its substituted attribute bag until it settles to a terminal state on its own (success / error / park-then-wake-then-settle / held-then-auto-terminal). Cascade events that target an already-dispatched node-run do not interrupt — they queue a new node-run that the system dispatches in order once the current run finishes. This makes the in-flight node-run's view of the world fixed for its lifetime.

The model uses a single state machine with seven values across `state`, makes `held` a state so cascade can defer to non-members of the held subgraph until auto-terminal commit/abandon, and introduces `terminal/error/abandoned` as a subscribable signal so downstream of held work can react when held work rolls back.

## Shape

### Core invariant

A node-run in `running`, `held`, `parked`, or `pending` state is never re-invalidated, never has its state mutated, never has its substituted attribute bag rewritten by anything other than its own executor's writeback. Cascade events targeting such a node-run cause a NEW node-run to be created for the same (node, run-scope); the new run waits in line.

### Seven-state state machine

| State | Meaning | Has bag? | Dispatch-eligible? |
|---|---|---|---|
| `pending` | created, waiting for upstream cascades to settle (wait-set draining) | no | no |
| `stale` | gates cleared, bag built and persisted, ready to dispatch | yes (frozen) | yes (subject to self-gate) |
| `running` | claimed by dispatcher, executor in flight | yes (frozen) | no (running) |
| `held` | executor returned with held=true claim; cascade to non-members deferred awaiting auto-terminal commit/abandon | yes (frozen) | no (in-flight via held) |
| `parked` | executor returned park terminal | yes (frozen) | no (in-flight via park) |
| `fresh` | settled successfully — TERMINAL, no outgoing transitions | yes (final) | no (settled) |
| `failed` | settled with terminal/error or held + auto_abandon — TERMINAL, no outgoing transitions | yes (final) | no (settled) |

#### Transitions

```
pending → stale      (gate_cleared: wait-set fully drained + no in-flight upstream + no in-flight self)
pending → failed     (instance_killed) — bag was never built; lineage record handles "killed before built" with no carry-forward to clean up

stale → running      (dispatch_claimed)
stale → fresh        (pure_cascade) — no-executor node settles at gate time
stale → fresh        (acquire_pass) — acquire phase declines work; settle pass
stale → failed       (policy_retry, policy_give_up, infra_reenqueue) — pre-dispatch error
stale → failed       (dispatch_impossible) — dispatch can't proceed (e.g. claim invariant violated)
stale → failed       (instance_killed)

running → fresh      (handler_complete AND run has no active claim participation AND no poisoned-by-abandoned-claim portfolio)
running → held       (handler_complete OR handler_error AND run participates in at least one active claim_handle — as acquirer with held=true claim, or as co-holder via claim_holders)
running → parked     (handler_park)
running → failed     (handler_error AND no active claim participation; or policy_give_up, instance_killed)
running → failed     (auto_terminal_abandon) — co-holder settle when its claim_holders portfolio includes any abandoned claim (poison rule, claim resolved during this run's release-locks)

held → fresh         (last participating claim resolved, all resolved=committed) — this holder fires its deferred terminal/success cascade to non-members
held → failed        (last participating claim resolved, any resolved=abandoned, poison rule) — this holder fires its deferred terminal/error/abandoned cascade to non-members
held → failed        (instance_killed)

parked → stale       (deadline_resume) — bag preserved; same row, just re-eligible
parked → failed      (park_timeout, instance_killed)
```

`fresh` and `failed` have NO outgoing transitions. Cascade events targeting a `fresh` or `failed` node create a NEW node-run.

For `policy_retry` and `infra_reenqueue` on a run with no active claim participation: the prior run transitions `running → failed` and a NEW node-run is created in `stale` for the retry attempt. For a run with active claim participation, errors in the held subgraph are aborts (no in-period retry) — see "`held` as state with subgraph-scoped cascade defer" below. (See "Bag source variants" for retry-run creation.)

### `rimsky_node_runs` columns

- `sequence` — monotonic per (node_id, run_scope_id, frame_id), assigned at row creation. Drives dispatcher claim order: lowest unblocked sequence wins.
- `creation_reason` — enum `{cascade, operator_invalidate, policy_retry, infra_reenqueue}`. Determines walker accumulation participation (cascade only), pending-vs-direct-stale path (cascade goes through pending; non-cascade goes direct to stale), and mode-rule applicability (cascade only).
- `claimed_by`, `claimed_at` — queue ownership for the running-state dispatch.
- `last_heartbeat_at`, `last_progress_at` — liveness.
- `frame_id` — frame membership.
- `prior_dispatch_id`, `prior_dispatch_disposition` — retry chain lineage.

A `dispatch_input_bag` sidecar on `NodeAttributes` preserves the input bag (the bag the executor saw at dispatch) for idempotency comparison, separate from the writeback-mutated live bag.

### Cascade walker — accumulate-or-queue, never mutate

The cascade walker has a three-case rule. It never mutates running/held/parked/stale runs; it ONLY creates new pending runs or accumulates wait-set rows into the latest pending one. The accumulation gate is **per-sender-node**: a new cascade row accumulates into the latest pending iff that pending's wait-set does not already cover the sender's node.

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
- **R has a pending R'_i, sender's NODE IS in R'_i's wait-set**: create a new pending R'_(i+1). Same-upstream re-cascade during pending phase is this case. R'_(i+1) starts a fresh wait-set; subsequent cascades from other sender_nodes accumulate into it.

Multiple pendings (R'_1, R'_2, …) can coexist when same-upstream re-cascades arrive during a pending phase. The **latest** pending is the accumulation target. Each pending transitions independently when its own wait-set drains and its gates clear; the self-gate (no other run in stale/running/held/parked) serializes their stale/dispatch in arrival order.

Bounds at any moment per (node, run-scope, frame):
- Pending count: unbounded above by same-upstream re-cascade volume during a single in-flight period.
- In-flight count (running ∪ held ∪ parked): ≤ 1 (self-gate).
- Stale count: depends on mode rule — ≤ 1 under `most-recent`; can grow under `sequenced` and the idempotent variants.

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

**Force-pull from upstreams.** When a receiver R subscribes to an upstream U with a force-refresh predicate (template-declared "fetch fresh upstream value before dispatch"), the cascade walker's create-pending path probes whether U has a recent settled run available; if not, it creates a pending run for U as well and inserts a pre-seed wait-set row on R pointing to U's pending. The pre-seed counts as covering U's node for rule (a) — a subsequent natural cascade from U dedupes against the pre-seeded row by `sender_run_id` rather than triggering the "new pending" branch.

### Gate evaluator: cascade-driven pending → stale

The cascade-driven pending state has a single transition trigger: a wait-set row for the pending row drains. The gate evaluator walks the affected receivers and checks the gates:

```
for each cascade-driven pending R' whose wait-set was touched by this drain:
    if R'.wait-set has no undrained rows
    AND no subscribed upstream of R has an in-flight run in this frame:
        # gates cleared — build bag, apply mode rule, transition pending → stale
```

"In-flight subscribed upstream" means: any subscribed upstream of R has a run in (`pending`, `stale`, `running`, `held`, `parked`) for the same (run-scope, frame). EXCEPTION: an upstream run in `held` state is skipped if that upstream is a co-member with R of any held subgraph (R and the upstream both appear as holders — acquirer or co-holder — in `claim_holders` for some claim_handle still in `active` state). R cannot be gated by an upstream R shares a held transaction with. All other in-flight states block normally.

The serialization gate ("no other run for same (node, run-scope) in `running` / `held` / `parked`") lives at the dispatcher: at claim time, the dispatcher refuses to claim a stale row if another run for the same (node, run-scope) is in that set. Multiple stale rows can coexist (cascade-driven + non-cascade); the dispatcher serializes them via sequence-ordered claim.

The gate evaluator is THE site where:

1. Bag is built (carry-forward + cascade-overlay, per the next section).
2. Bag is persisted to `NodeAttributes(R'.id)`.
3. Mode rule applies (decides whether this R' becomes stale or gets dropped).
4. State transitions `pending → stale` (if not dropped).

After transition, the dispatcher picks up `stale` runs on its normal sweep, claims by lowest unblocked sequence, loads the persisted bag, dispatches. Every dispatch loads the persisted bag; no path resolves the bag at dispatch time.

The gate evaluator executes synchronously within the terminal-handler transaction that drained the wait-set row. Walking receivers, building bags, applying mode rules, and persisting state transitions all complete before the sender's settle commits.

### Bag composition at cascade-driven pending→stale

When R'_i's gates clear, the bag is built before the mode rule fires:

1. Find the immediately-prior run by sequence for same (node_id, run_scope_id) — the highest-sequence run with `sequence < R'_i.sequence`.
2. Carry-forward: bag = predecessor's persisted bag (or empty if no predecessor).
3. Overlay: for each drained wait-set row on R'_i, fetch the sender's current `NodeAttributes`, and OVERRIDE the bag's entry for that sender's nodeType.
4. Resulting bag = R'_i's input bag, persisted to `NodeAttributes(R'_i.id)`.

This composition is the same for every cascade mode. The mode rule decides what happens to R'_i *after* the bag is built. Non-cascade rows carry forward at creation time and skip the wait-set overlay entirely.

`sequence` is monotonic per (node_id, run_scope_id, frame_id) — each sub-scope has its own sequence space. For runs in sub-scopes (fan-out children, sub-graph delegations), the first run in a fresh sub-scope has no prior; bag starts empty. Subsequent runs in the same sub-scope carry forward from the prior in that sub-scope.

### Mode rules at cascade-driven pending→stale

Four modes, configured per-template-node via the `cascade_mode` field. All mode rules apply ONLY to cascade-driven rows (`creation_reason = cascade`); non-cascade rows are immune.

- **`most-recent`** (DEFAULT). If a prior cascade-driven stale-not-claimed run exists for this (node, run-scope), DELETE it (CAS-protected against the dispatcher's concurrent claim transaction); R'_i takes its place. Non-cascade stales coexisting are left untouched. Cascade-stale depth ≤ 1 at a time. Effect: M cascade rounds during a single in-flight period produce 1 post-settle cascade dispatch with the latest view (plus any non-cascade dispatches operator/policy/infra requested).

- **`sequenced`** (opt-in). No delete, no dedup. R'_i transitions to stale alongside any prior stales. Cascade-stale queue can grow indefinitely; dispatch order follows sequence. Effect: M distinct cascade rounds → M post-settle dispatches, each with its own bag from its own moment.

- **`idempotent-queue`** (opt-in). Same queue behavior as `sequenced`, but at the transition: if a prior cascade-driven stale-not-claimed exists, JCS-canonicalize (RFC 8785, the same canonicalization used by `code:lib/graph/template/canonical::CanonicalSpecHash`) R'_i's input bag and the prior stale's input bag. If equal, DROP R'_i (don't transition). Else transition. Comparison ignores non-cascade stales. Effect: queue dedups consecutive identical-bag cascade entries; non-identical entries queue and dispatch normally.

- **`idempotent-settled`** (opt-in). Same as `idempotent-queue`, but the JCS comparison ALSO covers the most recent fresh-settled predecessor when no cascade-driven stale-not-claimed exists. Effect: the executor never re-runs for identical inputs across either queue or fresh boundaries, for cascade-driven re-runs.

Both idempotency variants read the predecessor's INPUT bag (the bag the executor saw at dispatch), preserved separately from the writeback-mutated live bag.

### Bag source variants

Three creation paths, distinguished by `creation_reason`:

- **Cascade-driven creation** (`creation_reason = cascade`, dominant path): walker creates a row in state `pending`. Bag is built at pending→stale transition from carry-forward (predecessor's persisted bag) + overlay (drained wait-set rows). Subject to mode rules. Subject to walker accumulation rule (a).

- **Non-cascade re-run creation** (`creation_reason ∈ {operator_invalidate, policy_retry, infra_reenqueue}`): row is created directly in state `stale` with bag = carry-forward from the immediately-prior run by sequence (or empty if first-ever). Skips pending entirely. No wait-set rows. Not a walker accumulation target. Not subject to mode rules. Dispatched in sequence order, gated by "no other run for same (node, run-scope) in running/held/parked."
  - `policy_retry`: previous run errored, policy says retry. Bag = carry-forward.
  - `infra_reenqueue`: executor crashed or heartbeat-failed. Bag = carry-forward.
  - `operator_invalidate`: operator forces a re-run. If attribute overrides are needed, operator calls `set_attribute` first (separate debug action), then `invalidate_node`. The `set_attribute` mutates the live bag of the most recent settled run; the subsequent `invalidate_node` creates an operator-stale row that carries forward that (now-mutated) bag.

- **Initial creation** (instance start): a regular cascade — the empty-message virtual sender's cascade walk creates the initial pending runs. `creation_reason = cascade`. Same drained-wait-set path as cascade-driven creation.

**Cascade-then-operator interaction.** If a cascade-driven pending R'_1 (seq 1) exists and operator-invalidate fires, an operator stale R'_2 (seq 2) is created. R'_1 continues its pending→stale path independently; when its gates clear, it transitions to stale (seq 1 still). Dispatcher claims seq 1 first, then seq 2 after the seq-1 run settles. Two dispatches, in arrival order.

### `held` as state with subgraph-scoped cascade defer

`held` is the state for any node-run that participates in at least one not-yet-resolved claim_handle — both the original acquirer of a held=true claim AND every co-holder that has registered against that claim via `claim_holders`. The trigger for held entry is uniform: at run settle time, query the run's `claim_holders` rows joined to `claim_handles`; if ANY claim_handle is still in `active` state, the run transitions `running → held` instead of `running → fresh / failed`. The acquirer's case (executor returns held=true, creating a new active claim_handle that names this run as a holder) is a special instance of the same rule.

**Cascade from a held node** is filtered to **held-subgraph members only**. Membership is the union of subgraphs the sender participates in: for each `active` claim_handle the sender holds (via `claim_holders`), the subgraph is the set of nodes declared as holders in the template (the acquirer of that claim's type plus all nodes with `Holds.From=<acquirer's nodeType>` for that alias). A receiver R is a member iff R's node_type is in the union of those subgraphs. Non-member receivers do NOT see cascade during the held period.

This filter handles overlap automatically: a held run participating in claims X and Y cascades to receivers that are members of EITHER X's subgraph OR Y's subgraph.

**The commit/abandon decision per claim** is made by `CheckAndFireResolution`. When called for a specific claim_handle, it walks the holders of that claim:

- If any holder's `claim_holders` row state is `active` (the holder hasn't settled yet) → return, wait.
- Else: outcome = `commit` if no holder is in `failed` state, else `abandon`. The claim_handle transitions to `committed` or `abandoned`.

**Per-holder run transition** when a claim resolves: walk every row in `claim_holders` for the just-resolved claim_handle. For each holder run R:

1. Query R's OTHER `claim_holders` rows, join to `claim_handles`. If any other claim_handle is still `active` → R stays held (will be re-evaluated when those resolve).
2. Else (R's portfolio fully resolved): apply the poison rule across all R's claim outcomes.
   - All committed → R transitions `held → fresh`, fires its **deferred terminal cascade** to non-members with `terminal/success`. The signal carries R's own outputs from the run R completed.
   - Any abandoned → R transitions `held → failed`, fires `terminal/error/abandoned` to non-members. The signal type is `terminal/error/abandoned` regardless of R's individual settle color — abandoned poisons R's outputs for outside observers, because those outputs depended on a claim that did not commit.

`terminal/error/abandoned` is a cascade-firable signal in the same shape as other `terminal/error/<class>` signals. Class is `"abandoned"`. Subscribers matching `terminal/error/abandoned` or `terminal/error/*` receive it.

**Errors inside a held subgraph are aborts.** If a co-holder's executor returns error: the run's `claim_holders` row transitions to `failed` state; the run transitions `running → held` (per the uniform rule, the claim is still active); `CheckAndFireResolution` next fires, sees the failed holder, decides abandon; the claim resolves abandoned; every participating holder transitions `held → failed` via the poison rule including the originally-errored one. Policy retry does NOT apply within a held subgraph — the held mechanism is transactional, errors are aborts. A retry of held work requires a new acquirer dispatch (which would create a new claim_handle).

**Worked example (A→B, B→X, A→C, C→Y; A is acquirer of claim Q; B, C declare `Holds.From=A`):**

1. A dispatches, executor returns held=true. Claim_handle Q created in `active` state; `claim_holders` row for A. A transitions `running → held`. Cascade fires from A filtered to members of Q's subgraph = {A, B, C}. X (subscribes to B) and Y (subscribes to C) are non-members; X and Y do NOT see cascade.
2. B dispatches. Registers as co-holder of Q (`claim_holders` row for B). B runs with the held claim available, settles success. B's claim_holders row → `completed`. B's other claim participation: none. But Q is still `active`. B transitions `running → held`. Cascade fires from B filtered to subgraph members; X is non-member, cascade defers (no row inserted for X under the filter).
3. C: same path. C transitions to held. Y deferred.
4. `CheckAndFireResolution(Q)`: all holders settled (no `active` claim_holders rows, no `failed` either). Outcome = commit. Q → `committed`.
5. Walk Q's holders: A, B, C. For each:
   - A: only claim is Q (just committed). A held → fresh. Fires deferred cascade from A to non-members (Z would be cascaded if a node Z subscribed to A; none in this graph).
   - B: only claim is Q (committed). B held → fresh. Fires B's deferred cascade to non-members → X. X dispatches.
   - C: only claim is Q (committed). C held → fresh. Fires C's deferred cascade to non-members → Y. Y dispatches.

If step 4 had been abandon (e.g., B errored and `claim_holders` for B → `failed`): outcome = abandon. Q → `abandoned`. Each of A, B, C transitions `held → failed` via poison rule. Each fires `terminal/error/abandoned` to non-members. X and Y dispatch (if they subscribe to terminal/error/abandoned or terminal/error/*) with the rolled-back signal.

**Overlapping claim sets (A and B different claims, D inherits both):** A and B are independent acquirers; D declares `Holds.From=A` and `Holds.From=B`. A returns held (claim Q_A); cascade to members of Q_A's subgraph includes D. D dispatches, registers as co-holder of Q_A. Later B returns held (claim Q_B); cascade to members of Q_B's subgraph includes D. D dispatches a second time? No — D's prior run is still in flight (held). The cascade walker creates a new pending for D under rule (a); D's serialization self-gate prevents the new pending from dispatching while the prior is held. The new pending waits.

D's prior run (the one already in held) registered as co-holder of Q_A only (it dispatched before Q_B existed). When Q_A resolves (commit): walk Q_A's holders. D's portfolio = {Q_A committed}. D held → fresh. D fires its cascade to non-members. The pending D' (waiting on serialization gate) can now dispatch — it acquires its own co-holder slot for Q_B (Q_B still active), runs, transitions to held, eventually transitions per Q_B's resolution.

If Q_A and Q_B had been concurrent in a way that D acquired BOTH co-holder slots in a single dispatch (e.g. both claims active before D dispatched): D's portfolio at settle = {Q_A active, Q_B active} → D held. When Q_A resolves: D's portfolio = {Q_A resolved, Q_B still active}. D stays held. When Q_B resolves: D's portfolio fully resolved. If Q_A committed and Q_B committed → D held → fresh. If Q_A committed and Q_B abandoned → D held → failed (poison). D fires its cascade with the appropriate signal.

**Async-callback note.** Per `concept:parked-state`, the await-async-callback outcome keeps the node-run in `running` state (it is NOT a park) for the duration of the callback wait. Under the seven-state model, async-wait nodes inherit running-state in-flight protection: cascade events targeting them queue new pendings per the walker rule, just as for any other running node-run. No special case.

### Wait-set semantics

Wait-set schema: `(frame, receiver_run, sender_run, topic_kind, subscription_scope, drained_at)`. Wait-set rows are inserted at cascade-walk time, keyed by the receiver_run that owns them (either the latest pending Ri being accumulated into per rule (a), OR a newly-created R(i+1)). Drain happens via `drainWaitSetOnSettled` when the sender settles. Non-cascade rows have no wait-set rows.

Each cascade-driven receiver run Ri carries its OWN wait-set rows for the cascade events that contributed to it. There is no row sharing across runs: D' has its rows, D'' has its (different) rows. Both follow rule (a) during their pending phase.

The cascade-driven dispatch-eligibility predicate ("no undrained wait-set rows + no in-flight subscribed upstream") lives in the gate evaluator's pending→stale gate check. For cascade-driven rows, state `stale` means input bag built and upstream cleared.

The serialization gate ("no other run for same (node, run-scope) in `running`, `held`, `parked`") lives at dispatcher claim time. Multiple stales can coexist (cascade-driven + non-cascade); the dispatcher serializes via sequence-ordered claim with a CAS on the chosen stale. `stale` is NOT in the serialization gate's exclusion set — that's the whole point of allowing the queue to exist.

Multiple invalidation cycles of an upstream produce new sender_runs and therefore new wait-set rows naturally — the wait-set handles "multiple sender_runs per receiver" since rows are keyed by sender_run, not sender_node.

### Eligibility-as-state observability

State `pending` explicitly says "cascade-driven, waiting on upstream cascades to drain." State `stale` explicitly says "input bag built; upstream dependency cleared; waiting only for the dispatcher's serialization gate (no in-flight for same (node, run-scope)) and a free dispatcher slot." Two stales for same (node, run-scope) are possible (cascade + non-cascade queued); an operator inspecting "why isn't the second stale dispatching?" sees the first stale or in-flight run via the same row, ordered by `sequence`.

The upstream-dependency check is at the gate evaluator and runs once at pending→stale transition; the dispatcher's SELECT does not evaluate it. The dispatcher SELECT matches `state = 'stale' AND no current claim`, with the serialization gate enforced as a CAS check at claim attempt. Multiple stales may match the SELECT; sequence-order and the serialization-CAS together select the single winner per (node, run-scope). The operator's "why isn't this dispatching?" reduces to two answers: another run for same (node, run-scope) is in (running, held, parked), or all dispatcher workers are busy. The upstream-dependency question never reaches the operator — it's already answered by the row being in `stale` at all.

### Node-row surface: no derived state, only summary

`rimsky_nodes` carries node identity and node-level error-policy state — `id`, `instance_id`, `node_type`, `executor`, `current_error_class`, `retry_counter`, `action_index`, `frame_id`, `tags`, `cascade_mode`, `created_at`, `updated_at`. It does NOT carry a derived "current state" field synthesized from its runs. All execution state for a node lives in `rimsky_node_runs`, queried by run id.

Runtime callers that need to know the state of a specific run (error-policy evaluation, retry decisions, cascade-recalculate gates, async-callback drive-terminal) query the run directly — by the run id they already have (`acq.DispatchID`), or via `Queue.GetInFlightRunForNode(node, run_scope)` when they need the currently-in-flight run for a (node, scope). No code path conflates "node state" with "the state of a particular run of that node."

Operator-facing summaries (HTTP `/nodes/{id}`, cascade-graph observability views) return a `NodeRunSummary` — categorical counts of node-runs by state class:

- `ActiveCount` — runs in `running`, `held`, `parked`
- `PendingCount` — runs in `pending`, `stale`
- `FreshCount` — runs in `fresh`
- `FailedCount` — runs in `failed`

Dashboards drill into `/nodes/{id}/runs` for per-run detail (with `run_scope_id`, `sequence`, `creation_reason`, `state`, `settling_signal_type`, etc). The summary carries no state-machine-shaped node state; the operator-facing presentation decides how to display "this node has K active runs across M scopes" rather than the persistence layer picking one run's state to call the node's.

This collapses the modeling ambiguity for fan-out nodes (where parent and N children share `node_id`): all runs are visible in the summary; the operator chooses how to interpret them. No lateral-join tiebreaker between parent and child run rows.

### Design artifact plan

The design layer under `.ok-planner/design/` is kept in sync with implementation. Each story anchors a scenario test via `// @story:` annotation; each decision records a non-obvious pivot via `// @decision:` annotation; each concept doc is the canonical statement of the model the code enforces.

**Stories** (`.ok-planner/design/stories/`):

- `story:resume-preserves-snapshot` — node-run input bag is preserved across park-resume.
- `story:cascade-defers-during-flight` — node in running/held/parked is not interrupted by upstream cascades; cascade queues a new node-run that dispatches after the current settles.
- `story:held-commit-cascades-success` — non-member downstream subscribers see `terminal/success` only when held work commits.
- `story:held-abandon-cascades-abandoned` — non-member downstream subscribers see `terminal/error/abandoned` when held work rolls back.
- `story:operator-invalidate-queues-during-flight` — operator-invalidate while a run is in-flight produces a stale row that dispatches after the in-flight run settles, with carry-forward bag at invalidate-moment.
- `story:most-recent-coalesces-cascades` — default mode coalesces multiple cascade rounds during a single in-flight period into one post-settle dispatch with the latest bag.
- `story:sequenced-preserves-cascade-rounds` — opt-in mode dispatches M times for M cascade rounds, each with its own bag.
- `story:idempotent-mode-dedupes` — opt-in modes (both variants) drop re-runs whose input bag JCS-equals the predecessor's.

**Decisions** (`.ok-planner/design/decisions/`):

- `decision:walker-rule-per-sender-node` — rule (a) over rule (b); makes sequenced mode meaningful at the right granularity.
- `decision:non-cascade-direct-to-stale` — skip pending entirely for operator/retry/reenqueue; immune to mode rules.
- `decision:held-as-state-not-phase` — held is a node-run state with subgraph-scoped cascade defer.
- `decision:terminal-error-abandoned-as-error-class` — `terminal/error/abandoned` over a new `terminal/abandoned` root signal.
- `decision:mode-default-most-recent` — default coalescing over sequenced or strict-once.

**Concept docs** (`.ok-planner/design/concepts/`):

- `concept:node` — identity + node-level error-policy state. No derived run-state field; runtime queries node-runs directly; operator-facing summary is a `NodeRunSummary` of categorical run counts.
- `concept:cascade` — in-flight runs are sealed; cascade creates new pending. Per-sender-node walker rule.
- `concept:node-run` — seven-state machine. `sequence` and `creation_reason` columns. Transitions.
- `concept:parked-state` — every dispatch loads persisted bag. Time-wake transitions parked→stale.
- `concept:auto-terminal` — each subgraph member that executed broadcasts its own deferred terminal cascade at commit/abandon.
- `concept:claim-handle` — held is a node-run state. Co-holder vs arms-length distinction at cascade time. Overlapping claim sets defer until last-resolved.
- `concept:wait-set` — gate-evaluator semantics. Multiple pendings per (node, run-scope). Per-sender-node accumulation.
- `concept:signal` — `terminal/error/abandoned` in the canonical taxonomy.
- `concept:frame` — whether `pending` counts as in-flight for frame-end purposes.

## Scope

- **In scope:** cascade walker, dispatcher, state machine, wait-set, held-subgraph cascade defer, mode rules, gate evaluator.
- **Out of scope:** frame model, claim-handle acquisition mechanics, executor protocols, message-delivery wire shape.
- **Wait-set role:** the wait-set is the data structure for tracking cross-run dependencies. It is consulted at drain time by the gate evaluator for the upstream-dependency predicate. The serialization gate ("no other run for same (node, run-scope) in `running` / `held` / `parked`") is enforced separately at the dispatcher's claim attempt.

## Resolution log

Items raised during inline review and resolved through subsequent discussion. Audit trail of design pivots and implementation milestones.

### Gaps resolved

**Gap 1: Walker rule consistency.** ✅ RESOLVED. Rule (a) — per-sender-node accumulation gate — confirmed and inlined into the cascade walker section. The pseudo-code and the three-case description both encode (a). Multiple pendings can coexist; the latest is the accumulation target; the self-gate serializes their stale/dispatch.

**Gap 2: Non-cascade re-run paths lack a drain-event trigger.** ✅ RESOLVED via direct-to-stale. Non-cascade paths (operator-invalidate, policy-retry, infra-reenqueue) skip pending entirely and create rows directly in state `stale` with the carry-forward bag built at creation moment. Two supporting fields added to `rimsky_node_runs`: `sequence` (monotonic per (node, run-scope, frame), drives dispatcher claim order) and `creation_reason` (`cascade | operator_invalidate | policy_retry | infra_reenqueue`). Walker rule (a) targets only the latest pending — non-cascade stales aren't accumulation targets by definition, so no carve-out needed. The serialization gate moves from the pending→stale transition to the dispatcher's claim site; multiple stales (cascade + non-cascade) coexist, dispatcher claims by sequence. Mode rules scoped to `creation_reason = cascade` only — non-cascade runs are immune to most-recent's delete and to idempotent variants' dedupe.

**Gap 3: Queue depth claims depend on which walker rule applies.** ✅ RESOLVED via Gap 1 confirmation. Under (a) the bounds hold as stated: pending count unbounded above by re-cascade volume; in-flight ≤ 1 via self-gate; stale ≤ 1 under `most-recent` (delete-on-transition), unbounded under `sequenced` and idempotent variants.

**Gap 4: Held-cascade defer breaks the inheritor-dispatch mechanism.** ✅ RESOLVED via subgraph-scoped defer. The held subgraph's commit/abandon decision is made by `CheckAndFireResolution` after every expected co-holder settles — co-holders MUST execute during the held period. Cascade fires from held nodes filtered to subgraph members only, so co-holders dispatch; cascade to non-members defers until auto-terminal commit/abandon, when every member that executed broadcasts its own deferred cascade. Gate evaluator skips held upstreams that the receiver inherits from. Overlapping claim sets defer until the last participating claim resolves; any abandon poisons the cascade signal to `terminal/error/abandoned`.

### Smaller items addressed

- **Extra-root diamond example** (A→B, X→B, A→C, B→D, C→D): ✅ ADDED. Inline worked walk-through in the cascade walker section. Demonstrates accumulation across multiple roots and contrasts with the same-upstream re-cascade case that triggers a new pending under rule (a).
- **Async-callback semantics**: ✅ ADDED. Inline note in the held-as-state section confirming async-wait nodes stay `running` and inherit running-state in-flight protection without a special case.
- **SELECT-time vs drain-time eligibility distinction**: ✅ ADDED. Inline paragraph in the eligibility-as-state observability section makes the split explicit: upstream-dependency moves to gate-eval; `state = stale AND no claim` stays at dispatcher SELECT; serialization gate enforced as CAS at claim attempt.
- **Existing partial index `idx_node_runs_pending_idx`**: ✅ ADDRESSED. Migrated from `WHERE phase = 'pending'` to `WHERE state = 'stale'`. Same dispatcher-perf role, different column.
- **Multi-row-same-sender-node bag-build**: ✅ RESOLVED via Gap 1. Under rule (a), at most one wait-set row per sender_node per pending; overlay is unambiguous, no tie-breaker needed.
- **Held-subgraph worked example** (A→B, B→X, A→C, C→Y; A acquirer, B+C co-holders): ✅ ADDED. Inline walk-through in the held-as-state section. Demonstrates commit-time and abandon-time broadcast from each member that executed.

### Implementation milestones

- **Schema collapse to a single `001-initial.sql` per backend.** ✅ COMPLETE. The seven-value state CHECK, `sequence` and `creation_reason` columns, the `dispatch_input_bag` sidecar on `rimsky_node_attributes` (sealed/unsealed distinction retired — every dispatchable run carries a bag, populated at creation), and the migrated dispatcher-perf partial index are all in the initial migration. Pre-v1 break-freely applied; the prior incremental migration sequence is retired.
- **State machine in `lib/foundation/cascade/state.go`.** ✅ COMPLETE. Seven states (pending, stale, running, held, parked, fresh, failed); transition table matches the sketch; `NodeStateResuming` + the standalone deadline-resume state retired.
- **Cascade walker rule (a).** ✅ COMPLETE. `lib/runtime/cascade_walker.go::ensureCascadePending` implements per-sender-node accumulation with advisory locking per (node, scope, frame). The retired `MarkStaleForCascade` and the in-walker parked-wake path are gone from the cascade call sites.
- **Gate evaluator.** ✅ COMPLETE. `lib/runtime/gate_evaluator.go` evaluates gates at drain-time, builds the bag (carry-forward + cascade-overlay), applies the mode rule, transitions pending→stale.
- **Non-cascade direct-to-stale.** ✅ COMPLETE. Operator-invalidate, policy_retry, and infra_reenqueue all use `CreateNonCascadeStale` with `creation_reason` set accordingly. Old run transitions to `failed`; new stale row carries forward the bag.
- **Per-template-node `cascade_mode` config.** ✅ COMPLETE. Field on `TemplateNodeDef`; read via `Nodes().GetCascadeMode`; defaults to `most-recent`.
- **Dispatcher claim path.** ⏳ PARTIAL. Serialization gate moved to claim-time and sequence-ordered (✅). `resolveAttributes` + `upsertAttributesPreDispatch` still execute on the dispatch hot path and overwrite the gate-evaluator-built bag (⏳ — needs replacement with a load-by-id helper so the gate-eval bag IS the dispatch bag).
- **Held cascade defer (subgraph-scoped, per-holder).** ⏳ OUTSTANDING. Implementation target: `held` covers acquirer + any co-holder while any of the run's claim_handles remain `active`; cascade from a held node is filtered to the union of subgraphs the sender participates in; `CheckAndFireResolution` walks all holders per claim resolution and transitions each per its full claim portfolio (poison rule). Each transition fires that holder's own deferred cascade. Mechanism uses `rimsky_claim_holders` + `rimsky_claim_handles` directly; no new persistence column.
- **NodeRow surface refactor.** ⏳ OUTSTANDING. Drop derived `State`, `SettlingSignalType`, `AssignedSupervisorID`, `InFlightRunID`, `RunScopeID` from `NodeRow`. Collapse `nodeSelect` to a plain `rimsky_nodes` column SELECT (no LATERAL JOIN, no scope-priority heuristic). Runtime callers (`on_error.go`, `runner_error_policy.go`, `cascade_recalculate.go`) read run state directly by run id. Operator-facing HTTP `/nodes/{id}` and cascade-graph observability return a `NodeRunSummary` of categorical run counts. Scenario test helper `WaitForNodeState` and similar are rewritten to poll runs rather than the derived node state.
- **Already-shipped collapse.** ✅ COMPLETE. `NodeStateResuming`, the prior standalone deadline-resume mechanism, `IsResume`, `loadResumeAttributes`, and the corresponding migration are subsumed; the bag-load mechanism is general (one path for every dispatch).
- **Idempotency mode JCS canonicalization.** ⏳ PARTIAL. Idempotent-mode comparators are wired but currently use raw `json.Marshal` instead of RFC 8785 JCS via the existing `canonical.CanonicalSpecHash` helper.
- **Workaround sweep.** ⏳ OUTSTANDING. `SweepReady` stub, unused `ReasonHandlerError`, three reason-input self-loops on `state.go`, `subgraph_internal_cascade_fired` self-loop, `RevertRunningToStaleIfOrphaned` (compensating for premature state transition inside `ClaimDispatchRow`), the `claimed_by IS NOT NULL` carveout in the serialization-gate unique index, the `IsFanOutNode` gate on `fanoutRecalculate`, the `ListInFlightRunPhases` legacy name, the `wakeParkedReceiverIfPresentInTx` message-virtual special case, and the drained-only filter on `ListSenderNodesForReceiver` are all known workaround or legacy residue identified in code review and remain to be removed. (The `nodeSelect` lateral-join scope-priority heuristic vanishes as part of the NodeRow surface refactor, not this sweep.)
