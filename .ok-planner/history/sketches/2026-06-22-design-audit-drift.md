# Design-doc audit — drift, divergences, open question

Audit of `.ok-planner/design/{concepts,stories,decisions}/` against the
current implementation. Three clusters: artifacts the recent session's
changes silently invalidated; pre-existing divergences independent of
this session; and one open design question.

Working document — items addressed inline as we discuss them, checked
off as we go. Not a brainstorm input.

## Cluster 1 — Stale from this session's changes

Each of these silently misrepresents what the system does NOW. The
underlying delta is the same: the wait-set's dual role split (wake +
data carrier → wake only), and the attribute cascade gained a diff
gate (always-emit → emit-on-change).

- [x] **concept:wait-set** — body still describes drained-row data carrying;
  model is now wake-only. Substitution deps come from each subscribed
  sender's latest fresh-settled run, not from drained rows
  (`lib/runtime/substitution_context.go:65-140`).
- [x] **concept:signal** — describes `attribute/<key>/changed` as
  "non-empty delta"; actually emitted only when the key differs from the
  prior run's value (`lib/runtime/attribute_cascade.go:22-62`). Also
  tightened the opening "two paths" framing: payload is consumed at
  walk-time and audit-write time only, never propagated to subscribers.
- [x] **concept:signal + lib/foundation/signal/taxonomy.go** —
  `transient/*` taxonomy lists only `transient/retry/*` and
  `transient/await_async`; code emits `transient/infra/<class>`
  (`lib/runtime/runner_error_policy.go:212`) and
  `transient/release_and_requeue/<class>`
  (`lib/runtime/runner_error_policy.go:332`), neither in the canonical
  list. `ValidateTypePath` would reject them. Fix touches both the
  concept doc taxonomy section and `canonicalEmitPatterns` in code.
  Fixed: added `transient/infra/*` and `transient/release_and_requeue/*`
  to `canonicalEmitPatterns`; updated `taxonomy_test.go` accept-list
  to cover both; updated `concept:signal`'s `transient/*` enumeration
  with descriptions of when each fires. Also wired `ValidateTypePath`
  into `emitSignalInTxWithFilter` as a mechanical guard so any future
  undeclared emit fails loud rather than silently landing in the audit
  log. New test file `signal_emit_test.go` covers the guard
  (rejects empty, retired `message/*`, unknown top-level kind,
  malformed attribute leaf) plus a regression assertion that the two
  newly-declared `transient/*` patterns remain canonical.
- [x] **concept:attribute** — "trigger-message" wording predates the
  unified `messages.<type>` substitution surface
  (`lib/graph/attribute/substitution.go:166-167`). Updated both stale
  spots (line 27 subgraph note, line 44 grammar invariant). Also
  deleted the backward-compat `case "deps":` and `case "trigger":` shims
  in `lib/graph/attribute/substitution.go` and removed the corresponding
  assertions in `substitution_test.go` (unknown source kinds now fall
  through to the default arm uniformly).
- [x] **decision:substitution-context-builder-reads-drained-rows** —
  fully superseded by `populateSubscribedSenderDeps` reading the persisted
  store. Retired (moved to `_retired/` with retirement note + original
  retained for history). New `decision:substitution-deps-from-persisted-senders`
  written with current choice + rationale + alternatives. Decisions TOC
  updated. `@decision: substitution-deps-from-persisted-senders` annotation
  added to `BuildAttributeDeps`.
- [x] **story:cascade-signal-blind** — acceptance + proof
  (`test/scenarios/cascade_signal_blind_e2e_test.go:247-287`) only
  exercise first-emit; never test the same-value-resettles-no-emit case
  that diff-gating introduced. Story rewritten in user-outcome shape
  (Role/Capability/Business value lifted out of implementation
  prescription, Acceptance/Falsifier updated to include diff-gate
  observable). New `testCascadeAttributeChangedDiffGate` sub-test added:
  sender re-settles with the same value via `test/wake` message, receiver
  woken exactly once across both rounds.
- [x] **story:cross-frame-coupling** — proof
  (`test/scenarios/story_cross_frame_coupling_e2e_test.go:170-317`)
  passes, but the `attribute/counter/changed` subscription it appears to
  exercise (line 247) is now inert under diff-gating because B always
  returns the same `counter: 2`. The proof technically holds; the lever
  it claims to test is no longer pulled. Story rewritten in user-outcome
  shape (Role/Capability/Business value lifted out of implementation
  prescription); "coupling" terminology dropped from the prose (replaced
  with "iterative workflows"); the self-drain test rewritten as
  `TestStoryCrossFrameCoupling_SelfDrainConvergesViaDiffGate` —
  emit-node subscribes only to `attribute/step/changed` (not
  terminal/success), worker returns a fixed `step: 1`, the diff-gate
  stops the loop after exactly one iteration. Assertions are exact-count
  (worker=2, emit-node=1, drain/tick messages=1), not a runaway-prevention
  ceiling. `BackEdgeCycle_LoopsThenConverges` renamed to
  `BackEdgeCycle_LoopsWithoutGate` (the original name implied a
  convergence proof the test never made).

