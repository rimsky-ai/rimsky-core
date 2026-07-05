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


