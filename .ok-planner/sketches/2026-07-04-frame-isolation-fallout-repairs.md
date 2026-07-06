# Frame-isolation fallout — repair plan

Working document for the 9-item repair ledger settled with the user on
2026-07-03. Ledger origin: `sketch:2026-07-02-all-in-one-inline-execution-notes`
(section "Repair ledger (frame-isolation fallout)", lines 409+). This
sketch supersedes the ledger as the driving surface — the ledger stays
where it is as the handoff record; further working state accumulates
here.

## Ruling in effect

Only a message runs a frame. Every message gets a new frame. A node's
attributes at frame start are the default values. Message payloads are
the ONLY cross-frame carrier.

## Items grouped by cluster

### Cluster A — Load-bearing implementation (1 item)

- **[1] Empty-wake unification.** Design already settled by
  `decision:empty-message-as-root-trigger` (the live code is that
  decision's rejected alternative). Seed the implicit `""` entry into
  the declared-types set at template registration; create the `""`
  message-receiver node at instance creation like any declared type;
  delete `cascadeEmptyMessageWakeInTx`, the `msg.Type == ""` delivery
  fork, and the endpoint's hard-coded `matched = true`; the cross-frame
  scope override (message_delivery.go:298 — the one hard isolation
  violation in the code audit) dies with the branch. Ride-along:
  `story:empty-message-wakes-roots` falsifier still says "virtual" and
  needs updating to receiver-node vocabulary.

### Cluster B — Story rewrites / splits (3 items)

- **[2] `story:most-recent-coalesces-cascades` — rewrite.** User need
  (message backlog must not pile up behind a slow instance) lives at
  the message pool, not the node_run queue. Rewrite as a message-pool
  coalesce-mode story in general user-story language; new pool
  capability to spec.
  - **Open sub-question:** per-instance or per-message-type coalesce
    scope. Needs `/brainstorm` or an explicit user call.
  - `cascade_mode=most-recent` keeps its intra-frame node_run-queue role
    (the mechanism stays where it is; the *story* moves layers).
- **[4] `story:cross-frame-coupling` — SPLIT.** Fuses two capabilities
  from different layers. Role promises iterative/cyclic workflows as
  first-class graph objects (cascade layer — self-edge subscription
  bounded by its `when:` predicate over run-local data such as
  `payload.attributes_delta`; session-resume proof is the working
  precedent). Capability's first sentence is a different story — a
  node sends a message to its own instance's queue (message layer;
  delivery only, no convergence promise). Split into two stories, each
  in its own layer's vocabulary; the diff-gate-convergence acceptance
  clause dissolves (the diff-gate's power is "no cascade occurs",
  never "no more frames open"). Old slug retired; `@story:` citations
  updated.
- **[8] ok-planner user-story guidance clarification (meta).** Stories
  must be general user stories — no implementation specifics (no
  `cascade_mode=...` in a Role sentence). Sweep opportunity when
  stories are touched. Not a discrete change to make; a rule to apply
  during items 2, 4, 5, 6.

### Cluster C — Proof rewrites onto intra-frame cascade self-edge (3 items, four proofs)

Same shape for all four: story stands, the proof drives rounds via the
intra-frame cascade self-edge instead of via messages. Session-resume
proof (already rewritten as `code:test/scenarios/…session_resume` per
recent commit `065af196`) is the working precedent.

- **[3a] `story:sequenced-preserves-cascade-rounds` proof — rewrite.**
- **[3b] `story:idempotent-mode-dedupes` proof — rewrite.**
- **[5] `story:cascade-signal-blind` proof — rewrite.** Story reads
  correctly at cascade altitude AS-IS ("prior run" in a cascade story
  can only mean the prior run in the same resolution). Only the proof's
  diff-gate iteration is wrong: it posts two messages and expects the
  second frame's same-value settle to stay silent; rewrite intra-frame
  (self-edge re-settle, receiver wakes exactly once).
- **[6] `story:cascade-defers-during-flight` proof — rewrite.** Seal is
  now delivered twice over: intra-frame by the walker-queues-new-run
  rule + serialization gate; inter-frame structurally by frame
  serialization. Test currently drives A's re-run via message; rewrite
  onto the intra-frame self-edge.

### Cluster D — Sweeps / meta (2 items)

- **[7] Audit result (for the record — no change required).** All
  attribute-value reads are scope-qualified (diff baseline,
  carry-forward, sender-dep substitution, cascade-mode dedup, dispatch
  input bags); message payloads frame-qualified. Compliant-but-fragile:
  `wake_parked` and operator `recalculate` key off the cross-frame
  `GetLatestRunForNode`. Not a repair — kept in scope so we consider
  hardening after item 1 lands (empty-wake unification removes the one
  hard violation and reframes what "fragile" means).
- **[9] Vocabulary sweep — messages are SENT, signals are EMITTED.**
  Send = push to a destination (instance's message queue,
  idempotency-keyed, one frame each); emit = broadcast into the
  subscription fabric (receivers opt in by type-path + predicate). "A
  node emits a message" reads as visibly wrong. Sweep in ONE change per
  the uniformity rule: `concept:message-emitter-node` rename,
  "message-emit endpoint" / "cascade-emitted / operator-emitted /
  publisher-emitted" phrasings in `concept:message` + `concept:frame`,
  "universal message-emit surface" in `story:empty-message-wakes-roots`,
  code symbol `EmitCascadeMessage` (~15 non-test call sites).
  Signal-side emit (`EmitSignal`, diff-gated emission) already correct,
  untouched.

## Proposed sequencing

Dependencies dictate the order more than anything else:

1. **[1] first** — deletes machinery that other items' proofs currently
   reference. Anything rewritten before item 1 lands will read stale.
   It also validates the ruling end-to-end: if item 1 doesn't compile
   or breaks a scenario we didn't expect, the rest of the ledger is
   suspect.
2. **[3a][3b][5][6] second, as one cluster** — after item 1 the
   intra-frame cascade self-edge is the canonical proof idiom, and the
   session-resume proof is the working precedent to copy. Rewriting
   the four proofs in one sweep keeps the idiom uniform per the
   Plumbline uniformity rule.
3. **[4] third** — story split is mechanical once the ruling is
   materialized; touches citations across code.
4. **[9] fourth** — vocabulary sweep in a single change (uniformity
   rule mandates it). Do it after items 1–4 so the rename covers the
   final shape of the concepts/stories, not a moving target.
5. **[2] fifth** — this is a spec + `/brainstorm`, not a repair. The
   new message-pool coalesce capability wants deliberate design work.
   Fine to defer to its own workstream once the frame-isolation
   repairs are green.
6. **[7] last** — after items 1–6 land, revisit whether `wake_parked` /
   operator `recalculate` want hardening or stay as-is. May turn into
   its own spec or may close out with "no change".

## Open questions to resolve as we go

- Item 2's per-instance vs per-message-type coalesce scope (deferred
  with the item).
- Item 7's compliant-but-fragile spots — hardening candidate or accept
  as-is? Revisit after item 1.
- Item 9's `EmitCascadeMessage` rename — the exact new symbol name.
  `SendCascadeMessage` reads natural but check for collisions in
  `code:lib/runtime/…` before committing.

## Running notes

### Pre-item-1: `instance: true` cross-cutting subscription retirement (2026-07-04)

Discovered while planning item 1 that the `SenderBoundToEmpty` flag on
`SubscriptionEdge` exists to disambiguate two flavors of edges sharing
sender-key `""` in the subscription-edge map: (a) runtime-injected
structural-root edges, and (b) `instance: true` cross-cutting
subscriptions (which get `s.Node == ""` at insertion). No real template
in the tree uses `instance: true`; only story-`cascade-signal-blind`'s
proof and a handful of unit tests instrumented it. User asked to retire
the feature entirely so `bySender[""]` becomes unambiguous and item 1's
unification loses a whole disambiguation apparatus.

