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
- [ ] **concept:signal + lib/foundation/signal/taxonomy.go** —
  `transient/*` taxonomy lists only `transient/retry/*` and
  `transient/await_async`; code emits `transient/infra/<class>`
  (`lib/runtime/runner_error_policy.go:212`) and
  `transient/release_and_requeue/<class>`
  (`lib/runtime/runner_error_policy.go:332`), neither in the canonical
  list. `ValidateTypePath` would reject them. Fix touches both the
  concept doc taxonomy section and `canonicalEmitPatterns` in code.
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

- [ ] **concept:error-policy** — names a `discard-claims` action that
  doesn't exist; closed set is `retry, give_up, pass, release_and_requeue`
  (`lib/foundation/spec/policy.go:38-43`). The behavior described maps
  to `release_and_requeue`. Operator-facing — anyone writing YAML against
  the doc gets a validator rejection.
- [ ] **concept:transition-reason** — references reasons
  (`infra_reenqueue`, `handler_resume`, `handler_error`) that aren't in
  the 15-reason enum (`lib/foundation/cascade/state.go:32-57`). State-
  machine reference is inaccurate.
- [ ] **concept:node-run + concept:error-policy** — both assert a
  three-field policy cursor persists (action-index, retry-counter,
  current-error-class); only `retry_counter` column exists
  (`lib/foundation/spec/policy.go::EvaluatorState:27-29`,
  `col:rimsky_node_runs.retry_counter`). Two concepts asserting
  persistence of fields that have no column.
- [ ] **concept:message-emitter-node** — invariant says the envelope
  inserts in the same tx as the node's terminal-resolution;
  `emitCascadeMessage` (`lib/runtime/runner_emit_message.go:32`) opens
  its own tx. Atomicity is the deterministic idempotency key, not tx
  coupling — the invariant is silently violated.
- [ ] **decision:child-execution-unification** — promises unified
  dispatch AND unified settle; only dispatch was unified. Settle stayed
  split between `SettleFromDelegate` (`lib/runtime/child_execution.go:149`)
  and `SettleFromFanoutChild` (`lib/runtime/child_execution.go:273`) —
  exactly the rejected-alternative shape the decision says we wouldn't
  ship. Either complete the unification or retire the decision.
- [ ] **decision:wait-set-topic-kind-taxonomy** — doc names
  `terminal/transient/attribute/message`; migration
  (`lib/foundation/persistence/sqlite/migrations/001-initial.sql:322`)
  enforces `state/attribute/transient/terminal` (no `message`); test
  (`lib/foundation/persistence/sqlite/wait_set_topic_kind_test.go:121-129`)
  explicitly rejects `message`. Two opposite stories on what's allowed.

## Open question — lineage: data-flow vs. causal-flow

Current implementation tracks **data lineage**: `substitution_refs`
records "this run looked up X.attribute.Y, so its output depends on the
run that produced X" (`lib/runtime/lineage_writer.go::CollectSubstitutionRefsForEmit:278`).
Wake-only causality (consumer woken by upstream's `terminal/success` but
reading nothing from it) is a different relationship — **causal
lineage** — and the model is silent on it. Fan-out parents, pure-cascade
nodes, and subgraph exits all live entirely in causal-lineage land (no
attribute output to be cited), so they're structurally invisible in the
existing surface. The audit also surfaced that **concept:lineage** is
silent on fan-out parents emitting no leaf-run row; that gap is
downstream of the data-flow-vs-causal-flow framing — once we pin the
framing, the leaf-run-emission boundary writes itself.

### Recommendation

Keep lineage as data-lineage; document the boundary explicitly in
**concept:lineage**. The cost of conflating the two is that operators
read a ref and can't tell whether it means "this run's output literally
depended on that run's output" or "this run was notified when that run
settled." The precision protected elsewhere in the model erodes. If
causal-flow becomes a real operator need later, it's a cleaner addition
as a parallel surface (`/v1/lineage/causal/...` or a sibling `wake_refs`
array) than a polymorphic field on the existing one.

### Alternative framings (for completeness)

- Extend `substitution_refs` (likely renamed to `upstream_refs`) to
  cover wake-only causality; walker treats both kinds transparently.
  Most info, conflated semantics.
- Split into `substitution_refs` (data) + `wake_refs` (causal); same
  shape, different array names. Preserves the distinction in storage
  but doubles the cognitive surface for the same operator question.
