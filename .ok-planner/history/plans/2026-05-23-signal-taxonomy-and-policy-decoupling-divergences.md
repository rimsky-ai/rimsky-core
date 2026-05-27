# Divergence audit — Signal taxonomy and policy decoupling

Audited working tree against `plan:2026-05-23-signal-taxonomy-and-policy-decoupling`. Reports below cover places where the implementation made a different shape than the plan literally said, ordered roughly by pass. Implementer-flagged items are verified against the tree; auditor-found items follow.

---

## Pass 1 — Signal infrastructure + audit-emission wiring

### D1. CEL `payload` binding — plain dyn-map plus AST walk, not `ext.NativeTypes`

- **Plan said:** Task 5 — "For payload struct fields, use `cel.ObjectType` with the fully-qualified Go type name — see CEL-go's Go-struct-to-CEL example for the registration pattern."
- **Implemented:** `code:foundation/signal/cel.go::buildEnv` binds `payload` as `cel.MapType(cel.StringType, cel.DynType)` unconditionally; field-name checking for exact-type subscriptions happens via an AST walk over `payload.<field>` select operands in `code:foundation/signal/cel.go::checkPayloadFields`. No `ext.NativeTypes` / `cel.ObjectType` registration.
- **Inferred reason:** Cleaner shape — sidesteps the cel-go native-type tag-name surface (which would require fully-qualified Go type names baked into operator-authored templates) while still giving exact-type subscriptions the same compile-time field-misspelling diagnostics. Documented in the file header comment block.

### D2. `EmitSignal` moved to subpackage `foundation/signal/audit/`

- **Plan said:** Task 6 — "Create `foundation/signal/audit.go` … `package signal`."
- **Implemented:** `code:foundation/signal/audit/audit.go` lives in subpackage `audit`. Callers import as `signalaudit "github.com/rimsky-ai/rimsky-core/foundation/signal/audit"` and invoke `signalaudit.EmitSignal(...)`.
- **Inferred reason:** Forced choice — once `foundation/spec/policy.go` started importing `foundation/signal` for `spec.Resolution.Signal` (Pass 3 Task 33), the original `foundation/signal/audit.go` importing `foundation/persistence` created a cycle (`persistence` depends on `spec`). Header comment at `code:foundation/signal/audit/audit.go` explicitly cites this rationale and points at Pass 3.

### D3. Bare-attribute substitution refs auto-subscribe to `attribute/*`, not `attribute/*/changed`