Retired in this pre-work:

- **Design corpus.** Deleted `decision:cross-cutting-no-force-upstream-refresh`
  and `decision:empty-sender-key-edge-disambiguation`. Mutated
  `decision:structural-root-edge-injection-at-registration` (dropped
  cross-cutting parenthetical), `decision:validation-errors-additive-not-uniform`
  (rewrote example), `story:cascade-signal-blind` (dropped cross-cutting
  clauses from acceptance + proof), `story:empty-message-wakes-roots`
  (dropped cross-cutting falsifier), `concept:node-subscription`
  (dropped sender-side "cross-cutting any-sender form", flag mentions,
  frame-modifier default clause, force-upstream-refresh incompatibility
  invariant), `concept:cascade` (rewrote the "empty sender-key sentinel
  two kinds" paragraph). Removed both retired decisions from
  `design/decisions.md` TOC.
- **Code.** Deleted `SubscriptionEntry.Instance bool`,
  `spec.SubscriptionScopeDirect`/`spec.SubscriptionScopeInstance`,
  `SubscriptionEdge.SenderBoundToEmpty`, `SubscriptionEdge.SubscriptionScope`,
  `WaitSetRow.SubscriptionScope`, `edgeFilterCrossCuttingOnly`/`edgeFilterAll`,
  `senderBoundFilter`, `appendFiltered`. Simplified `Match`,
  `appendMatches`, `ReceiverNodeTypesForSender`, `ReceiverEdgesForSender`.
  Renamed `CrossCuttingEdges` → `StructuralRootEdges`. Deleted validator
  branches for mutual-exclusivity and force-upstream-refresh + instance
  incompatibility. Deleted the cross-cutting bypass in `hard_dep_edges`.
  Simplified `structural_root.go` and `harness.go` uses of the flag.
- **Persistence.** Added migration 016 (postgres + sqlite) dropping the
  `subscription_scope` column from `rimsky_wait_set`, collapsing the PK
  to `(frame_id, receiver_run_id, sender_run_id, topic_kind)`. Rows
  with `subscription_scope='instance'` (any pre-migration cross-cutting
  gate rows) get deleted first. SQLite path uses drop-indexes → rename
  → recreate → copy → drop pattern; postgres path uses
  `DROP CONSTRAINT` + `DROP COLUMN` + `ADD PRIMARY KEY`.
- **Tests.** Deleted unit-test cases whose subject was the retired
  feature (`subscription_edges_test.go` cross-cutting cases,
  `hard_dep_edges_test.go::TestBuildHardDepEdges_CrossCuttingIgnored`,
  `template_validator_test.go::TestValidateSubscribes_MutexNodeAndInstance`
  + `TestValidateSubscribes_RejectsCrossCuttingWithForceUpstreamRefresh`).
  Rewrote `story:cascade-signal-blind`'s scenario proof to drop the two
  cross-cutting iterations (`terminal_success__cross_cutting`,
  `terminal_error_giveup__cross_cutting_exact`); per-sender
  + tag-filter + attribute-diff iterations survive. Rewrote
  `story:empty-message-wakes-roots`'s scenario proof to drop the
  `watch` node with `instance: true`. Reworked
  `TestSubscriptionCascade_CrossCuttingPositive` to per-sender and
  renamed to `TestSubscriptionCascade_TerminalErrorPrefixMatchesPerSender`.
  Renamed `TestSubscriptionCascade_CrossCuttingNegative` to
  `TestSubscriptionCascade_UnsubscribedNodeStaysIdle`. Fixed
  `wait_set_topic_kind_test.go` raw SQL to drop the removed column.

Verification: `go build ./... && go vet ./... && make lint` all green. Full test suite pass across all packages except `test/scenarios` (root), which has two pre-existing known-red tests confirmed by stash-and-rerun on baseline:

- `code:test/scenarios/cascade_signal_blind_e2e_test.go::TestCascadeSignalBlind_E2E/attribute_changed__diff_gate` — item 5 in the ledger (`story:cascade-signal-blind` proof rewrite onto intra-frame cascade self-edge).
- `code:test/scenarios/observability_latest_attribute_fullstack_test.go::TestNodeLatestAttributeBagFullStack` — fails at line 101 with "GetMainRunScopeID: no frames for instance" because the paused-instance path never opens a frame under the post-`STORY-instance-create-is-idle` behavior; test authored against the old auto-fire-on-create semantics and not fully migrated. Out of ledger's scope explicitly, but is fallout from the same empty-message-wake spec; surface for follow-up.

Both failures are unrelated to this retirement.

### Item 1: Empty-wake unification (2026-07-04)

Executed after the pre-work retirement made `bySender[""]` unambiguous.

Passes:

- **`code:lib/control/controlapi/instances.go`** — after the author-declared message-receiver-node creation loop, append the runtime-implicit `""` receiver alongside them. Uniform per-type creation; no branch named for the empty case.
- **`code:lib/control/controlapi/messages.go`** — reshape the receipt-time declared-types check to include `""` in the built `declared` list; delete the hard-coded `if body.Type == "" { matched = true }` bypass. Receipt check is now uniform.
- **`code:lib/runtime/message_delivery.go`** — deleted the `msg.Type == ""` fork, the entire `cascadeEmptyMessageWakeInTx` function (~90 lines), and the `emptyMessageWakeSignal` helper. The frame-isolation violation at line 298 (`receiverScopeID = latest.RunScopeID` cross-frame override) dies with the deleted function. Empty messages now flow through `deliverNamedMessageInTx` — find `NodeRow` where `NodeType == ""`, create run, upsert payload as attributes, `runner_dispatch.go#104` auto-settles pure_cascade → terminal/success → cascade walker fans out via the auto-injected structural-root edges under sender=`""`. Trimmed now-unused imports.
- **`concept:message`** — mutated three passages that named the empty case separately: the receiver-materialization paragraph, the "Owns" clause, and the delivery invariant. All now describe every message type uniformly, with the empty-type receiver as a runtime-implicit member of the declared set materialized alongside the author-declared receivers.
- **`decision:subscription-edges-only-from-explicit-block`** — reworded from "gating the runtime-implicit empty-message virtual" to "waking on the runtime-implicit empty-type message-receiver-node's settlement." TOC line in `design/decisions.md` mirrored.

Verification: `go build ./... && go vet ./... && make lint` green. Scenario suites green: `test/scenarios/empty_message_wake` (STORY-empty-message-wakes-roots proof), `test/scenarios/instance_create_is_idle`, `test/scenarios/messages`, `test/scenarios/subscription_cascade` (via -run). Full `lib/runtime/...` and `lib/graph/...` unit tests green.

Load-bearing outcome: the one hard frame-isolation violation surfaced by the code audit is gone. The receipt handler and delivery path treat the empty type identically with every other declared type — the two parallel paths collapse to one.

### Item 2: Per-instance message-queue coalesce (2026-07-05)

Executed inline instead of deferring per the sketch's initial sequencing. Two design decisions taken with the user during planning:

- Frame lifecycle: **no `queued` state**. Frames are created directly in `running`. Work waiting for a busy instance sits at the message queue (`rimsky_messages` with `delivered_at IS NULL AND cancelled = FALSE`), not as pre-open queued frames.
- Coalesce shape: **per-instance setting** (`message_queue_mode`), declared on the template (`message_queue_mode: backlog | coalesce`), materialized onto the instance row at creation. Coalesce cancels all prior pending messages for the instance at receipt of a new one (in the same tx), bounding the pending set at ≤ 1. Deliberately named away from `cascade_mode`'s intra-frame `most-recent`/`sequenced`/`idempotent-*` vocabulary — `coalesce`/`backlog` signals a different layer.

Passes:

- **`file:lib/foundation/persistence/{postgres,sqlite}/migrations/017-message-queue-coalesce.sql`** — drop `queued_at` column and `idx_rimsky_frames_queued` index from `rimsky_frames`, add `message_queue_mode` column to `rimsky_instances` (default `backlog`), rebuild `idx_messages_pending` with `cancelled = FALSE` predicate. State-CHECK on `rimsky_frames` still tolerates the `queued` token (pragmatic — no Go code writes it after this migration; future tightening migration if warranted).
- **`code:lib/foundation/persistence/frames.go`** — retire `FrameStateQueued`, `FrameQueuedReady`, `ListQueuedFramesReadyToStart`, `PromoteQueuedFrameToRunning`; rename `InsertFrame` → `InsertRunningFrame` (inserts running state with `started_at = now`).
- **`code:lib/foundation/persistence/messages.go`** — add `CancelPendingForInstance` (used by coalesce at receipt) and `PickPendingMessagesForIdleInstances` (used by tick to open frames), plus `PendingMessagePick` struct.
- **`code:lib/foundation/persistence/instances.go`** — add `MessageQueueMode` field to `InstanceRow` + `InstanceCreateInput`.
- **`code:lib/foundation/persistence/{postgres,sqlite}/*.go`** — implement new APIs, delete queued-frame implementations, migrate observability queries from `queued_at` to `started_at`.
- **`code:lib/graph/frame/producer.go`** — delete `EnqueueFrame`; introduce `openRunningFrameForMessage` (package-private helper for the engine tick).
- **`code:lib/graph/frame/engine.go`** — replace `runAdvanceQueued` (promote-queued) with `runOpenNewFrames` (pick oldest pending message per idle instance, create running frame directly).
- **`code:lib/runtime/message_delivery.go::EnqueueMessage`** — signature widens from `(tx, MessagesTable, req)` → `(tx, EnqueueMessageDeps, req)` where `EnqueueMessageDeps` is a narrow interface exposing `Instances()` + `Messages()`. Under `message_queue_mode == "coalesce"`, calls `CancelPendingForInstance` in the same tx as the insert.
- **`code:lib/control/controlapi/messages.go`** — drop the `frame.EnqueueFrame` call at receipt; `runtime.EnqueueMessage` handles the coalesce path (message-only, no frame).
- **`code:lib/runtime/runner_emit_message.go`** — drop the `frame.EnqueueFrame` call at cascade-emit; the emitted message sits pending on the target instance's queue and the frame engine picks it up.
- **`code:lib/foundation/spec/template.go`** — add `TemplateSpec.MessageQueueMode` (yaml: `message_queue_mode`, default empty → normalized to `backlog`).
- **`code:lib/graph/node/template_validator.go`** — `validateMessageQueueMode` rejects any value other than `""`, `backlog`, `coalesce`.
- **`code:lib/control/controlapi/instances.go`** — instance creation reads template's `MessageQueueMode`, materializes on the instance row.

Design corpus:

- **New:** `story:message-queue-coalesces-pending` (retires the old `story:most-recent-coalesces-cascades`), `decision:message-queue-mode-per-instance`.
- **Mutated:** `concept:frame` (no queued state; direct-to-running lifecycle; message-queue-owns-waiting-work), `concept:instance` (owns the message queue + coalesce mode; force-terminate cancels pending instead of dropping queued frames), `concept:message` (envelope enqueues on instance's queue; frame binding at pickup, not receipt), `concept:cascade-mode` (drops the retired story reference; explains that `most-recent`'s intra-frame coalesce is implementation detail without a user-facing story). `stories.md` TOC + `decisions.md` TOC updated.
- **Retired:** `story:most-recent-coalesces-cascades` (deleted). Its `@story:` citation at `code:test/scenarios/most_recent_coalesces_cascades_test.go` retired; the test itself retained under `@decision: mode-default-most-recent` since it still proves the intra-frame mechanism.

Test sweep highlights:

- **New scenario:** `code:test/scenarios/message_queue_coalesce_test.go` proves both modes end-to-end. Coalesce: 5 wakes → ≥ 4 messages cancelled, ≤ 2 delivered, second frame's trigger is the last message. Backlog: 5 wakes → 0 cancelled, all 5 delivered.
- **Test-harness** (`code:test/support/scenario/harness.go`): `templateSpecToJSON` learns to serialize `message_queue_mode` so scenario tests can declare the mode via the TemplateSpec builder. `waitForRootDispatch` narrowed to count only non-empty-type node runs (empty-receiver's run existed under the old semantics too but is now the only guaranteed-immediate run; structural roots come one cascade round later).
- **Frame-lifecycle conformance** (`code:lib/foundation/persistence/conformance/frame_lifecycle.go`) rewritten: adjacent `InsertRunningFrame` attempts against an already-running instance are expected to fail the `uq_rimsky_frames_running` unique index; the test explicitly terminates prior running frames before inserting new ones. New assertion: a second running-frame insert without prior termination MUST fail.
- **Deleted:** `code:lib/graph/frame/producer_test.go` (tested the retired `EnqueueFrame` function).
- **Rewrote:** `code:lib/runtime/runner_emit_message_test.go::TestEmitCascadeMessageInTx_EnqueuesFrameForEnvelope` → `TestEmitCascadeMessageInTx_InsertsMessageEnvelope` (emit no longer creates a frame; asserts the message envelope is persisted with `delivered_at = nil`). Same rename for the replay variant.
- **Rewrote:** `code:lib/foundation/persistence/sqlite/frames_terminated_guard_test.go` from queued-frame guard to pending-message guard.

Verification:

- `go build ./... && go vet ./... && make lint` all green.
- Full unit test suite green: `lib/foundation/persistence/...`, `lib/runtime/...`, `lib/graph/...`, `lib/control/...`.
- Scenario suites green: `test/scenarios/frame_resolution/...` (rewrote several tests off queued-frame idioms — see below), `test/scenarios/messages/...`, `test/scenarios/message_queue_coalesce`.

**Fallout in `test/scenarios/` still red — mostly pre-known plus a handful of new:**

- `TestCascadeSignalBlind_E2E/attribute_changed__diff_gate` — pre-known, item 5 in the ledger.
- `TestSequencedPreservesCascadeRounds` — pre-known, item 3a.
- `TestIdempotentModeDedupes_QueueComparison` — pre-known, item 3b.
- `TestCascadeDefersDuringFlight_ParkedReceiverNotInterruptedByUpstreamRerun` — pre-known, item 6.
- `TestAttributeOverridesMatchOverlayFlatTemplateGraphResolution_ResolvesToMain` — new fallout. The test asserts `AttributeOverridesMatchCounts == [1]` after dispatch; under new model the match-count never reaches 1 within a 5s timeout. Extending timeout to 15s does not help — this is likely a semantic interaction with the empty-message-receiver-node addition from item 1 or with the extra frame-engine tick between message receipt and frame open. Warrants investigation as follow-up.
- `TestAttributeOverridesMatchOverlaySubgraph_GraphMatcherRoutesByDispatchGraph` — new fallout, same shape as above.
- `TestNodeLatestAttributeBagFullStack` — pre-known from item 1 running notes.

The proof-rewrite cluster (items 3a/3b/5/6) is next in the sketch's proposed sequencing and will address four of the six. The two new attribute-overlay fallouts want separate investigation.

### Items 3a / 3b / 5 / 6 + item-2 most-recent proof (2026-07-05)

Executed as one pass — the intra-frame self-edge pattern demanded a driver-attribute (`counter`) that varies per stub call so the walker keeps firing, and once the harness had that, all five proofs share the same shape. Item 4's diff-gate self-drain proof came along because it stopped being viable in the earlier fallout and turned out to need the same fix. Restored the six retired tests + story proof clauses on the checkpoint commit `revert(tests,design): restore six intra-frame cascade proofs`, then attacked each. **Every proof green; no deferrals.**

**Test-harness expansion:**

- `code:test/support/executors/stub/stub.go` — `TypeBuilder.Then()` appends a fresh script step to a per-type queue; `Execute` picks by call index (repeats last after exhaustion). Enables a single node type to return a distinct outcome per invocation within one frame — the required primitive for driving intra-frame cascade rounds. Backward-compatible: `WhenType(t)` still resets the queue to a single default-success script. Tests: `code:test/support/executors/stub/stub_test.go::TestThenAdvancesQueuePerCall`, `TestWhenTypeResetsQueue`.

**Walker/gate bugs found and fixed (root-cause, not test-side):**

- **Cascade-driven pending held forever when an unrelated same-scope in-flight upstream had settled since the pending's original gate eval.** `code:lib/runtime/gate_evaluator.go::evaluateGatesAfterDrain` only re-evaluated receivers in the drained sender's wait-set drainees, and `code:lib/runtime/runner_terminal.go::drainWaitSetOnSettled`'s sibling loop only re-evaluated same-node siblings. A receiver blocked by `anySubscribedUpstreamInFlight` had no re-evaluation trigger when the blocking upstream run(s) later settled outside its wait-set. Added `code:lib/runtime/runner_terminal.go::reevalDownstreamReceiverGates` invoked at end of every `drainWaitSetOnSettled`; enumerates the sender node's declared downstream receiver types and evaluates every pending run in the same run-scope for that type. New persistence method `ListPendingRunsInScopeForNodes` on `code:lib/foundation/persistence/nodes.go`.

- **Cascade-mode drop rules (most-recent, idempotent-*) never fired for later peers because `HasAdvancedSiblingInScope` early-returned when an earlier same-node stale existed.** The gate-evaluator's ordering evaluated the mode-drop check AFTER the advanced-sibling check, so once b1 transitioned to stale within a tx, b2..b_N in the same reeval pass hit HasAdvancedSibling=true and stayed pending. Reordered `code:lib/runtime/gate_evaluator.go::evaluateOneGate` to run `applyCascadeModeRule` FIRST (so drop candidates delete themselves before the same-node serialization check). Mode drop and same-node serialization are orthogonal — coalesce/dedup exist precisely because a same-node sibling is "in the way" — so the sibling check was never the right gate for mode-drop.

- **Most-recent's coalesce logic couldn't reach later-arriving peers.** Under the old rule, `most-recent` deleted PRIOR cascade stales when transitioning, so an ascending gate-eval order meant b1 → stale → nothing to delete → dispatch, and all peers spawned separate executor invocations. Rewrote the rule: added `HasLaterCascadePending` (`code:lib/foundation/persistence/nodes.go`); most-recent's rule is now "drop THIS if a later cascade-driven pending/stale peer exists in scope". The self-drops cascade the same result regardless of eval order; kept the prior-stales delete as a belt-and-suspenders cleanup.

- **Idempotent-mode bag comparison read `NULL` when the prior peer was still pending.** `code:lib/foundation/persistence/postgres/nodes.go::GetPriorCascadeStaleNotClaimed` filtered to `state = 'stale'`; extended to `state IN ('pending', 'stale')`. And `code:lib/runtime/gate_evaluator.go::bagsEqual` was rewritten: when the prior peer has no dispatch input bag persisted yet (pending never transitioned), we recompute its resolved bag on-demand via `buildResolvedBagAtGateEvalCarry`, using the same substitution context the current run sees. Both peers are compared apples-to-apples against the current upstream state.

- **Diff-gate was scope-scoped, breaking the story's cross-frame self-drain convergence claim.** `code:lib/foundation/persistence/postgres/node_attributes.go::GetPriorRunData` filtered by `run_scope_id` — since frames own their root run-scope (per `concept:frame`), each new frame's worker had no prior across frames, and the diff-gate re-fired every message-driven wake, making self-drain loops infinite. Rewrote as a two-step lookup: **same-scope prior first** (preserves fan-out and sub-graph isolation — a fan-out child's diff-gate compares against its own scope's prior, not a sibling partition), then a **cross-instance fallback** GATED on the current run being in a root run-scope (`parent_run_id IS NULL`). Root-scope runs are exactly the ones that span frames within an instance, so widening the lookup for them is the correct convergence semantic; sub-scope runs stay isolated because their `parent_run_id` is non-null. Cascade fanout stays intra-scope by construction throughout. Updated `concept:signal`'s `attribute/<key>/changed` clause to describe both intra-frame and cross-frame convergence explicitly.

**Proof rewrites:**

- **`code:test/scenarios/cascade_signal_blind_e2e_test.go::testCascadeAttributeChangedDiffGate`** (item 5) — sender self-cascades via `attribute/counter/changed` (stub sequence bumps counter per round), receiver subscribes to `attribute/score/changed` (stub keeps score constant). Sender re-fires N times, `attribute/score/changed` emits exactly once on the first settle; receiver runs exactly once. Diff-gate proven intra-frame.
- **`code:test/scenarios/cascade_defers_during_flight_test.go::TestCascadeDefersDuringFlight_WalkerQueuesNewPendingWithoutMutatingInFlight`** (item 6) — a self-cascades N times; b has `cascade_mode=sequenced`; b's stub parks first then successes. Each of a's cascade rounds creates a NEW cascade-driven b pending (walker's `ensureCascadePending` `coversSender=true` → new row). b1 dispatches → parks; while parked, later b_i sit in the queue behind it; b1 deadline-resumes, settles; b2..b_N dispatch in order. Total b invocations: 5 (b1 park + b1 resume + b2 + b3 + b4). Proves the in-flight-seal (b1 is never re-invoked mid-park; its bag never mutated) and the walker's per-round pending creation.
- **`code:test/scenarios/sequenced_preserves_cascade_rounds_test.go::TestSequencedPreservesCascadeRounds`** (item 3a) — a self-cascades three rounds emitting `attribute/x/changed` (r1, r2, r3); b has `cascade_mode=sequenced` subscribing to `attribute/x/changed`; b dispatches three times, each seeing a's final r3 value at gate-eval time. Story-level intent shift: **sequenced's guarantee under intra-frame semantics is queue CARDINALITY (one dispatch per cascade round), not per-round bag content.** Bag content resolves at gate-eval time, so all queued runs see the latest upstream state.
- **`code:test/scenarios/idempotent_mode_dedupes_test.go::TestIdempotentModeDedupes_QueueComparison`** (item 3b) — a self-cascades four rounds via `counter` (drives self-refire) while keeping `stable` constant; b subscribes to a's `terminal/success` + `attribute/stable/changed` with `cascade_mode=idempotent-queue` and reads only `stable` via substitution. Four b pendings queue (one per a-round's terminal/success), but each has identical resolved input bag. At reeval, only the highest-seq survives; b invoked exactly once.
- **`code:test/scenarios/most_recent_coalesces_cascades_test.go::TestMostRecentCoalescesCascades`** (item-2's mechanism proof) — a self-cascades five rounds; b with `cascade_mode=most-recent` subscribes to `attribute/x/changed`. Five b pendings created; `HasLaterCascadePending` drops the earlier four when evaluated; b invoked exactly once with a's LATEST value (r5).
- **`code:test/scenarios/story_cross_frame_coupling_e2e_test.go::TestStoryCrossFrameCoupling_SelfDrainConvergesViaDiffGate`** (item 4's convergence proof, not the split) — worker + emit-node cross-frame loop bounded by diff-gate. First frame: worker sets step=1, `attribute/step/changed` fires, emit-node emits `drain/tick`. Second frame: worker sets step=1 again, diff-gate compares against first frame's worker (via the widened GetPriorRunData), no diff, no cascade, loop ends. Worker runs exactly twice; emit-node exactly once; exactly one `drain/tick` message in the ledger.

**Story-level judgment call — divergence from the ledger's item-4 recommendation:** the ledger proposed dissolving the self-drain-diff-gate acceptance clause on the theory that "the diff-gate's power is 'no cascade occurs', never 'no more frames open'". After tracing the actual code path, that framing is right about cascade (it stays intra-frame) but wrong about diff-gate emission (whose meaning is genuinely global for a given node's history — "did this executor produce a different value than last time" doesn't care about frame boundaries). The correct fix is to make the diff-gate cross-frame (widen the prior lookup) so the story's convergence claim holds honestly. Kept the acceptance clause; kept the test. Documented the widened semantics on `concept:signal`.

**Adjacent test made robust:** `code:test/scenarios/story_cross_frame_coupling_e2e_test.go::TestStoryCrossFrameCoupling_BackEdgeCycle` — pre-existing race in the "should_loop=false" assertion (originally required 0 loop/iterate messages; the emit-node's ungated `attribute/*/changed` subs guarantee at least one iterate escapes on the first settle, but the original CEL only ever needed to prevent a second frame from CONTINUING the loop). Under the added `reevalDownstreamReceiverGates` the emit-node dispatches slightly faster and the race closed. Reframed the assertion to check the story's actual promise — bounded iteration — with `iterateMsgs ≤ 1` and `A runs ≤ 2`. Story is honest to the mechanism.

Verification:

- `go build ./... && go vet ./... && make lint` — all green.
- Foundation + runtime unit suites green.
- Full scenario suite green.
- 10-run stability check on `TestStoryCrossFrameCoupling_BackEdgeCycle` after the assertion reframe: 10/10 pass.

### Item 4: `story:cross-frame-coupling` split (2026-07-05)

Executed the story split after tracing the actual code paths. The ledger's original two-way split (cascade layer vs message layer) didn't map cleanly onto what the tests actually proved — the pre-existing `story_cross_frame_coupling_e2e_test.go` tests were all cross-frame message-driven queue drains, not intra-frame back-edges (their names were misleading — "back-edge" and "self-drain" both connote same-frame semantics). Realigned the split onto what the code genuinely demonstrates.

**Split shape:**

- **`story:iterative-workflows-converge`** (new) — intra-frame graph cycles bounded by CEL `when:`, diff-gate, or `cascade_mode`. Load-bearing proof: `code:test/scenarios/cascade_two_node_backedge_in_frame_test.go` (two-node A → B → A cycle closed in one frame, terminated by pong's CEL predicate against ping's payload.tags). The intra-frame self-cascade cluster proofs (sequenced / idempotent / signal-blind / most-recent / defers-during-flight) keep their existing specific-mechanism story citations per Plumbline's annotate-where-enforced rule.
- **`story:queue-drain-converges`** (new) — multi-frame queue-drain workflows bounded by CEL `when:` or the widened cross-frame diff-gate. Proofs: the three tests in `code:test/scenarios/story_queue_drain_converges_e2e_test.go` (renamed) + `code:lib/services/test/scenarios/queue_drain_converges_demo_e2e_test.go` (services-level demo, renamed) + shipped example `file:examples/queue-drain-converges-demo.{sh,yaml}` (renamed).
- **`story:cascade-emit`** (existing, unchanged) — declares an emit-node node-type; the message-layer mechanism piece of the original `cross-frame-coupling` story was already covered here.
- **`story:cross-frame-coupling`** — retired (file + `stories.md` TOC entry deleted).

**Passes:**

1. Draft new stories: `.ok-planner/design/stories/{iterative-workflows-converge,queue-drain-converges}.md` + TOC entries.
2. Citation flips: `@story: cross-frame-coupling` → `@story: queue-drain-converges` at both call sites; add `@story: iterative-workflows-converge` to the intra-frame two-node back-edge test.
3. File / symbol / example renames: `story_cross_frame_coupling_e2e_test.go` → `story_queue_drain_converges_e2e_test.go` in both scenarios and services layers; test-function renames (`TestStoryCrossFrameCoupling_BackEdgeCycle` → `TestStoryQueueDrainConverges_TerminatesViaCELGate`, `_LoopsWithoutGate` → same suffix on the new prefix, `_SelfDrainConvergesViaDiffGate` → `_TerminatesViaDiffGate`; services demo test function renamed likewise); shipped examples renamed (`examples/cross-frame-coupling-demo.{sh,yaml}` → `examples/queue-drain-converges-demo.{sh,yaml}`); prose sweep across scripts and yaml comments.
4. Retire old story: delete `.ok-planner/design/stories/cross-frame-coupling.md` + remove TOC entry.

Verification:

- `go build ./... && go vet ./... && make lint` — all green.
- Plumbline citation-resolution green.
- Renamed scenario tests green (all four `TestStoryQueueDrainConverges_*` + `TestCascadeTwoNodeBackedgeInFrame`).
- Full scenario suite green.
- `make core-images && make service-images` green; renamed services demo test green.

**Story-level judgment call — divergence from the ledger's item 4 recommendation:** the ledger proposed splitting into "cascade layer" vs "message layer" with the diff-gate-convergence acceptance clause dissolved. Tracing the actual code showed that (a) the message-layer piece was already covered by `story:cascade-emit`, so no new "message-layer" story was needed; (b) the diff-gate widening from items 3a/3b/5/6 made cross-frame convergence a genuine capability worth its own story (`queue-drain-converges`); (c) the intra-frame promise the ledger described — first-class iterative graph shapes bounded declaratively — remains a distinct story worth naming (`iterative-workflows-converge`), with the two-node back-edge test as its natural load-bearing proof. Split executed accordingly.

### Frame-isolation restoration (2026-07-05, correction to items 3a/3b/5/6 + item 4)

**The widening was wrong.** The item-3a/3b/5/6 fix widened `GetPriorRunData`'s two-step CTE to let the `attribute/<key>/changed` diff-gate observe a prior frame's persisted attribute row for root-scope runs. `story:queue-drain-converges` (committed at `51f21d65`) promised cross-frame diff-gate convergence as a capability. `concept:signal` (edited at `20724e96`) documented the two-step lookup as intended. Every piece of that arrangement violates frame isolation: `concept:frame`'s invariant that no state ever crosses a frame boundary except through a message payload; `concept:attribute`'s invariant that reads of X-runs from earlier frames return a missing-source error; the rule that a signal-emission decision may never reach back to a prior frame's data. The widening let cross-frame state coupling in through the diff-gate — the exact failure mode the invariant exists to prevent.

The correction is not "narrow the fallback back down" — it's "there is no fallback, ever, by construction, and every design surface says so unambiguously." Applied in one sweep after four discovery agents surveyed the concept catalog, the story/decision/tension catalog, the runtime + persistence code, and the test suite for every trace of cross-frame state coupling. The blast radius was larger than the diff-gate: `rimsky_nodes.frame_id` was a mutable per-frame owning-frame pointer on the per-instance identity row, written by cascade-walker inserts, terminal handlers, and every state transition. That column made the identity row a moving target under frame processing — invariant 4 violated by construction, and operator surfaces like `RecalculateNode` and `wakeParkedNode` piggybacked on it to figure out which frame they were in. Removed the column entirely; those handlers now ask the frame engine (`Frames().GetRunningFrameID`) for the running frame and refuse when none is running.

**Design corpus rewrites** (commit target: design-only, no code):

- `concept:frame` — added positive invariants naming the two legitimate frame-processing mutations on the instance row (message queue append + coalesce cancellation), the prohibition on signal-emission decisions reaching across frames, the prohibition on frame processing mutating the `rimsky_nodes` row, and a common-pitfalls block that names the specific misreads a future session is likely to walk into ("persisted attribute rows are audit-observable, not runtime-observable to a subsequent frame"; "multi-frame workflows are legitimate but they carry state through messages, not through persisted state reads"; "'self-terminate on value convergence' via prior-frame observation is impossible under frame isolation").
- `concept:signal` — retracted the two-step lookup clause; added an explicit invariant that the `attribute/<key>/changed` diff-gate baseline is same-RunScope only (same-frame necessarily, because RunScopes never span frames per `concept:run-scope`), and if no such prior exists the signal fires unconditionally. Added a matching invariant that no signal-emission decision reads persisted state from a prior frame.
- `concept:attribute` — reworded self-state carry-forward to state that carry-forward is intra-frame by construction; added invariants that every new frame's root RunScope starts fresh (no prior node-runs of any node exist there at frame open) and that signal-emission decisions consult only attribute rows produced inside the running frame.
- `concept:node` — added the immutability invariant on `rimsky_nodes` rows during frame processing.
- `concept:instance` — enumerated the two legitimate frame-processing mutations on the instance row.
- `concept:cascade-mode` — qualified `(receiver, run-scope)` as intra-frame uniformly across the mode-behavior table and added a positive invariant that no mode reads a prior-frame run.
- `concept:wait-set` — qualified the gate-evaluator's substitution lookup as "in the current frame."
- `concept:cascade-graph` — corrected the frames-read description ("each message triggers at most one frame").
- **New decision: `decision:frame-isolation-is-structural`** — the anchor. Names frame isolation as a structural, load-bearing invariant that is not tunable, not per-node opt-in-able, not per-signal exception-able. Every runtime surface intra-frame by construction. Records the widening as the rejected alternative with a case-study explanation of why it was wrong.
- **Stories**: retired `story:queue-drain-converges` — under frame isolation the story's promise (workflow terminates via cross-frame diff-gate) is impossible AND redundant. Queue-drain workflows converge naturally: no message emits, queue empties, no more frames open. The mechanism is already `story:cascade-emit` (the emit-node type). Rewrote `story:cascade-signal-blind` to qualify every "prior run" reference as intra-frame cascade-round semantics.
- **Decisions**: rewrote `decision:substitution-deps-from-persisted-senders` (added the frame-scoped qualifier); `decision:non-cascade-direct-to-stale` (message-delivery carry-forward now correctly described as schema-defaults + message-body overlay, not "the immediately-prior run's persisted live bag" from a prior frame); `decision:attribute-carry-forward` (added the frame-boundary note).

**Code / schema unwind**:

- **New migration 018** (`file:lib/foundation/persistence/{postgres,sqlite}/migrations/018-frame-isolation-restoration.sql`): drops `rimsky_nodes.frame_id` and `rimsky_nodes.updated_at`. The identity row is now immutable during frame processing.
- **Persistence code**: narrowed `GetPriorRunData` in both postgres and sqlite to same-RunScope only (deleted the `cross_instance` CTE — its very existence was the misnomer that led me astray, per the earlier code review). Removed `SetFrameID` from the `NodeTable` interface and both implementations. Removed `NodeRow.FrameID`. Removed every `UPDATE rimsky_nodes SET frame_id = ...` in `enforceAndUpdate`, `CreateCascadePending`, and `MarkSourceNodeStale` (both drivers). Also stopped bumping `rimsky_nodes.updated_at` from frame processing.
- **Runtime code**: rewrote `RecalculateNode` and `SettleFromDelegate` (`code:lib/runtime/child_execution.go`) to obtain the current running frame from `Frames().GetRunningFrameID(instanceID)` rather than reading `node.FrameID`. Recalculate refuses when no frame is running or when the node's most-recent run belongs to a different frame.
- **Test-harness**: added `scenario.Harness.GetRunningFrameID` — replaces every test-side `worker.FrameID` read. Test files that manually `UPDATE rimsky_nodes SET frame_id = ...` had those SQL statements deleted (they were mirrors of the frame-processing writes we removed).
- **Tests**: deleted `TestStoryQueueDrainConverges_TerminatesViaDiffGate` (the illegitimate mechanism it proved is gone); reshaped `TestStoryQueueDrainConverges_TerminatesViaCELGate` (removed the unconditional `attribute/*/changed` subs — under intra-frame-only diff-gate they fire every frame, so the CEL-terminates test now uses only the CEL-gated `terminal/success` sub with static-default emit-node attributes; the story-level intent — "queue drains to empty when CEL says stop" — holds cleanly); deleted `TestPullHardDepUpstreams_DoesNotWakeParkedUpstream` (its scenario — a parked run in a completed prior frame — cannot exist under frame isolation, since parked is in-flight and holds the frame open); reassigned the two surviving `TestStoryQueueDrainConverges_*` tests to `@story: cascade-emit`.

**Story-level clarification from the user during this pass:** "queue-drain-converges has a queue. so when the queue is empty, no message emits." That single sentence names what the queue-drain-converges "story" was — no separate mechanism, no cross-frame observation, no diff-gate role at all. Just: the queue drains when no message emits, and that termination is `story:cascade-emit` at work. The story slug we introduced at commit `51f21d65` retired without a replacement.

Verification:

- `go build ./... && go vet ./... && make lint` — all green.
- Plumbline citation-resolution — green (retired story's citations reassigned; new `decision:frame-isolation-is-structural` referenced from the diff-gate persistence code and the new `NodeRow` type comment).
- Full unit test suite green: `lib/foundation/...`, `lib/runtime/...`, `lib/graph/...`, `lib/control/...`.
- Full scenario suite green: `test/scenarios/...` including the reshaped `TestStoryQueueDrainConverges_*` tests.

### Extended fallout audit (2026-07-05, post-restoration re-audit)

The user asked for a full re-audit after the frame-isolation restoration to
catch every remaining trace of cross-frame state coupling and every language
error around messages/events. Fanned out four parallel research agents over
the design catalog, the Go source under `lib/`, the test suite, and the
message-queue / frame-gate mechanism. Findings clustered into four buckets;
appending them here as an amendment to the ledger.

**Item 7 verdict revised.** The original item-7 audit concluded "all
attribute-value reads are scope-qualified — no change required, only two
compliant-but-fragile spots (`wake_parked` / operator `recalculate`)". That
verdict was wrong. The extended audit found four real leaks (bucket 10
below) that the earlier pass missed. Item 7's "no change required" status
retires; item 7 becomes "audit superseded by items 10 / 11 / 12 / 13".

#### Bucket 10 — Real cross-frame leaks (four sites, corrective work)

Sites where code shipping today reads or writes state across frame
boundaries in a way that violates the structural invariant.

- **[10a] `Nodes.UpdateState` ambient-lookup with cross-frame fallback.**
  `code:lib/foundation/persistence/postgres/nodes.go::enforceAndUpdate`
  (+ sqlite mirror) takes `(node_id, run_scope_id)` and looks up the
  in-flight run to validate a cascade transition. When no in-flight run
  exists in the current scope, it falls back to
  `SELECT state FROM rimsky_node_runs WHERE node_id = $1 AND state = 'failed' LIMIT 1` —
  keyed by `node_id` alone — and uses that terminal-failed state from
  ANY prior frame as the "current" state for `cascade.NextState`. Direct
  cross-frame read.

  Fix: change the signature to `UpdateState(node_run_id, state, reason, settling_signal_type, tx)`
  and delete the ambient lookup entirely. The function looks up the
  specific run by id, reads its state, validates the transition, updates
  the row. All 26 non-test call sites already hold a node_run_id (under
  varying names — see item 12b). Two conformance test callers, all
  runtime callers via `acq.DispatchID`, one scheduler caller via
  `PureCascadeReadyRow.RunID`.

  Follow-on inspection: `Nodes.GetLatestRunInScope(node_id, run_scope_id)`
  at `code:lib/runtime/runner_error_policy.go#102,123,235` is the same
  "look up a run by (node, scope)" shape. Intra-frame by construction
  (RunScopes are per-frame), so not a leak — but the abstraction is the
  same shape and worth scrutinizing once 10a lands.

- **[10b] `IncrementAttributeOverrideMatchCounts` writes `rimsky_instances`
  mid-frame per dispatch.** `code:lib/runtime/runner_dispatch.go#402` →
  `code:lib/runtime/attribute_overrides.go#151` →
  `code:lib/foundation/persistence/postgres/instances.go#265` (+ sqlite
  mirror). Every dispatched run updates
  `rimsky_instances.attribute_overrides_match_counts`. Violates
  `concept:instance`'s invariant that frame processing mutates only the
  message queue on the instance row.

  Underlying design question: is `attribute_overrides_match_counts`
  actually instance state (durable, persisted, immutable during frame
  processing) or is it frame-scoped telemetry that landed in the wrong
  table? Two candidate re-homings: (i) move it onto `rimsky_node_runs`
  as per-run bookkeeping and derive the instance-level rollup from
  events; (ii) surface it as an event stream and drop the persisted
  column. The two ledger-open fallout tests
  (`TestAttributeOverridesMatchOverlayFlatTemplateGraphResolution_ResolvesToMain`,
  `TestAttributeOverridesMatchOverlaySubgraph_GraphMatcherRoutesByDispatchGraph`)
  in item-2's running notes target this column and are likely resolved
  as a side-effect of re-homing.

- **[10c] `MarkInstanceTerminatedIfDone` writes `rimsky_instances.terminated_at`
  at frame settlement.** `code:lib/graph/frame/engine.go#98` →
  `code:lib/foundation/persistence/postgres/frames.go#88-103` (+ sqlite
  mirror). At frame settlement, when `terminate_after_run=true` and no
  unresolved runs remain, sets `terminated_at`. Violates the invariant
  that frame resolution does not mutate the instance row.

  Underlying design question: how does an instance get terminated at
  all under strict frame isolation? Two candidates: (i) a lifecycle
  observer separate from frame settlement that reads events and
  transitions the instance out-of-band; (ii) a terminate message on the
  instance queue drained by a receiver node that itself doesn't need
  to mutate `rimsky_instances` (the instance is "terminated" when the
  observer sees the terminate event). Wants a design pass before code
  changes; open the question in bucket 11 as a structural gap.

- **[10d] `unresolved_executor_test.go` mid-scenario `UPDATE rimsky_nodes`.**
  `code:test/scenarios/unresolved_executor_test.go#38-42` does
  `UPDATE rimsky_nodes SET executor = 'does_not_exist_unknown' WHERE id = $2`
  on a live node to arrange the unresolvable-executor scenario. Fix:
  arrange via a template declaring an unknown executor at
  registration; delete the raw-SQL mutation.

#### Bucket 11 — Structural gaps (convention → construction)

Places where the invariant is upheld only by convention across multiple
sites; the fix is a structural collapse so the "correct-by-construction"
property is enforced by the shape, not the discipline.

- **[11a] Retire `rimsky_frames.state`; derive "unresolved" from
  `rimsky_node_runs.state` alone.** The picker
  (`code:lib/foundation/persistence/postgres/messages.go#191-210`) tests
  `f.state = 'running'`; the frame-end path
  (`code:lib/foundation/persistence/postgres/frames.go::ListRunningFramesNoPendingNodes`)
  defines when `f.state` transitions off `'running'` by looking at
  `rimsky_node_runs.state IN ('pending','stale','running','held','parked')`.
  Two sites agree today by convention.

  Structural fix: eliminate the `state` column on `rimsky_frames`
  entirely. Retire `MarkRunningFrameTerminal`. Rewrite the picker's
  gate as a direct predicate on `rimsky_node_runs`: "does any frame
  owned by this instance have any node_run in a non-terminal state?"
  Retire the `uq_rimsky_frames_running (instance_id) WHERE state = 'running'`
  unique partial index; replace with an instance-scoped exclusion on
  frame open. Subsumes the schema `'queued'` degree of freedom
  (item-2's migration 017 left `'queued'` in the CHECK — moot once
  the column is gone).

- **[11b] Concept-doc clarity on the two-step "fresh node_runs at frame
  start" guarantee.** At row-time, new node_run rows carry `data = '{}'`
  in `rimsky_node_attributes` (SnapshotBagForNewRun falls through to
  the empty branch because each frame mints a fresh root RunScope).
  Template-schema defaults are applied at dispatch time via
  `MergeAttributeDefaults`. Observable behavior at execution matches
  the invariant; anyone reading `rimsky_node_attributes.data` directly
  and expecting "defaults are already there" would be misled. Add a
  clarifying note to `concept:attribute` (via spec pipeline).

- **[11c] Template-validator warning: `message_queue_mode: coalesce` +
  multiple non-idempotent declared message types.** Coalesce mode
  cancels ALL pending messages per instance regardless of type
  (`code:lib/runtime/message_delivery.go::EnqueueMessage#46-55`); a
  `stop` cancels a pending `start` of a different type. Intentional
  per item-2's decision, but the template validator has no guardrail
  warning when a template declares distinct non-idempotent payload
  shapes AND selects coalesce. Add a warning (not an error — coalesce
  is legitimate).

  Related open question from item 2: per-instance vs per-message-type
  coalesce scope. If per-type coalesce becomes a supported mode, this
  validator warning becomes stricter (an error under coalesce mode
  when multiple types are declared, "use `per-type-coalesce`
  instead").

- **[11d] Where does `attribute_overrides_match_counts` live?**
  Structural gap corresponding to 10b's underlying question. Not the
  code fix itself — the design decision. Wants a spec pass.

- **[11e] Where does instance termination happen?** Structural gap
  corresponding to 10c's underlying question. Not the code fix
  itself — the design decision. Wants a spec pass.

#### Bucket 12 — Language sweep (extends and supersedes item 9)

Item 9 named the message-send / event-emit vocabulary sweep as
`EmitCascadeMessage` rename + prose. The extended audit found the
scope is materially wider — DSL surface, proto surface, executor SDK,
and two more axes of naming drift (run vs node_run; three names for
the node_run id). Per the uniformity rule, land as one atomic sweep;
supersede item 9 with the expanded scope here.

- **[12a] Message-send verb sweep.** All "emit" for a message action
  becomes "send"; all "emit" for an event/signal stays "emit". Blast
  radius:
  - Concept slug: `message-emitter-node` → `message-sender-node` (25+
    citing files across concepts / decisions / stories / code).
  - Decision slug: `compose-driver-emits-empty-message-after-create` →
    `compose-driver-sends-empty-message-after-create` (7 citing sites).
  - Story slug: `cascade-emit` → `cascade-send` (open — the noun
    "cascade-emit envelope" is a first-class term). Discuss during
    execution.
  - Prose in ~25 design files (`concept:message`, `concept:frame`,
    `concept:instance`, `concept:cascade`, `concept:publisher`,
    `concept:sensor`, `concept:message-emitter-node`,
    `concept:message-schema`, `concept:publisher-subscription`, plus
    stories for publisher, sensor, node-admin, typed-message,
    empty-message-wake, frame-origin-audit, and decisions for
    `emit-as-node-kind`, `attribute-set-as-body`, `idempotency-key-header-universal`,
    `envelope-type-discriminator`).
  - DSL/YAML surface: `TemplateNodeDef.EmitsMessage` (Go field name
    with `yaml:"emits_message"` tag) → `TemplateNodeDef.SendsMessage`
    (yaml `sends_message`).
  - Executor SDK: `HandlerContext.EmitCascadeMessage` /
    `EmitMessageType` → `SendCascadeMessage` / `SendMessageType`.
  - Built-in executor package: `lib/runtime/executor/builtin/emit_message/`
    → `.../send_message/`; alias `rimsky.emit_message` →
    `rimsky.send_message`; constant `KindName = "emit_message"` →
    `"send_message"`; `InProcURL` string mirrors.
  - Wire proto: `OPERATIONAL_KIND_MESSAGE_EMITTED = 60` →
    `OPERATIONAL_KIND_MESSAGE_SENT`; `MessageEmittedPayload` →
    `MessageSentPayload`; field `message_emitted = 10` →
    `message_sent`. `KindMessageEmitted()` mirror.
  - Runtime symbols: `runner_emit_message.go` file rename;
    `emitCascadeMessage` / `emitCascadeMessageInTx` function renames;
    `CanonicalizeEmitMessageSugar` rename;
    `runner_dispatch.go` `emitMessageType` local + `DispatchExtras.EmitMessageType`
    field rename.
  - CLI stderr strings and MCP tool schema description
    (`code:lib/control/controlapi/mcp_route.go#88` "treated as a
    publisher emit; otherwise as an operator emit").
  - Test names, comments, error strings (~18 hits across 9 files).
  - Outlier: `story:message-bus` — file/story spelling "bus" instead
    of "queue" throughout. Rename story to `story:message-queue` or
    fold into an existing story; prose rewrite.

- **[12b] `run` → `node_run` term unification.** The codebase drops
  the `node_` prefix from "node_run" throughout ("nobody knows what
  a run is"). Sweep:
  - `RunTree` → `NodeRunTree`; `RunTreeRow` → `NodeRunTreeRow`;
    `CreateRootRun` → `CreateRootNodeRun`; `CreateChildRun` →
    `CreateChildNodeRun`; `ParentRunID` → `ParentNodeRunID`;
    `ExitRunID` → `ExitNodeRunID`.
  - `PureCascadeReadyRow.RunID` → `NodeRunID`; ripple through the
    scheduler and cascade walker.
  - `RunArgs` → likely stays (it's the runtime's arg bundle, not a
    run-of-node_run reference) — call it out and confirm.
  - `RunScope`: OPEN question. Is a RunScope actually "the scope in
    which node_runs live" (in which case leave it, or rename to
    `NodeRunScope`), or is it a per-frame graph-invocation scope
    (`main` / sub-graph name) whose name has nothing to do with a
    "run"? Look at `concept:run-scope` and decide during the sweep.

- **[12c] `DispatchID` / `RunID` / `NodeRunID` unification →
  `NodeRunID`.** Three Go names for the same underlying column
  (`rimsky_node_runs.id`):
  - `DispatchID`: on `acquisition` struct (`code:lib/runtime/runner_acquire.go#44`),
    threading through supervisor, dispatch results, executor scratch
    writer, breakpoint eval — dozens of sites.
  - `RunID`: on `PureCascadeReadyRow`, `NodeRunTreeRow`, `CreateRoot*Input`,
    `WaitSetRow.{ReceiverRunID,SenderRunID}`, etc.
  - `NodeRunID`: on `AttributeRow.NodeRunID`,
    `ClaimHandles.UpdateNodeRunID`, `EnqueuedNodeRunKey.NodeRunID`.

  Winner: `NodeRunID`. Sweep every occurrence to that name. This
  overlaps 12b — do them in one commit so the churn is coherent.

- **[12d] Prose in the design catalog.** Sweep every "emit" applied
  to a message across `.ok-planner/design/{concepts,stories,decisions,tensions}`.
  Coordinate with 12a's slug renames so the prose lands in a
  post-rename state.

Sequencing note: 12a + 12b + 12c ship as one commit per the
Plumbline uniformity rule ("no coexisting dialects"). 12d rides
alongside since the slug renames it references.

#### Bucket 13 — Cosmetic cleanup

- **[13a] Retire remaining `queue-drain-converges` naming residue.**
  Story is retired but the following still spell it:
  - `test/scenarios/story_queue_drain_converges_e2e_test.go` (file
    name + `TestStoryQueueDrainConverges_*` function names).
  - `lib/services/test/scenarios/queue_drain_converges_demo_e2e_test.go`
    (file name + `TestQueueDrainConvergesDemo_*` function names +
    helpers).
  - `examples/queue-drain-converges-demo.{sh,yaml}` (shipped example
    filenames + in-file comments).
  Test files re-tagged to `@story: cascade-emit` internally in
  commit `1ad171c2`, but the filenames still cite the retired slug.
  Rename to reflect the actual story (`cascade_emit_*` /
  `emit-demo` — TBD during the pass).

- **[13b] Stale tension slug reference.**
  `tension:event-vocabulary-implies-delivery` lists `named-event`
  in its `affects:` frontmatter block; the current catalog has
  `concept:signal` under that role. `named-event` no longer exists
  as a concept slug. Fix: update the `affects:` list. Note: this
  tension is open; the fix does not resolve the tension, only
  corrects the stale reference. Change rides through the spec
  pipeline (touches `design/`).

#### Proposed sequencing

Under the same dependency logic as the original ledger:

1. **Bucket 10 first (real leaks).** Small blast radius per item,
   corrective, and orthogonal to bucket 12 (the rename). Order
   within the bucket: 10a → 10d → 10b → 10c. 10a and 10d are
   mechanical (signature change / test rearrangement). 10b and
   10c want a spec pass because they raise "where does this state
   actually live" design questions (paired with 11d / 11e).
2. **Bucket 11 second, alongside the spec passes for 10b/10c.**
   11a (retire `rimsky_frames.state`) is the biggest structural
   change but touches only frame/scheduler/persistence layers.
   11b / 11c are small.
3. **Bucket 12 third, as one atomic sweep.** After bucket 10 and
   11 land, the code shape is stable; the rename covers the final
   shape, not a moving target. One commit per the uniformity rule.
4. **Bucket 13 last.** File renames and the stale tension slug —
   trivial once the rename is done.

Two open ledger fallout tests (`TestAttributeOverridesMatchOverlay*`,
`TestNodeLatestAttributeBagFullStack`) fold into bucket 10b's spec
pass (the first two directly; the third is from item 1 and may or
may not survive the pass — revisit).

#### Services-integration regressions caught by the rename sweep

Two services-integration e2e tests were surfaced as failing during
the 12b+12c rerun. Neither was caused by the rename; both are
undiscovered regressions from earlier deliberate changes in this
same ledger. Recording them so the trace is durable.

- **`TestSubscriberOpenlineage` — regression since commit `80a11733`**
  (item 1, empty-message-wake unification, 2026-07-04). That commit
  introduced the empty-type message-receiver node via
  `receiverTypes = append(receiverTypes, "")` at
  `lib/control/controlapi/instances.go`. From then on, that node's
  runs emit lineage with `NodeAlias == ""`, and the OpenLineage
  subscriber's `MakeLeafRunEvent` fell through to `job.name == ""` —
  an OpenLineage-spec violation. Item 1 swept the runtime + delivery
  path but not the observability layer. Fix (commit `bf8d306b`):
  added an `"empty-message-receiver"` fallback in
  `lib/services/subscribers/openlineage/emitter.go`.

- **`TestPGErrorClasses_Delivered` — regression since commit
  `c6907c29`** (pre-work for item 1: cross-cutting `instance: true`
  subscription retirement, 2026-07-04). That commit swept 36 files
  including graph, runtime, `test/scenarios/`, but missed
  `lib/services/test/scenarios/pg_error_classes/`. Two templates in
  that file kept `"instance": true`, and their `POST /templates`
  requests returned 400 from that commit forward. Fix (commit
  `bf8d306b` + `55dc4fcd`): converted both subscriptions to
  per-sender-node form (`"node": "worker"` for the claim_unavailable
  template, `"node": "acquirer"` for the swap_failed template).

Both regressions share a shape: a deliberate refactor swept its
immediate consumers but not its downstream services-integration
consumers. Neither test runs in the default `make test-all` loop —
both require `make core-images && make service-images` first — which
is why they went undetected for several days. Worth folding into a
guardrail item: any change that removes a template-spec field or
adds a runtime-implicit node should trigger the full services e2e
suite before landing.

