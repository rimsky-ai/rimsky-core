# Divergences — 2026-05-21 attribute_overrides matcher overlay

Audit of how the working tree differs from `.ok-planner/plans/2026-05-21-attribute-overrides-matcher-overlay.md`. Stylistic differences and trivial naming variations are omitted.

---

## Task 5 — Postgres `IncrementAttributeOverrideMatchCounts` SQL shape

**What the plan said.** Build a chained `jsonb_set` expression with one step per *position in the input slice*. The chained step references the prior expression and adds 1, using `$2..$N` parameterised text-array paths:

```
jsonb_set(<prev>, ARRAY[$%d::text], to_jsonb(coalesce((<prev>->>$%d::text)::int, 0) + 1))
```

**What was implemented** (`code:foundation/persistence/postgres/instances.go::IncrementAttributeOverrideMatchCounts`). The Go side first aggregates duplicate indices into a `deltas map[int]int` and emits one `jsonb_set` per *unique* index, with the aggregated count inlined as a SQL literal:

```
jsonb_set(<prev>, ARRAY['%d'], to_jsonb(coalesce((<prev>->>%d)::int, 0) + %d), false)
```

Key shape differences from the plan:

- One `jsonb_set` per unique index (aggregated in Go) rather than one per input occurrence.
- Path/index/delta inlined as `fmt.Sprintf` literals — only `$1` (instanceID) remains parameterised.
- `->>` is given a numeric integer literal (`%d`) rather than a text-cast path arg. The plan's text-cast form (`->>$N::text`) does not work against JSONB arrays — `->>` with a text argument addresses object keys, not array elements; numeric literals are required for array indexing on the read side.
- Adds the explicit `create_missing=false` (4th `jsonb_set` arg) so out-of-range indices become silent no-ops, matching the plan's intent.

**Inferred reason.** Implementer's stated rationale (in the function's doc-comment): "PostgreSQL's expression evaluator does NOT guarantee that two textually-distinct `jsonb_set(col, '{N}', ...)` subexpressions referring to the SAME path will compose left-to-right — duplicated paths in a chained expression can collapse so only one increment lands. Aggregating in Go and emitting one jsonb_set per unique index sidesteps the issue without changing the per-occurrence semantic." The `->>` array-indexing correction is a load-bearing postgres-specific fix to plan SQL that would not have worked as written. The per-occurrence semantic the plan pinned (Task 8: "duplicates inside one call increment that many times") is preserved by the aggregation.

---

## Task 6 — SQLite `IncrementAttributeOverrideMatchCounts` SQL shape

**What the plan said.** Chain one `json_set` per position-in-input-slice with `'$[i]'` path syntax and `+ 1`:

```
json_set(<prev>, '$[i]', coalesce(json_extract(<prev>, '$[i]'), 0) + 1)
```

**What was implemented** (`code:foundation/persistence/sqlite/instances.go::IncrementAttributeOverrideMatchCounts`). Same per-unique-index aggregation pattern as postgres, emitting one `json_set` per unique index with the aggregated delta:

```
json_set(<prev>, '$[i]', coalesce(json_extract(<prev>, '$[i]'), 0) + <delta>)
```

**Inferred reason.** Parity with the postgres aggregation. Per the function's doc-comment: "chained json_set calls with duplicated paths can collapse, so we emit one set per unique index with the aggregated delta." Per-occurrence semantic preserved via Go-side aggregation.

---

## Task 11 / Task 12 — Single `RunTree.GetByID` fetch consolidated, plus warn-on-error structure change

**What the plan said.** Tasks 11 and 12 reorder the existing `RunTree.GetByID` call (which today fetches `ParentRunID` near line 466) above the happy-path `acquisition{...}` literal so one fetch supplies both `ParentRunID` and `ChildKey`. The plan calls this "a small structural reorder, not a new fetch." The plan suggests `if err := ...; err == nil && row != nil { childKey = row.ChildKey }` and leaves the pre-existing error-handling shape implicit.

**What was implemented** (`code:runtime/runner_acquire.go::tryAcquire#432-456`). The fetch was moved above the literals as planned; the consolidated block reads both `childKey` and `parentRunID` in the same `if-else` chain, including the existing Warn log for `GetByID` failures. The previously-below `if rt := args.Persist.RunTree(); rt != nil { ... }` block that set `out.ParentRunID` was deleted entirely. `ParentRunID` is now set in the `acquisition{}` literal directly. Both literals at the unavailable branch (formerly line 420) and the happy path (formerly line 444) receive `GraphName` + `ChildKey` from the consolidated lookup.

**Inferred reason.** Cleaner shape. The plan said the unavailable-branch literal could use `ChildKey: ""` since that path doesn't run `applyAttributeOverrides`. The implementer chose instead to populate it from the same consolidated fetch — slightly more uniform, no behavior difference. Per-branch divergence from the plan's "unavailable branch is fine with empty string" suggestion in favor of a single derivation path.

---

## Plan-omitted: `TestExecutorBlocked` flake left unfixed