## Cluster 2 — Pre-existing divergences

These weren't caused by this session — they were already wrong, and the
audit caught them as a side effect. Each is independently shippable;
they don't share a delta the way Cluster 1 does.

- [x] **concept:error-policy** — names a `discard-claims` action that
  doesn't exist; closed set is `retry, give_up, pass, release_and_requeue`
  (`lib/foundation/spec/policy.go:38-43`). The behavior described maps
  to `release_and_requeue`. Operator-facing — anyone writing YAML against
  the doc gets a validator rejection. Fixed: concept:error-policy
  invariant rewritten to describe `release_and_requeue` (release-and-
  re-enqueue, not in-place); concepts TOC entry updated; decision:
  in-place-retry stripped of the `discard_claims_then_retry` mention
  (that action is not in-place); story:template-error-policy four-action
  enumeration updated with corrected description of release-and-requeue
  behavior.
- [x] **concept:transition-reason** — references reasons
  (`infra_reenqueue`, `handler_resume`, `handler_error`) that aren't in
  the 17-reason enum (`lib/foundation/cascade/state.go:32-57`). State-
  machine reference is inaccurate. Fixed: parenthetical example list
  in "What it is" rewritten with actual reasons; audit-event-kind list
  in Purpose §2 rewritten with actual non-signal reasons (`deadline_resume`
  not `handler_resume`, removed `infra_reenqueue`); the "handler_error
  reason is a deliberate dead-end sentinel" invariant deleted (described
  a sentinel that never gets constructed).

  Additional drift surfaced and swept in the same pass: the same phantom
  values also appeared as **creation reasons** in several live design
  docs. Actual creation-reason enum is `{cascade, operator_invalidate,
  recalculate, message_delivery}` (`lib/foundation/cascade/state.go:189-194`).
  Fixed sites: `decision:non-cascade-direct-to-stale` (Choice + Rationale
  both rewritten — the two paths are now correctly named: operator-
  invalidate, fanout-parent recalculate, message-delivery);
  `decision:mode-default-most-recent` (non-cascade-immunity bullet);
  `story:idempotent-mode-dedupes` (Capability + Falsifier);
  `concept:wait-set` (non-cascade-rows invariant);
  `concept:cascade-mode` (mode-applies-only-to-cascade invariant);
  `concept:node-run` (transition table — `handler_error` removed from
  `running → held` causes and replaced with the actual `handler_held`
  reason; phantom `running → running (policy_retry)` transition removed
  and replaced with a parenthetical noting that in-place retry fires no
  state transition).