- **Plan said:** Task 25 — "`{{nodes.X.attribute}}` (bare) → `(X, "attribute/*/changed")` (whole-attribute pull)."
- **Implemented:** `code:graph/node/subscription_edges.go::edgeFromSubstitutionRef` (~L645) emits `signal.TypePath("attribute/*")` for bare-attribute refs.
- **Inferred reason:** Likely accidental — the pattern still matches all per-key `attribute/<k>/changed` signals via prefix, and `code:foundation/signal/taxonomy.go::ValidateSubscriptionType` accepts `attribute/*` per the wildcard rule, so behaviour is equivalent in practice. But it diverges from the canonical "all `attribute/*/changed`" shape the plan and concept doc describe.

### D4. Per-attribute signals are emitted twice per terminal

- **Plan said:** Task 12 — emit one `attribute/<key>/changed` per key inside `applyTerminalComplete`'s attribute-delta path.
- **Implemented:** `code:runtime/runner_terminal.go::applyTerminalComplete` (~L360–377 and again ~L434–449) emits the same per-key `attribute/<key>/changed` signals **twice**: once inline into the cascade walker (so subscribers fire) and once again post-commit through `signalaudit.EmitSignal` for the audit row. Each pass produces one cascade walk and one `rimsky_events` row per attribute key.
- **Inferred reason:** Likely deliberate — the in-tx loop drives subscriber gating with the actual signal value (so a `when: payload.value > 5` subscriber sees the right payload), while the post-commit loop is the canonical audit-row write. Not literally what the plan described but doesn't appear buggy. Could be cleaner via a single helper. The `terminal/success` signal is also written by both paths.

---

## Pass 2 — Subscription consumption (signal-driven cascade)

### D5. `cascadeSubscribersStaleInTx` retired multi-level BFS; each terminal does its own walk

- **Plan said:** Task 21 — "Update all `cascadeSubscribersStaleInTx` call sites to pass the signal." Plan kept the BFS-over-receivers shape; the per-call recursion that pre-existed was implied to continue.
- **Implemented:** `code:runtime/runner_terminal.go::cascadeSubscribersStaleInTxWithVisited` (~L545–915) — the deeper-BFS recursion through downstream receivers is gone. Comments at ~L630–650 + ~L903–909 explicitly state "no deeper BFS — each receiver's own terminal eventually fires its own cascade walk." The `visited` cycle-guard set is retained only for the hard-dep walk.
- **Inferred reason:** Spec-intent override — the implementer judged the BFS as a wait-set seeding-semantics change: pre-reshape, all downstream receivers were gated at the root sender's terminal; under signal-typed gates, an intermediate receiver's `when:` predicate cannot be evaluated until that receiver has its own real signal. So each terminal walks one level, and the next terminal walks the next. The implementer flagged this and the header comment captures it. Out-of-scope for the plan's literal text.

### D6. New persistence primitive `HasRunForNodeInFrame` + intra-terminal `visitedReceivers` set

- **Plan said:** No mention.
- **Implemented:** New `HasRunForNodeInFrame` method on `code:foundation/persistence/nodes.go::NodeTable` (~L235–243), with Postgres + SQLite implementations. Paired with a `visitedReceivers map[UUID]struct{}` set threaded through `cascadeSubscribersStaleInTxWithVisited` (`code:runtime/runner_terminal.go` ~L378–795). Together they enforce a once-per-frame dispatch guard across (a) the same terminal emitting multiple signals (intra-terminal) and (b) a chain of upstream terminals seeding the same receiver (cross-terminal).
- **Inferred reason:** Forced choice — under the new multi-signal-per-terminal cascade pathway (one `terminal/success` + N `attribute/<key>/changed` + M `event/<name>`), each signal would re-affirm the receiver's run row without this guard, multiplying dispatch work. The plan didn't anticipate this when designing the per-key signal-emit loop. Self-edges intentionally bypass the cross-terminal guard (~L800) so "drain my own queue" idioms continue to work.

### D7. `target: self` semantics — runtime suffix check, not validator rewrite

- **Plan said:** Implicit from Task 26's translation table that `target: self` becomes part of `type: message/foo/operator/self`; spec invariant says the receiver-alias resolution happens via the validator substituting `self_alias`.
- **Implemented:** `code:runtime/message_delivery.go::cascadeMessageSubscribersInTx` (~L341–345) — a runtime `strings.HasSuffix(string(e.TypePattern), "/self")` check rejects envelopes whose `target` doesn't match the receiver's own node-type. No validator-side `self_alias` substitution.
- **Inferred reason:** Spec-intent override — implementer kept the wire pattern `message/.../self` as a magic literal because positional wildcards remain rejected by `ValidateSubscriptionType`, so a `when: payload.target == self_alias` rewrite wasn't structurally feasible. Comment block at ~L268–274 captures the rationale.

---

## Pass 3 — Policy 3-tuple decoupling

### D8. `spec.ResolvedAction.Targets` and `.Frame` fields retained, not dropped

- **Plan said:** No explicit instruction; spec intent was to retire `targets` / `frame` along with `invalidate`.
- **Implemented:** `code:foundation/spec/policy.go::PolicyAction` (~L54, ~L59) still carries `Targets` and `Frame` fields, marked "retained for parse-compatibility through the retirement window; ignored by the runtime." `ResolvedAction` (~L80) drops them.
- **Inferred reason:** Compatibility shim — fixtures still parsing pre-reshape YAML with stray `targets:`/`frame:` keys don't reject at unmarshal time. The implementer kept these on `PolicyAction` (the YAML-facing shape) and only dropped them from `ResolvedAction` (the runtime-internal shape). Implementer-reported divergence (item 6) is partly inaccurate — only the `ResolvedAction` side dropped.

### D9. `ReasonPolicyInvalidate` var commented out, branch deleted, but enum spot retained

- **Plan said:** Task 43 — "Find `var ReasonPolicyInvalidate` … Delete the var declaration."
- **Implemented:** `code:foundation/cascade/state.go` (~L75–79) — the `var` is gone but replaced with a six-line comment retiring it; the `NextState` switch's `policy_invalidate` branch is deleted but `code:runtime/cascade_invalidate.go::invalidateSourceBucket` (~L484) still has `case "policy_invalidate", "handler_invalidate":` for metric-label bucketing, kept alongside a comment listing the legacy site.
- **Inferred reason:** Tracked-deprecation comment — the implementer kept the names visible so future grep-archeology finds the retirement note, even though the var itself is gone. The `invalidateSourceBucket` switch still listing the dead labels is dormant code that could be cleaned up but doesn't break anything (no caller can pass `"policy_invalidate"` anymore).

---

## Pass 4 — Retire lifecycle-handler + fold acquire-failure into `error_types:`

### D10. `handleAcquireUnavailable` routes through `OnError`, not `applyErrorPolicy` — and `OnError` writes a non-signal audit row

- **Plan said:** Task 41 — "Call into `applyErrorPolicy(ctx, args, acq, ...)` with this synthetic class."
- **Implemented:** `code:runtime/runner_lifecycle.go::handleAcquireUnavailable` (~L58) calls `OnError(ctx, OnErrorArgs{...ErrorClass: "acquire/unavailable"...})` — the older `code:runtime/on_error.go::OnError` (~L103), not the newer `applyErrorPolicy`. Critically, `OnError` (~L166–177) writes an audit row with `Kind: "error"` and a hand-built payload (`error_class`, `details`, `action_taken`, `action_index`, `delay_ms`), **not** the canonical `signalaudit.EmitSignal` path. The `OnError`'s `pass` branch (~L296–347) also still references "last_outcome=passed" / "last_outcome=fresh_unchanged" in comments.
- **Inferred reason:** Forced-choice or oversight — `applyErrorPolicy` requires an `*acquisition` value with `DispatchID`, `RunScopeID`, `FrameID`, `Locks` etc., which the pre-dispatch acquisition-failure path doesn't fully hold (no DispatchID yet, no successful Locks set). `OnError` is the older shim that took a flat `OnErrorArgs`. The downstream effect: `acquire/unavailable` doesn't produce a canonical `terminal/error/acquire/unavailable` signal — it produces a legacy `kind="error"` row. The new scenario test at `code:test/scenarios/acquire_unavailable_error_types_test.go` validates only state-machine outcomes, not signal emission; the spec's stated goal of unifying acquire-failure under the signal surface is only half-met.
- **(Resolved 2026-05-24 in review-cleanup:** Round-2 fix #1 rewrote `code:runtime/on_error.go::OnError` to emit the canonical signal via `signalaudit.EmitSignal` from each branch's state-transition tx (using the shared `errorPolicySignal` helper to construct the envelope). `handleAcquireUnavailable` still routes through `OnError` rather than `applyErrorPolicy` (the structural reason still holds: no `acquisition` with `DispatchID`/`Locks` at pre-dispatch), but the downstream effect described above no longer applies — `acquire/unavailable` now DOES produce a canonical `terminal/error/acquire/unavailable` (or `transient/retry/<n>/acquire/unavailable`) signal on `rimsky_events`. Round-3 review-cleanup also reworded the stale "last_outcome=passed" comments in the `pass` branch. The remaining D10 divergence is purely path-routing — `OnError` and `applyErrorPolicy` are two functions doing the same canonical thing, kept separate for the acquisition-vs-dispatch distinction; the signal-surface unification the spec called for is complete.**

### D11. `applyTerminalPass` comment reference lingers

- **Plan said:** Task 42 — "Find any other `applyTerminalPass` call site or comment reference via `rg 'applyTerminalPass' runtime/` and remove."
- **Implemented:** Function is gone, but `code:runtime/supervisor.go:496` and `code:runtime/runner_error_policy.go:295` still carry comments referencing the retired `applyTerminalPass` symbol.
- **Inferred reason:** Stale comment — minor, but the plan explicitly called for removal of all references.

### D12. ColorFresh-pass branch uses different reason from running-state to stale-state path

- **Plan said:** Implicit; the spec said "pass settles fresh." No specific TransitionReason guidance.
- **Implemented:** `code:runtime/runner_error_policy.go::applyResolvedAction` (~L304–306) uses `ReasonHandlerPass` for the running→fresh pass branch. `code:runtime/on_error.go` (~L296–333) splits stale→fresh (uses `ReasonAcquirePass`) from running→fresh (uses `ReasonHandlerPass`). Implementer-reported item 7 confirmed.
- **Inferred reason:** Cleaner shape — preserving the pre-reshape stale-state-vs-running-state ReasonAcquirePass / ReasonHandlerPass distinction so the audit log can still distinguish acquire-pass from executor-error-pass settlements. Not in the plan text but consistent with spec intent.

---

## Pass 5 — Retire `last_outcome` + isTerminal → isSettled

### D13. `ChildState.IsSuccess` treats unknown SettlingSignalType as success

- **Plan said:** Task 46.7 — "the aggregation logic that currently distinguishes `fresh_changed` vs `fresh_unchanged` now checks `signal.Type == "terminal/success"` plus the signal payload's `changed` field."
- **Implemented:** `code:runtime/run_tree.go::ChildState.IsSuccess` (~L117–135) treats an empty `SettlingSignalType` as success (comment: "pure_cascade / pre-Pass-5 legacy"), and accepts both `terminal/success` and `terminal/error/*` prefix matches as success. Latter is intentional (the `pass`-color settled-fresh case); former is a back-compat affordance for rows that pre-date the migration.
- **Inferred reason:** Pre-v1 carry-over — even though `rimsky` is pre-v1 and the migration drops `last_outcome` rather than nulling it, the implementer kept a defensive empty-string accept path. Not load-bearing, but adds an implicit "what does null mean?" semantic the plan didn't specify.

### D14. New aggregate error-class leaves (`terminal/error/aggregate/<policy>_failed`)

- **Plan said:** Task 46.7 — "failed outcome → `terminal/error/aggregate/<policy_name>_failed`."
- **Implemented:** `code:runtime/run_tree.go` (~L177, ~L247, ~L293, ~L350) — the three policies emit `terminal/error/aggregate/strict_failed`, `terminal/error/aggregate/threshold_failed`, `terminal/error/aggregate/first_failed`. Matches the plan. Confirms implementer item 10.
- **Inferred reason:** Direct execution of plan; flagged here only because the canonical taxonomy in `code:foundation/signal/taxonomy.go::canonicalEmitPatterns` (~L20–32) doesn't enumerate the `aggregate/*` leaves explicitly — they pass via the generic `terminal/error/*` prefix. Validator and emit-time validators are consistent with this, but the concept doc would need updating if exact-pattern documentation is sought.

### D15. New `waitSetTopicKindFor` mapping for the legacy `rimsky_wait_set.topic_kind` enum

- **Plan said:** No mention. Task 21 changed the cascade walker without touching the wait-set table.
- **Implemented:** `code:runtime/runner_terminal.go::waitSetTopicKindFor` (~L1200–1222) — a new collapsing function maps signal `TypePath`s back into the three-value `state | attribute | event` enum the wait-set table's CHECK constraint still enforces.
- **Inferred reason:** Forced choice — the DB CHECK constraint on `rimsky_wait_set.topic_kind` predates the reshape and wasn't included in the plan's migration scope. The implementer added a runtime adapter rather than expanding the CHECK constraint (which would have been a third migration). Comment at ~L1200–1212 captures this as a deferred follow-up.

### D16. `ClearSettlingSignalType` / `ResetFailedTerminalSettlingSignalType` rename + NULL-write semantic

- **Plan said:** Task 46.5 said add the column; no explicit instruction on what to do with `ClearLastOutcome` / `ResetFailedTerminalLastOutcome`.
- **Implemented:** Renamed to `ClearSettlingSignalType` / `ResetFailedTerminalSettlingSignalType` per `code:foundation/persistence/nodes.go` (~L151–177). Implementer item 9 confirmed; commented in-line that the clear semantic now flips to NULL (matching the pointer-typed field).
- **Inferred reason:** Cleaner shape — necessary consequence of the column type changing from non-null enum to nullable text pointer.

---

## Pass 6 — Bundled-executor vocabularies

### D17. `isRuntimeSynthesizedErrorClass` shim added to validator

- **Plan said:** No explicit instruction for the validator's range-check to bypass runtime-synthesized classes; the plan's Task 24 introduced the hook with implied silent-skip when the hook returned `ok=false`.
- **Implemented:** `code:graph/node/template_validator.go::isRuntimeSynthesizedErrorClass` (~L437–451) — explicit allow-list bypass for `acquire/*` prefix plus six exact classes (`template_resolution_failed`, `template_validation_failed`, `executor_schema_unavailable`, `attributes_schema_failed`, `retry_loop_no_progress`, `unresolved_executor`).
- **Inferred reason:** Forced choice — once Pass 6 sets `declared_error_classes: ["stub/*"]` (or similar) on each executor, operator templates declaring policies for these runtime-internal classes would fail validator range-check. Validator helper bypass is the cleanest fix. Implementer item 12 confirmed.

### D18. `errorClassMatchesDeclared` helper with `<prefix>/*` semantics

- **Plan said:** Task 23 said "cross-check the type leaf against the declared set; silent-skip otherwise" — exact-match implied.
- **Implemented:** `code:graph/node/template_validator.go::errorClassMatchesDeclared` (~L459–472) honors trailing `/*` wildcards from the declared set. So `declared_error_classes: ["http/server_error/*"]` accepts `error_types: { "http/server_error/500": ... }`.
- **Inferred reason:** Spec-intent override — without prefix matching, every leaf would need to be enumerated. Implementer item 13 confirmed.

### D19. `executors/verifier-http/*` and `executors/verifier-shape-checks/*` keep flat error classes

- **Plan said:** Pass 6 explicitly targeted http-node, claude-agent, postgres-stores, stub.
- **Implemented:** `code:executors/verifier-http/executor.go` (~L68, 90, 99, 108, 121) and `code:executors/verifier-shape-checks/server.go` (~L72, 76, 93) still emit flat classes (`invalid_attribute`, `http_request_failed`, `verifier_failed`).
- **Inferred reason:** Out of scope per the plan's translation-table targeting. Implementer item 14 confirmed.
- **(Resolved 2026-05-24 in post-review follow-up:** both verifier executors now emit hierarchical `verifier/*` classes (`verifier/attribute_invalid`, `verifier/network_error`, `verifier/timeout`, `verifier/check_failed[/<check_kind>]`) and register `ObservabilityCapabilities` advertising `Declared()`. New `errorclasses/` subpackages mirror http-node's pattern; drift tests added in `code:test/scenarios/bundled_executor_vocab_test.go`. The `ValidationFinding.Class` surface in `code:executors/verifier-shape-checks/validation.go` (registration-time validate-RPC findings) stays flat — different surface, not under canonical signal taxonomy.**

### D20. `ExecutorCapabilities` closure extended in-line, no `ExecutorCapsResult` struct

- **Plan said:** Task 61 — "Or use a small `ExecutorCapsResult` struct return shape if the parameter count gets unwieldy."
- **Implemented:** `code:control/controlapi/app.go::ExecutorCapabilities` (~L76) — four-return form `(declaredEvents []string, declaredErrorClasses []string, expectedAttributesSchema []byte, ok bool)`. Callers in `code:control/controlapi/templates.go` (~L122, L126, L130, L200) discard unused returns with `_`.
- **Inferred reason:** Cleaner shape — three returns plus ok is still readable; struct would have been overkill. Matches the plan's "or" option.

---

## Cross-cutting

### D21. Pass 5 Task 52 widened scope beyond the strict TODO markers

- **Plan said:** Task 52 — "For each `// TODO(signal-taxonomy Pass 5)` comment added in Pass 1 Tasks 7-12, identify the original fixed-string audit write that the comment marked for retirement. Delete that original write."
- **Implemented:** The implementer retired the fixed-string audit rows for `pure_cascade_commit` (`code:graph/scheduler/pure_cascade.go:147–161`), `named_event_emitted` (`code:runtime/runner_named_events.go:123`), `park_requested` (`code:runtime/runner_terminal_park.go:180`), the two `error` writes (`code:runtime/runner_error_policy.go:147`, `code:runtime/runner_error_policy.go:410`), and `heartbeat_lost` (`code:runtime/conductor.go:113`) — beyond what the literal Pass 1 TODO markers would have selected. Pure-cascade now emits canonical `terminal/success`. Implementer item 11 confirmed.
- **Inferred reason:** Cleaner shape — finishing the canonicalization rather than leaving free-form audit kinds in places where a canonical signal applied. `OnError`'s `"error"` audit-row write is the conspicuous exception (see D10).

### D22. `apps/crimefinder/templates/code-review-pass.yml` migrated to new shape

- **Plan said:** Task 26 — "All `*.yaml` template fixtures … that declare `subscribes:` blocks."
- **Implemented:** `code:apps/crimefinder/templates/code-review-pass.yml` (~L92, L136) rewrote to `subscribes: [{ node: X, type: "terminal/*" }]`. Confirms the implementer also covered the crimefinder app, not just test fixtures.
- **Inferred reason:** Direct execution per the plan's "every fixture file" wording.

### D23. Lingering retired-Reason labels in `invalidateSourceBucket`

- **Plan said:** Task 43 implicitly — clean up retired references.
- **Implemented:** `code:runtime/cascade_invalidate.go::invalidateSourceBucket` (~L484) keeps `case "policy_invalidate", "handler_invalidate":` for Prometheus metric label bucketing. The case body returns `"handler"` for both; no caller can pass these reasons anymore.
- **Inferred reason:** Likely accidental — dead code that survives only because the switch is closed-with-default and the dead cases just collapse into a label that's never emitted.

---

## No-meaningful-divergence on these implementer claims

The audit confirms implementer items 1–14 with the small variances noted above. Items 1 (CEL native-types), 2 (audit subpackage), 3 (HasRunForNodeInFrame), 4 (BFS retired), 5 (target: self runtime check), 7 (ReasonHandlerPass), 8 (collateral test fixture fixes), 9 (Clear/Reset rename), 10 (aggregate leaves), 11 (Task 52 wider scope), 12 (isRuntimeSynthesizedErrorClass), 13 (errorClassMatchesDeclared), 14 (out-of-scope verifiers) all match what's in the tree. Item 6 (ResolvedAction field drops) is partly inaccurate — Targets/Frame stay on `PolicyAction`, only dropped from `ResolvedAction`.

The major audit-only finding is **D10** (`OnError` path used for `acquire/unavailable` instead of the planned `applyErrorPolicy` shim, leaking a `kind="error"` audit row instead of a canonical signal). The plan was specific about routing acquire-failure through the new signal-emit surface; the runtime path didn't get that piece.