**What the plan said.** Nothing — the test is unrelated to this plan's surface.

**What was implemented.** The implementer's hand-off report flagged `code:test/scenarios/executor_blocked_test.go::TestExecutorBlocked` as failing under heavy parallel load while passing in isolation. The diff does not touch this file; the flake remains in tree.

**Inferred reason.** Implementer judged the flake "unrelated to this plan" and left it. Per `submodules/rimsky/.claude/rules/rules.md`'s "Fix Every Bug You Find" rule, this is a documented divergence from project policy. Flagging here so the user can decide whether to investigate before closing this plan.

---

## Task 30 — Fan-out partition routing scenario does not drive a fan-out producer

**What the plan said.** Register a template with a fan-out node `fan` that emits three children with `child_key`s `"a"`, `"b"`, `"c"`. Create an instance with three `by_match` entries keyed by `child_key`. Assert each child saw the correct `tag`; assert `AttributeOverridesMatchCounts == [1, 1, 1]`.

**What was implemented** (`code:test/scenarios/attribute_overrides_match_overlay_e2e_test.go::TestAttributeOverridesMatchOverlay_NodeTypeMatcherFires`). Single-node template (`worker`). Three `by_match` entries keyed by `node_type` / `executor` / empty-matcher — none of them use `child_key`. The end-to-end seam (instance create → dispatch → merge → counter increment) is exercised, but partition routing by `child_key` is not. The file's package-level comment (lines 14-19) calls this out explicitly: "Fan-out partition routing (`child_key` matcher) is covered at the unit level (`runtime/attribute_overrides_test.go`) — the scenario harness does not yet drive a fan-out producer."

**Inferred reason.** Forced choice — the scenario harness does not currently drive a fan-out producer. Implementer chose to pin the L5 fold + counter-increment seam end-to-end at the scenario level while leaving `child_key` matcher coverage at the unit-test level.

---

## Task 31 — Sub-graph routing scenario does not exercise a sub-graph

**What the plan said.** Template with a `main` graph and a `worker` sub-graph; both contain a node-type `pass`. Two `by_match` entries (`graph: "main"` and `graph: "worker"`). Assert main-graph `pass` dispatches see `where=outer`; sub-graph internal `pass` dispatches see `where=inner`. Verify entry-absorbed dispatch behavior.

**What was implemented** (`code:test/scenarios/attribute_overrides_match_overlay_subgraph_e2e_test.go::TestAttributeOverridesMatchOverlaySubgraph_FlatTemplateResolvesToMain`). Flat single-node template; one `by_match` entry with `graph: "main"`. Asserts the matcher fires and the counter increments. The file's package-level comment (lines 8-20) calls this out: "The scenario harness does not yet drive sub-graph delegation end-to-end (sub-graph dispatch is exercised at the runtime unit level in `runtime/subgraph_dispatch_test.go`). What this scenario pins is the *acquisition-time* graph resolution seam: a flat-Nodes template resolves to `graph: \"main\"` ... Internal-sub-graph routing ... is covered by unit tests against the matcher evaluator + `lookupGraphName` helper."

**Inferred reason.** Forced choice — sub-graph delegation is not exercised by the scenario harness. The scenario pins only the acquisition-time `lookupGraphName` flat-template fallback. Sub-graph-name matching, internal-node routing, and entry-absorbed disposition are not covered end-to-end.

---

## Task 24 — Validator test cases expanded beyond the plan's bullets

**What the plan said.** 18 bullets covering the `by_match` validation surface. Ordinal-shaped keys were enumerated as one bullet ("ordinal-shaped matcher key (dispatch_index, nth_child, partition_index, seq) → 400 redirecting...").

**What was implemented** (`code:control/controlapi/attribute_overrides_test.go`). 21 `by_match` cases. The expansion is each ordinal key getting its own subtest (`dispatch_index`, `nth_child`, `partition_index`, `seq` — 4 cases vs the plan's 1 collapsed bullet). All other planned bullets are present. Implementer's report cited "~20 subcases" — actual count is 21.

**Inferred reason.** Cleaner per-key coverage. Each ordinal key has its own error message, so per-key cases pin each message independently.

---

## Note: matched items not flagged as divergences

For traceability, items the implementer's report flagged that match the plan literally are NOT recorded above. These include:

- Doc sweep (Task 29): the two docs documenting `attribute_overrides` (`docs/executors/claude-agent/expected-attributes.md`, `docs/mcp-servers/control-api/README.md`) were both updated in place. No new docs created. Matches plan.
- Unit-test subtest counts (Task 16): 11 `TestApplyAttributeOverrides_ByMatch` subtests, matching the 11 plan bullets.
- All call-site updates to `applyAttributeOverrides` (34 in `runtime/attribute_overrides_test.go`) thread the new `graph` + `childKey` args with `"main"` / `""` defaults as planned.
- Acquisition struct, validator signature change, `provisionArgs` plumbing, `instanceItem` field, CHANGELOG entry, and all three concept-doc mutations land per plan text.