- [x] **concept:node-run + concept:error-policy** — both assert a
  three-field policy cursor persists (action-index, retry-counter,
  current-error-class); only `retry_counter` column exists
  (`lib/foundation/spec/policy.go::EvaluatorState:27-29`,
  `col:rimsky_node_runs.retry_counter`). Root cause: this session's
  commit `339809cc` ("refactor(policy): collapse per-class retry counts
  into single node-level MaxRetries + per-class action") simplified
  the model and the docs were never swept. Fixed both concept docs
  end-to-end against the new shape: error-policy's "What it is",
  Boundaries, Adjacent, and Invariants sections all rewritten to
  describe the flat per-class action map + per-dispatch MaxRetries cap;
  the action-chain machinery, no-progress-counter framing,
  signal-type-resets-counter claim, pass-advances-action-index
  invariant, supervisor-default-no-progress-cap, and synthetic-no-progress-
  class metric all deleted (none have a corresponding code path);
  acquire-failure fallback corrected to the actual two-level form
  (exact-class → family-class, then fall-through to give-up).
  node-run lines 28 and 95 cursor descriptions both shrunk to the single
  per-dispatch `retry_counter`.
- [x] **concept:message-sender-node** — invariant says the envelope
  inserts in the same tx as the node's terminal-resolution;
  `emitCascadeMessage` (`lib/runtime/runner_emit_message.go:32`) opens
  its own tx. Atomicity is the deterministic idempotency key, not tx
  coupling — the invariant is silently violated. Fixed: Boundaries
  Owns clause and Invariants entry both rewritten to describe the
  actual mechanism — the envelope inserts in its own transaction
  during the handler call (envelope row + frame enqueue atomic with
  each other); at-most-once across retries is preserved by the
  deterministic `(node-id, frame-id)` idempotency key, not by tx
  coupling with terminal-resolution.
- [x] **decision:child-execution-unification** — promises unified
  dispatch AND unified settle; only dispatch was unified. Settle stayed
  split between `SettleFromDelegate` (`lib/runtime/child_execution.go:149`)
  and `SettleFromFanoutChild` (`lib/runtime/child_execution.go:273`) —
  exactly the rejected-alternative shape the decision says we wouldn't
  ship. Either complete the unification or retire the decision. Fixed:
  retired the decision (moved to `_retired/` with retirement note +
  original retained for history) since the "delegation is fan-out with
  N=1" rationale doesn't survive scrutiny — fan-out clones the calling
  node N×1, delegation dispatches a sub-graph's distinct internals 1×N,
  the two are different operations not variants of one shape. New
  `decision:fan-out-and-delegation-are-distinct-mechanisms` written
  describing the actual situation: thin shared dispatch helper (a
  partitions×children matrix) but intentionally-split settle because
  the two fan-in shapes differ. `concept:child-execution` reframed as
  an umbrella naming the two distinct mechanisms (not variants of one
  shape) plus the thin shared dispatch helper. `concept:fan-out`
  Definition rewritten to lead with the cloning framing — "fan-out
  clones the calling node N times" — and explicitly call out no
  attribute aggregation. `@decision: fan-out-and-delegation-are-distinct-mechanisms`
  annotations added at the three load-bearing sites: `DispatchChildren`
  (the shared helper), `dispatchFanOutChildren`, `applyTerminalCompleteSubgraphCaller`.
  Decisions TOC updated.
- [x] **decision:wait-set-topic-kind-taxonomy** — doc names
  `terminal/transient/attribute/message`; migration
  (`lib/foundation/persistence/sqlite/migrations/001-initial.sql:322`)
  enforces `state/attribute/transient/terminal` (no `message`); test
  (`lib/foundation/persistence/sqlite/wait_set_topic_kind_test.go:121-129`)
  explicitly rejects `message`. Two opposite stories on what's allowed.
  Fixed: decision rewritten to "3 canonical kinds (terminal, transient,
  attribute) projecting the signal taxonomy plus `state` as defensive
  fallback = 4 total" — matches the migration exactly; `message` retirement
  acknowledged in the Alternatives section. Decisions TOC entry updated.
  Parallel claim in `concept:signal` invariant (line 104) corrected:
  was "four canonical kinds (terminal, transient, attribute, message)";
  now "three canonical kinds + `state` fallback" with cross-reference to
  the decision.

## Open question — lineage: data-flow vs. causal-flow [RESOLVED]

Resolution: keep lineage strictly as data-lineage; do not extend the
surface to capture wake-only causality. Operators wanting "consumer C
was woken by upstream U" consult the audit log's signal-emission rows
or the wait-set ledger directly.

Fixed: `concept:lineage` Definition rewritten to call out lineage as
data lineage explicitly (attribute-substitution refs + claim-tree
linkage), with substitution_refs framing made literal. Added a Boundaries
clause stating wake-only causality is NOT owned by `concept:lineage`,
naming the audit log and wait-set ledger as where operators consult for
it. Added an invariant capturing the pass-through-emits-no-leaf-run
behavior: fan-out parents (executor skipped at acquire-phase split) and
pure-cascade nodes (no executor declared) emit no leaf_run record by
design (the sketch's earlier note about subgraph exits was wrong on
inspection — exits are normal executor-bearing nodes). Parallel
invariant added to `concept:lineage-record` so the leaf-run record
shape carries the same boundary.

No new decision doc — the boundary is captured in the concept invariants;
if the framing-as-decision becomes useful later (e.g. a wake-refs sibling
surface is proposed), a decision can be written then to record the
choice between data-only and causal-extension.

### Original analysis (retained for history)

Current implementation tracks **data lineage**: `substitution_refs`
records "this run looked up X.attribute.Y, so its output depends on the
run that produced X" (`lib/runtime/lineage_writer.go::CollectSubstitutionRefsForEmit:278`).
Wake-only causality (consumer woken by upstream's `terminal/success` but
reading nothing from it) is a different relationship — **causal
lineage** — and the model is silent on it. Alternative framings considered:
(a) extend `substitution_refs` (rename to `upstream_refs`) to cover
wake-only causality, walker treats both transparently — rejected for
conflating semantics; (b) split into `substitution_refs` (data) +
`wake_refs` (causal) — rejected for doubling cognitive surface without
clear operator demand.
