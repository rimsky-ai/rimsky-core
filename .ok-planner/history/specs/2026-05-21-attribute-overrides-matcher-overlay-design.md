# Matcher overlay for attribute_overrides

**Date:** 2026-05-21
**Status:** Spec — design approved
**Depends on:** `.ok-planner/specs/2026-05-20-userdata-collapse-into-attributes-design.md` (the in-flight userdata-collapse work, which renames `userdata_overrides` → `attribute_overrides` and reshapes the override mechanism). This spec assumes the userdata-collapse plan has fully landed before execution begins.
**Origin sketch:** `.ok-planner/sketches/2026-05-20-instance-harness-sketch.md` — matcher-overlay primitive only; the event-tap and instance-breakpoint primitives from that sketch stay parked as separate future sketches.

## Context

The post-collapse `attribute_overrides` mechanism carries two routing dimensions:

- `by_executor[<exec>]` — overlay applied to every dispatch with the named executor.
- `by_node[<node>]` — overlay applied to every dispatch of the named template node.

Both routes are keyed by static template identifiers — names declared at registration. That's fine for the "operator tunes one node's config at instance time" pattern, but it can't differentiate among siblings of a fan-out batch.

The forcing case: a consumer's integration test registers a real template against a real rimsky stack and needs to drive its DAG to terminal by scripting per-(partition, iter) executor outcomes. The fan-out children share `node_type` and `executor`, so `by_node` and `by_executor` route every child to the same overlay. There's no static key that distinguishes "the fix-fan-out child for partition `a` at iter 1" from "the fix-fan-out child for partition `b` at iter 2" — that distinction only materializes at dispatch time, when the producer's `SplitScope` has run and each child carries its `child_key` and resolved attribute frame.

The structural answer is a third routing dimension keyed by a dispatch-time matcher predicate over identity (executor, node, graph, child_key, resolved attrs). Forking the template per-child can't replace this: the fan-out shape is the unit under test; the partition count and identities are data-dependent; and a per-child template variant explodes combinatorially with the partition matrix.

Sibling primitives discussed in the source sketch (event tap, instance breakpoint) are deliberately out of scope. They cover different surfaces (observation, pause-points) and warrant their own specs.

Pre-v1 break-freely: no migration shim; no deprecated path; treat as if rimsky has always had `by_match`.

## Summary

`col:rimsky_instances.attribute_overrides` gains a third top-level key `by_match` — an ordered list of `{matcher, overlay}` entries. The matcher is an equality-only predicate over a fixed five-key set; matching entries fold their overlays in declaration order via `DeepMergeJSON` as a fifth merge layer (L5). The merge result is validated against the effective attribute schema by the post-collapse pipeline's existing JSON Schema gate.

Per-entry match counters persist on a new column `col:rimsky_instances.attribute_overrides_match_counts`. The supervisor increments synchronously after `applyAttributeOverrides` returns, in a short dedicated transaction separate from the already-committed dispatch-row write. Tests and operators read the counter via the existing `GET /instances/{id}` response and assert on which entries fired — making unused-entry detection a loud signal rather than a silent miss.

The matcher grammar is intentionally tiny: five keys, equality only, ordinal addressing rejected. `child_key` is the recommended anchor for per-child fan-out routing (per `concept:fan-out`'s opaque, content-derived partition key); `attrs.<path>` covers non-fan-out differentiation.

No protobuf changes. No template-registration changes. No executor-visible changes. The matcher overlay is a rimsky-internal extension between the control-API instance-create surface and the supervisor's dispatch hot path.

## Architecture

### Wire shape

```jsonc
{
  "by_executor": { "<exec>": { "<attr>": <value>, ... } },          // L3 (existing, post-collapse)
  "by_node":     { "<node>": { "<attr>": <value>, ... } },          // L4 (existing, post-collapse)
  "by_match":    [                                                   // L5 (new)
    {
      "matcher": {
        "node_type": "fix-fan-out",       // optional string
        "executor":  "claude-agent",      // optional string
        "graph":     "fix-iteration",     // optional string — "main" or sub-graph name
        "child_key": "z_a:iter_1",        // optional string
        "attrs":     { "iter_num": 1 }    // optional object — primitive equality on resolved values
      },
      "overlay": { "<attr>": <value>, ... }
    }
  ]
}
```

Stored on `col:rimsky_instances.attribute_overrides` (JSONB / JSON, single column carrying all three top-level keys).

### Merge layering

The userdata-collapse spec established the post-collapse four-layer merge:

```
L1: template.defaults.attributes.by_executor[<exec>]          (registration; folded into effective schema)
L2: node.attributes.schema.properties[<attr>].default          (registration; per-node defaults)
L3: instance.attribute_overrides.by_executor[<exec>]           (runtime)
L4: instance.attribute_overrides.by_node[<node>]               (runtime)
```

This spec adds:

```
L5: instance.attribute_overrides.by_match[].overlay            (runtime; ordered list, each match folds in declaration order)
```

L5 evaluates after L4. For each `by_match` entry whose matcher predicate evaluates true against the dispatch context, the entry's `overlay` folds on top of the running merged bag via `code:foundation/shared/jsonmerge.go::DeepMergeJSON`. Walk order is declaration order; entries declared later in the list win on conflicting attribute paths.

**More-specific-wins is expressed by author-controlled declaration order**, not by a calculated specificity rank. Authors who want a "very broad" matcher to be overridable by a "very narrow" matcher place the broad matcher earlier in the list. This keeps the merge mechanism deterministic and trivially predictable; the rule is "read top to bottom, later wins."

The resulting merged bag goes through the same JSON Schema validation gate the post-collapse pipeline runs (`code:runtime/runner_dispatch.go::substituteAttributesSchema`'s final pass). Overlay values that violate a property's type are caught there, with the same `template_validation_failed` error class the rest of the pipeline uses.

### Matcher grammar

Equality-only over a fixed five-key set. No expressions, no `in`-sets, no wildcards beyond "missing key = wildcard," no ordinals.

| Key | Type | Compared against | Validation at instance-create |
|---|---|---|---|
| `node_type` | string | the dispatch's node-type | must match a node declared in the locked template (same set built for `by_node` cross-check) |
| `executor` | string | the resolved executor name | must match a declared executor used by some template node (same set built for `by_executor` cross-check) |
| `graph` | string | the graph identity of the row's declared template location — `"main"` or sub-graph name | must be `"main"` or a sub-graph name declared in the template's `graphs:` block |
| `child_key` | string | the fan-out child's `child_key` (set at sub-claim acquisition per `concept:fan-out`'s "Each child leaf run gets … `child_key = <partition_key>`" invariant) | none — `child_key` is producer-emitted; rimsky treats it as an opaque string identifier (the `partition_request` bytes that produced it are opaque per `concept:fan-out`, and the resulting key is used by rimsky only for `(parent_run_id, child_key)` uniqueness and substitution-leaf extraction) |
| `attrs` | object | resolved attribute values at dispatch | shape-only: each value is a primitive (string / number / bool); paths use dot notation; no schema cross-check |

**Semantics:**

- Missing matcher keys are wildcards (a matcher omitting `executor` matches any executor).
- Empty matcher `{}` matches every dispatch — useful for "smoke-test this overlay across the whole instance" patterns.
- All present keys must match for the entry to apply (AND across keys).
- For `attrs.<path>`: walk the dotted path in the post-L4 merged bag; the resolved value must equal the matcher value via primitive equality. Missing path → matcher fails. Non-primitive resolved value (object / array) → matcher fails. The grammar does not express equality on composites; if an author needs that, the right primitive is a separate brainstorm.
- For `child_key`: dispatches that aren't fan-out children carry no `child_key` (empty string); any matcher specifying `child_key` won't apply to them.
- For `graph`: the entry-absorbed dispatch (where a sub-graph's entry node and the calling node share runtime identity per `concept:delegation`) reports `graph = <outer graph>` — matching the row's declared template location. Tests that want to target the inner sub-graph's behavior do so by matching on the sub-graph's *internal* nodes (which are unambiguous) or by combining `node_type` with `child_key` / `attrs`.

**Matcher reads from the post-L3+L4 bag.** An L3 override that sets `attrs.iter_num = 1` makes a matcher `attrs: { iter_num: 1 }` fire — even though the source-bound resolution produced a different value. This is the right answer for the matcher's purpose (it targets what the executor will see, not what the substitution engine produced), but it's a sharp corner; the concept doc and the function's doc-comment will surface it explicitly.

**Rejected forms** at instance-create:

- Unknown top-level matcher key. (`{ "node_name": ... }` rejected — caller meant `node_type`.)
- Non-primitive value under `attrs.<path>`. (`{ "attrs": { "settings": { ... } } }` rejected — express as `attrs.settings.x`.)
- Ordinal-shaped keys: `dispatch_index`, `nth_child`, `partition_index`, `seq`. Rejected with a message pointing at `child_key` or `attrs.<path>` as the correct anchor. This explicit rejection is the spec's prophylactic against the brittleness this design exists to avoid.
- Expression-shaped values: `{ "iter_num": { "$in": [1, 2] } }` rejected. Equality only.

This is the entire grammar. Future expansions (richer predicates, multi-value matching, schema-aware path validation) go through their own brainstorm, not as "small grammar additions."

### Dispatch evaluation

The matcher fires inside `applyAttributeOverrides`, the post-collapse override-merge function already in tree at `code:runtime/attribute_overrides.go`. The function extends to receive the additional dispatch-context arguments and to walk `by_match` after the existing L3 + L4 folds, returning the merged bag *and* the list of matched entry indices for the caller to persist.

**Updated signature:**

```go
func applyAttributeOverrides(
    resolved  map[string]any,           // post-substitution + post-static-default bag
    overrides map[string]any,           // single blob: by_executor + by_node + by_match
    executor  string,
    nodeName  string,
    graph     string,                   // NEW: "main" or sub-graph name
    childKey  string,                   // NEW: "" for non-fan-out dispatches
    logger    shared.Logger,
) (merged map[string]any, matched []int)
```

The function returns matched entry indices instead of taking a counter interface. This keeps the merge pure (no persistence side effect), defers transactional concerns to the caller, and makes the function trivially testable. The caller (in `runner_dispatch.go`) batches the counter persistence into one short transaction after the merge returns — see the persistence section for the increment path.

**Evaluation loop:**

1. Clone `resolved` (existing behavior — inputs are not mutated).
2. Fold L3 (`overrides.by_executor[executor]`) via `DeepMergeJSON` if present (existing).
3. Fold L4 (`overrides.by_node[nodeName]`) via `DeepMergeJSON` if present (existing).
4. **Snapshot the post-L4 bag** as `matcherCtx`. All matcher evaluations in step 5 read `attrs.<path>` from this snapshot, not from the running `merged` bag — so prior L5 folds within the same call do not change what subsequent matchers see. This keeps matchers stateless with respect to each other; declaration order controls overlay precedence, not matcher visibility.
5. Look up `overrides.by_match` via a new `lookupMatchList` helper (returns `[]map[string]any`, the per-entry objects).
6. For each entry in declaration order:
   - Evaluate matcher against `(executor, nodeName, graph, childKey, matcherCtx)`. AND across all present matcher keys.
   - If matches: `merged = DeepMergeJSON(merged, entry["overlay"])`. Append the entry's index to `matched`.
   - If no match: continue.
7. Return `(merged, matched)`.

The downstream JSON Schema validation pass in `resolveAttributes` (`code:runtime/runner_dispatch.go::resolveAttributes`, which calls `attributes.Validate(dispatchSchema, resolved, attributes.PhaseDispatch)` *after* `applyAttributeOverrides`) operates on the returned bag, catching overlay-introduced type errors uniformly with the existing layers' errors.

**Dispatch context surfacing.** The supervisor's acquisition struct (`acquisition` in `code:runtime/runner_acquire.go`) gains two new fields:

- `GraphName string` — populated to `"main"` for main-graph dispatches and to the sub-graph name for internal-sub-graph dispatches. Derivation: the acquisition path consults the bound template's `Graphs` list (`code:foundation/spec/template.go::TemplateSpec.Graphs`) and finds the `GraphSpec` whose `Nodes` contains the dispatching `NodeType`; the resulting `GraphSpec.Name` (or `spec.MainGraphName`) is the value. For entry-absorbed dispatches (where a sub-graph's entry node shares runtime identity with the calling node per `concept:delegation`), the outer graph wins — the row's declared template location. The lookup is local to acquisition; no new persistence column on `rimsky_nodes` or `rimsky_node_runs`.
- `ChildKey string` — populated to the producer-emitted partition key (set at sub-claim acquisition per `concept:fan-out`); empty string for non-fan-out dispatches. Already present in tree via `RunTreeRow.ChildKey` / `SubClaim.PartitionKey`; the acquisition step copies it through.

The single existing call to `applyAttributeOverrides` at `code:runtime/runner_dispatch.go:422` extends to pass the new arguments and to receive the matched-indices return value, which it then hands to the persistence increment call described in the persistence section.

### Validation

The control-API validator that gates `POST /instances` (post-collapse: `code:control/controlapi/attribute_overrides.go::validateAttributeOverrides`) extends to accept `by_match` as a third top-level key.

**Signature extension.** The validator's current signature is `(overrides, templateNodes, executors)`. The `graph` matcher key requires access to the template's declared graphs, which `templateNodes` alone cannot supply. The signature extends to `(overrides, templateNodes, templateGraphs, executors)` — where `templateGraphs []spec.GraphSpec` is the template's `Spec.Graphs` list. The single call site at `code:control/controlapi/instances.go:239` updates to pass `row.Spec.Graphs` alongside the existing `row.Spec.Nodes`.

**Per-entry shape validation:**

1. Each entry in `by_match` must be an object with exactly two keys: `matcher` (object) and `overlay` (object). Any other shape rejected.
2. `matcher` keys: only `node_type`, `executor`, `graph`, `child_key`, `attrs` admitted. Unknown matcher keys rejected with a message naming the offending key. The ordinal-shaped keys (`dispatch_index`, `nth_child`, `partition_index`, `seq`) get loud rejections with a message redirecting the author to `child_key` or `attrs.<path>`.
3. **Per-key cross-checks:**
   - `node_type`: must equal the `Type` of some node in the locked template.
   - `executor`: must be a declared executor referenced by some template node.
   - `graph`: must be `"main"` (matching `spec.MainGraphName`) or a `GraphSpec.Name` declared in `templateGraphs`. For legacy flat-Nodes templates (which the canonicalizer still accepts pre-v1 per `code:graph/node/template_validator_graphs.go`), `templateGraphs` is empty; the validator treats this case as implying a single `"main"` graph, accepting `graph: "main"` and rejecting any other value with a message ("template has no declared sub-graphs; only `graph: \"main\"` is valid").
   - `child_key`: string, no cross-check (opaque).
   - `attrs`: object whose values are primitives (string / number / bool); paths use dot notation; no schema cross-check.
4. `overlay` shape: object with attribute-name keys. Fragment values are not inspected (preserves the structural-inertness discipline that `concept:instance` carries post-collapse, same as L3/L4).

**Empty `by_match: []`** is accepted (semantic no-op — same as omitting the key entirely).

**Sentinel error**: continues to be `errAttributeOverridesInvalid` (post-collapse rename of today's `errUserdataOverridesInvalid`). Validation errors map to HTTP 400 via the existing handler wiring. New error messages use `wrapInvalidf` with `%q` verbs for any user-supplied JSON keys, preserving the HTTP-injection-safe discipline the validator already enforces.

**Audit logging**: `overridePresentKeys` (the helper that extracts override key names for structured logs without leaking fragment bytes) gains a third return: `byMatchCount int`. The audit log line per instance-create captures the count plus a per-entry matcher-key fingerprint (which keys each entry uses) — never the overlay fragment bytes.

### Persistence

Two columns on `table:rimsky_instances`:

- **`attribute_overrides`** (existing, post-collapse): JSONB / JSON column already holding the override blob. The `by_match` array rides inside this same JSONB. No schema change beyond the new key being accepted; existing instances without `by_match` continue to round-trip.

- **`attribute_overrides_match_counts`** (new): JSONB / JSON column holding an integer array indexed by `by_match` entry position. Shape: `[<int>, <int>, ...]`. Empty array (`[]`) for instances with no `by_match` entries.

  - Initialized at instance-create. The control-API handler sets it to `[0, 0, ...]` of length `len(by_match)` when the instance row is inserted. Absent or empty for instances without `by_match`.
  - Incremented in a short, dedicated transaction by the supervisor after `applyAttributeOverrides` returns, using the matched-indices list as input. The increment is *not* nested inside the dispatch-row write (which has already committed via `transitionToRunning` by the time `resolveAttributes` invokes the merge function); it is its own short transaction.
  - Read via the existing `GET /instances/{id}` response (the existing accessor pulls the row's columns; this is one more JSON field on the response).

**Persistence API.** A new method on the `Instances` interface (`code:foundation/persistence/instances.go::InstanceTable`):

```go
IncrementAttributeOverrideMatchCounts(ctx context.Context, instanceID uuid.UUID, indices []int, tx Tx) error
```

The `tx Tx` parameter follows the prevailing convention on the rest of the interface (`MarkTerminated`, `CountActiveByTemplate`, etc.). When called from the dispatch path, the supervisor passes a freshly-opened short transaction (or `nil` to let the implementation open one internally — pick a single convention in the plan). Batching all matched indices into a single call lets the backend update the array elements in one statement.

**Concurrency**: two concurrent fan-out child dispatches incrementing the same array element each open their own short transaction with row-level locking. Postgres uses `jsonb_set` against the array index inside a `SELECT ... FOR UPDATE` cursor; SQLite under `BEGIN IMMEDIATE` serializes transactions naturally. Counter is monotonic increment; no read-modify-write race that matters semantically.

**Counter semantics under dispatch failure.** The counter increments on *match*, not on successful dispatch. A matcher that fires and folds an overlay containing a type-invalid value will increment the counter even though the subsequent JSON Schema validation rejects the dispatch. This is the right semantics: the counter is "did this matcher's predicate evaluate true," answering the test-author's question of whether the matcher was wired correctly. Dispatch outcome is observed separately via run state and event log.

**Migration**: new migration in each backend adding the column with `DEFAULT '[]'`. Pre-v1 break-freely; no compat shim.

### Components

**`runtime/attribute_overrides.go`** — extend `applyAttributeOverrides` per the signature in `## Dispatch evaluation`. New helper `lookupMatchList` parallel to `lookupFragment` (returns `[]map[string]any` from `overrides["by_match"]`). Matcher-evaluation logic implemented as a private helper `evaluateMatcher` that takes the matcher object plus the dispatch context (executor, nodeName, graph, childKey, matcherCtx snapshot) and returns a bool. The function returns `(merged, matched)` — no persistence side effects. The `evaluateMatcher` helper carries an `@concept: inertness` annotation citing the new sanctioned read site, and `applyAttributeOverrides` retains its existing `@concept: attribute` annotation (now extended to cover L5).

**`runtime/runner_acquire.go`** — `acquisition` struct gains `GraphName string` and `ChildKey string` fields. `GraphName` is populated via the lookup against the bound template's `Spec.Graphs` (find the `GraphSpec` whose `Nodes` contains the dispatching `NodeType`); entry-absorbed dispatches resolve to the outer-graph name. `ChildKey` is copied from the run-tree / sub-claim state already in tree. Both are populated at the same call sites that today populate `Executor` and `NodeType`.

**`runtime/runner_dispatch.go`** — the existing call at line 422 extends to pass the new arguments and to receive `(merged, matched)`. After the merge, if `len(matched) > 0`, the supervisor calls `persistence.IncrementAttributeOverrideMatchCounts(ctx, instanceID, matched, tx)` with a short dedicated transaction (or `nil` if the implementation owns the tx — pin in plan).

**`control/controlapi/attribute_overrides.go`** — `validateAttributeOverrides` extends to accept `by_match` and gains a fourth parameter `templateGraphs []spec.GraphSpec` (see `## Validation`). New helper `validateMatchEntry` for per-entry shape validation. `overridePresentKeys` gains the third return.

**`control/controlapi/instances.go`** — `POST /instances` handler passes `row.Spec.Graphs` to `validateAttributeOverrides` (the call site at line 239 takes one additional argument). Handler initializes `attribute_overrides_match_counts` to a zero-filled array of length `len(by_match)`, or `[]` when `by_match` is absent or empty. `GET /instances/{id}` response includes the new field.

**`foundation/persistence/instances.go`** — `InstanceRow` gains `AttributeOverridesMatchCounts []int64` with JSON tag `attribute_overrides_match_counts,omitempty`. New method on the `InstanceTable` interface: `IncrementAttributeOverrideMatchCounts(ctx context.Context, instanceID uuid.UUID, indices []int, tx Tx) error` (signature matches the prevailing `tx Tx` convention on the rest of the interface).

**`foundation/persistence/postgres/instances.go`** — accessor methods (`Get`, `Insert`, `List`, supervisor reader) thread the new column. `IncrementAttributeOverrideMatchCounts` builds a single `UPDATE` statement that uses `jsonb_set` (or `jsonb_build_array` + an array-element-wise add) against the indices in one round-trip.

**`foundation/persistence/sqlite/instances.go`** — same shape; SQLite uses `json_set` with `$[<idx>]` path. Implementation may serialize index updates inside a single `BEGIN IMMEDIATE` block.

**`foundation/persistence/postgres/migrations/`** — new migration adding `attribute_overrides_match_counts JSONB NOT NULL DEFAULT '[]'` to `rimsky_instances`.

**`foundation/persistence/sqlite/migrations/`** — new migration adding `attribute_overrides_match_counts TEXT NOT NULL DEFAULT '[]'` (SQLite represents JSON as TEXT).

**`foundation/persistence/conformance/instances_attribute_overrides.go`** — conformance test extends with `by_match` cases. Validator accepts well-formed entries; rejects unknown / ordinal-shaped matcher keys; counter increments correctly under concurrent dispatches.

**`graph/node/template_validator.go`** — no changes. The matcher overlay attaches to instances, not templates.

**Protobuf** — no `.proto` changes. The matcher overlay flows through the existing `POST /instances` HTTP surface; nothing reaches the executor / observability wire formats.

## Data flow

End-to-end dispatch under the new model:

1. **Instance creation.** Operator POSTs `attribute_overrides` body including `by_match`. `validateAttributeOverrides` accepts the third top-level key, validates each entry per the rules above. Handler initializes `attribute_overrides_match_counts` to `[0, 0, ...]` of length `len(by_match)`.

2. **Dispatch.** Supervisor's acquisition path populates `acquisition.GraphName` and `acquisition.ChildKey`. `substituteAttributesSchema` resolves source-bound + static-default values into `resolved`. `applyAttributeOverrides` is invoked with the resolved bag, the override blob, and the dispatch context; it returns `(merged, matched)`.

3. **L3 + L4 fold.** Existing behavior — by-executor and by-node overlays fold via `DeepMergeJSON`.

4. **L5 walk.** Each `by_match` entry's matcher evaluates against the post-L4 snapshot. Matching entries fold their overlays into `merged` in declaration order; the entries' indices accumulate in `matched`. `applyAttributeOverrides` returns `(merged, matched)`.

5. **Counter persistence.** If `len(matched) > 0`, the supervisor invokes `persistence.IncrementAttributeOverrideMatchCounts(ctx, instanceID, matched, tx)` in a short dedicated transaction (separate from the already-committed dispatch-row write). The increment is best-effort observability — see `## Error handling` for the failure disposition.

6. **Schema validation.** Final JSON Schema validation pass in `resolveAttributes` (calling `attributes.Validate(dispatchSchema, resolved, attributes.PhaseDispatch)`) catches type errors from any layer, surfaced as `template_validation_failed` with the offending property path.

7. **Executor receives the merged attribute bag** through the existing `ExecuteRequest.attributes` field. The matcher overlay is invisible to the executor; it just sees the final values.

8. **Instance terminal.** `GET /instances/{id}` returns `attribute_overrides_match_counts`. Tests assert on the array. Operators inspect to detect drift between intended matchers and actual dispatches.

## Error handling

- **Validation failure at instance-create** (unknown key, malformed shape, cross-check miss): HTTP 400 via `errAttributeOverridesInvalid`. Existing handler wiring; new messages use `wrapInvalidf` with `%q` for user-supplied keys.
- **Overlay value violates schema type at dispatch**: caught by the post-collapse JSON Schema validation pass. Routes through `on_executor_errored` with `error_class: template_validation_failed`.
- **`IncrementAttributeOverrideMatchCounts` fails** (persistence error in its short tx): the dispatch row has already committed via `transitionToRunning`; the run continues normally. The supervisor emits a structured WARN (`instance.attribute_overrides_counter_increment_failed`) carrying the instance ID and the failed-to-increment indices. Counter loss does not affect dispatch correctness — the counter is observability, not control. Pre-v1 break-freely: the operational impact of a missing increment is a test that incorrectly believes a matcher did not fire; the noise from the WARN is the recovery signal.
- **Matcher attribute path missing or non-primitive**: matcher silently doesn't match (per the `## Matcher grammar` section's semantics). The counter for that entry doesn't increment, surfacing as a zero at instance terminal — the unused-entry signal.

## Testing

### Unit tests

**`runtime/attribute_overrides_test.go`** — extends existing file:

- Empty `by_match` is a no-op (function behaves identically to a blob with only `by_executor`/`by_node`).
- Single matcher with `node_type` only matches the right dispatches.
- Multiple matcher keys AND together (a matcher with `node_type` + `attrs.iter_num` requires both).
- Empty matcher `{}` matches every dispatch.
- Declaration order — when two entries match the same dispatch, the later entry's overlay wins on conflicting attribute paths; non-conflicting paths from both apply.
- `child_key` matching: present only on fan-out children; absent dispatches don't match a matcher specifying `child_key`.
- `graph` matching: `"main"` matches main-graph dispatches; sub-graph name matches internal-sub-graph dispatches; entry-absorbed dispatches report outer-graph identity.
- `attrs.<path>` against resolved values: equality on string/number/bool; missing path → no match; non-primitive resolved value → no match.
- Matcher reads from post-L4 bag (an L3/L4 override that sets `attrs.iter_num = 1` makes a matcher targeting that value fire).
- `matched` return value contains the correct entry indices for matching entries; empty for no-match cases.
- Non-mutation of the input matcher list during evaluation.
- Non-mutation invariant — `resolved` and `overrides` unchanged after the call.

**`control/controlapi/attribute_overrides_test.go`** — extends existing file:

- `by_match` accepted as a third top-level key alongside `by_executor` / `by_node`.
- Per-key cross-checks: unknown `node_type`, unknown `executor`, unknown `graph` rejected with clear messages.
- Loud rejections for ordinal-shaped keys (`dispatch_index`, `nth_child`, `partition_index`, `seq`) and unknown matcher keys.
- Non-primitive `attrs.<path>` value rejected.
- Empty `by_match: []` accepted (semantic no-op).
- Overlay fragment values not inspected (HTTP-injection-safe).
- `overridePresentKeys` returns the correct `byMatchCount`.

**`foundation/persistence/conformance/instances_attribute_overrides.go`** — extends existing conformance file. Backend test coverage for instance rows lives in this conformance harness (there are no per-backend `instances_test.go` files; the conformance test is the single source of truth that runs against both Postgres and SQLite):

- New column round-trips through `Insert` / `Get` / `List`.
- `IncrementAttributeOverrideMatchCounts(instanceID, [i, j])` writes the correct array elements; reading back shows the increments.
- Concurrent `Increment` calls against the same instance, different indices: both land.
- Concurrent `Increment` calls against the same instance, same index: both land (counter is monotonic; no lost update).
- Same `by_match` shape validation cases run against both backends.
- Counter increment behavior matches across backends.

### Scenario tests (`test/scenarios/`)

- **Fan-out partition routing scenario.** Template declares a fan-out node with three children (partitions `"a"`, `"b"`, `"c"`). Instance creates with three `by_match` entries keyed by `child_key`, each setting a different attribute overlay. Verify each child dispatch sees the matching overlay; the counter shows `[1, 1, 1]` at instance terminal.

- **Sub-graph routing scenario.** Template has a `main` graph and a `fix-iteration` sub-graph; both contain a node-type that fires in both contexts. Two `by_match` entries: one with `graph: "main"`, one with `graph: "fix-iteration"`. Verify each dispatch lands the right overlay; entry-absorbed dispatches follow the outer-graph disposition.

- **Declaration-order specificity scenario.** Two `by_match` entries match the same dispatch; their overlays touch overlapping attribute paths. Verify the later entry wins on conflicts.

- **Unused-entry observability scenario.** Instance creates with five `by_match` entries; only two actually match dispatches during the instance's run. At terminal, `GET /instances/{id}` shows `attribute_overrides_match_counts` with nonzero positions only for the two that fired.

- **Validation-rejection scenario** (negative). `POST /instances` with a matcher specifying `dispatch_index: 2` returns HTTP 400 with a clear message pointing at `child_key` or `attrs.<path>` as the right anchor.

These scenarios exercise the testcontainers-backed Postgres path; SQLite parity is covered by the conformance file.

### Conformance harness

No changes to `cmd/rimsky-executor-conformance`. The matcher overlay is rimsky-internal; the executor wire surface is unaffected.

### Race coverage

`go test -race -count=3` against `foundation/persistence/postgres/...` and `runtime/...` catches any counter-update ordering issues on the dispatch hot path.

## Design changes

Mutations execute-plan will apply, assuming the userdata-collapse spec has fully landed before this plan begins.

**Mutate `.ok-planner/design/concepts/attribute.md` in place.**

1. **Append a new bullet to `## Invariants`** (after the per-directive-strict-default invariant added by userdata-collapse):

   > A fifth override layer (L5) extends the four-layer merge: `instance.attribute_overrides.by_match` is an ordered list of `{matcher, overlay}` entries. The matcher predicate is equality-only over a fixed key set (`node_type`, `executor`, `graph`, `child_key`, `attrs.<path>`); evaluated against the dispatch context at runtime; missing keys are wildcards; AND across present keys. Each matching entry's overlay folds on top via `DeepMergeJSON` in declaration order — later entries win. Empty matcher (`{}`) matches every dispatch. The matcher reads from the post-L4 merged bag (overrides applied through L4 are visible to the matcher). Ordinal-shaped matcher keys (`dispatch_index`, `nth_child`, `partition_index`, `seq`) and expression-shaped values are rejected at registration. Enforced at `code:control/controlapi/attribute_overrides.go::validateAttributeOverrides` and `code:runtime/attribute_overrides.go::applyAttributeOverrides`.

2. **Append a new `## Matcher overlay (by_match)` section** after the `## Static-default properties` section (added by userdata-collapse), before `## Notes`. (The L5 layering content lives entirely inside this new section; the userdata-collapse spec does not add an `## Override layering` section to the concept doc, so there is no existing list to extend.)

   > ## Matcher overlay (by_match)
   >
   > A third routing dimension on `attribute_overrides`, alongside the static `by_executor` (L3) and `by_node` (L4) maps. `by_match` is an ordered list of `{matcher, overlay}` entries where the matcher is a content-keyed predicate over dispatch-time identity — solving the problem that static routes can't differentiate among children of a fan-out node that share node type and executor.
   >
   > The matcher grammar is intentionally small: equality only, over a fixed key set. `child_key` is the recommended anchor for fan-out routing (the producer-emitted per-sub-scope identifier from `concept:fan-out`, stable across dispatch reorderings); `attrs.<path>` covers non-fan-out differentiation. Ordinal-style addressing (any "third call" / "index N" semantics) is rejected at registration: matchers address partitions by identity, never by execution order.
   >
   > Override values are static — no substitution applied. The matcher reads from the post-L3+L4 bag, meaning earlier-layer overrides are visible to the matcher's `attrs.<path>` comparisons.
   >
   > Per-entry match counters persist on `col:rimsky_instances.attribute_overrides_match_counts`. The supervisor increments after the merge returns, in a short dedicated transaction. Operators and tests read the counter via `GET /instances/{id}` and assert on which entries fired. Entries that never match show 0 at instance terminal — the "silent miss becomes loud miss" discipline that makes matcher-overlay testing safe against producer key-scheme changes.

3. **Append a new entry at the bottom of `## Notes`**:

   > 2026-05-21 — Matcher overlay (L5 `by_match`) added per `.ok-planner/specs/2026-05-21-attribute-overrides-matcher-overlay-design.md`. Equality-only matcher grammar over `{node_type, executor, graph, child_key, attrs.<path>}`. Per-entry match counter persisted on `attribute_overrides_match_counts`.

**Mutate `.ok-planner/design/concepts/inertness.md` in place.**

The matcher overlay introduces a narrow new read site against *attribute values* (equality match in `attrs.<path>`). Today's structural-inertness bullet enumerates "pattern-matches" among the disallowed operations; this needs to be tightened to allow the matcher's evaluation as a precisely-named sanctioned site, parallel to how `walkPath` is enumerated.

1. **Replace the "Structural inertness" bullet** (currently at line 24 in the file, the second bullet under the `## What it is` "Read-site sub-disciplines" subsection; begins "**Structural inertness** — rimsky may traverse the bytes for transport mechanics (event-log persistence, JSON-walk substitution) but does NOT inspect values to make decisions. Applies to: attribute values, named-event payloads, message payloads, `Error.payload`. Rimsky reads them only at substitution leaves and event-ledger writes; never logs, formats with `%v`, validates beyond schema gates, transforms, normalizes, hashes, indexes, **pattern-matches**, attaches to traces, or includes them in error messages.") with:

   > **Structural inertness** — rimsky may traverse the bytes for transport mechanics (event-log persistence, JSON-walk substitution) and for the precisely-enumerated sanctioned read sites below, but does NOT inspect values to make routing or validation decisions outside those sites. Applies to: attribute values, named-event payloads, message payloads, `Error.payload`. Rimsky reads them only at the sanctioned read sites; never logs, formats with `%v`, validates beyond schema gates, transforms, normalizes, hashes, indexes, attaches to traces, or includes them in error messages.

2. **Append a new sanctioned read site** to the bullet list under "Sanctioned read sites:" (currently at lines 42-47 in the file):

   > - `evaluateMatcher` (`code:runtime/attribute_overrides.go`, the matcher-evaluator helper called from `applyAttributeOverrides`) — applies to attribute values only. Reads the resolved post-L4 attribute bag to evaluate `attrs.<path>` equality predicates from `attribute_overrides.by_match[].matcher`. The read is primitive-equality only; no traversal beyond the named path; values not logged, not formatted, not included in error messages. Sanctioned by `concept:attribute`'s L5 matcher-overlay invariant.

3. **Append a new entry at the bottom of `## Notes`**:

   > 2026-05-21 — Matcher overlay added per `.ok-planner/specs/2026-05-21-attribute-overrides-matcher-overlay-design.md`. New sanctioned read site (`evaluateMatcher`) reads resolved attribute values for equality matching. Structural-inertness bullet at line 22 tightened to explicitly allow sanctioned-site reads while preserving the general "no value-driven decisions" discipline.

The matcher's evaluator carries an `@concept: inertness` annotation at the new site, citing this concept doc as the sanctioning surface.

**Mutate `.ok-planner/design/concepts/instance.md` in place.**

1. **Replace the `attribute_overrides` validation invariant** (the bullet that the userdata-collapse spec rewrites to read "`attribute_overrides` validation inspects only routing keys (`by_executor`/`by_node` plus executor/node names); fragment values are never inspected ...") with:

   > `attribute_overrides` validation inspects only routing keys (`by_executor` / `by_node` plus executor/node names; for `by_match`, matcher key names + cross-checked values for `node_type` / `executor` / `graph`); overlay fragment values are never inspected (preserves structural-inertness for attribute values). Matcher attribute paths (`attrs.<path>`) are shape-validated (primitive equality) but not schema-cross-checked — unused matchers surface via `col:rimsky_instances.attribute_overrides_match_counts`.

2. **Update the `## Boundaries` "Owns" line** (rewritten by userdata-collapse to read "params, attribute_overrides, the binding to a template hash") to:

   > Owns: the per-deployment runtime state, params, attribute_overrides (including `by_match` matcher overlays and the per-entry match-counter column), the binding to a template hash.

3. **Append a new entry at the bottom of `## Notes`** (the section the userdata-collapse spec creates):

   > 2026-05-21 — Matcher overlay (`by_match`) added to `attribute_overrides` per `.ok-planner/specs/2026-05-21-attribute-overrides-matcher-overlay-design.md`. New column `col:rimsky_instances.attribute_overrides_match_counts` (JSONB array of int64, indexed by `by_match` entry position). Incremented synchronously by the supervisor at match time; readable via `GET /instances/{id}`.

**CHANGELOG.md**: append a new bullet under `## Unreleased`:

> - **Matcher overlay for attribute_overrides.** `col:rimsky_instances.attribute_overrides` gains a third routing dimension `by_match` — an ordered list of `{matcher, overlay}` entries keyed by a dispatch-time predicate (`node_type`, `executor`, `graph`, `child_key`, `attrs.<path>`). Equality-only grammar; ordinal addressing rejected. Recommended anchor for per-child fan-out routing is `child_key`. Per-entry match counter persists on new column `attribute_overrides_match_counts` for unused-entry observability. Enables consumer tests to script per-(partition, iter, …) executor stubs against a single real template, without forking template variants per child. Structural-inertness discipline (`concept:inertness`) gains a new sanctioned read site at the matcher evaluator — narrowly enumerated, primitive-equality only. Depends on the userdata-collapse work (`attribute_overrides` rename, post-collapse merge layering). See `.ok-planner/specs/2026-05-21-attribute-overrides-matcher-overlay-design.md`.

No new concepts created; no tensions resolved or raised by this spec.

## Open questions for plan phase

These are spec-stable decisions deferred to the plan writer for implementation-question-only knobs.

1. **`acquisition.GraphName` population in error / edge paths.** The acquisition struct is populated in several call sites (main-graph dispatch, sub-graph internal, fan-out child). The plan should enumerate each site and confirm `GraphName` / `ChildKey` are populated consistently — easy to miss a path on a quick first pass.

2. **Counter-increment transaction ownership.** `IncrementAttributeOverrideMatchCounts` takes a `tx Tx` parameter per the prevailing interface convention. The plan should pin a single convention for callers: either the supervisor always opens a short tx and passes it in, or the supervisor passes `nil` and the implementation opens its own. Pick one and document it on the interface method's doc-comment so future callers don't drift.

3. **JSONB array element update SQL — exact form.** Postgres `jsonb_set` with array path syntax (`'{<idx>}'`) is the natural shape; SQLite `json_set` uses `$[<idx>]`. The plan pins the exact statements. A batched call (`Increment` with `indices []int`) can either issue one statement per index inside one tx, or one statement with `jsonb_set` chained for all indices — measure and pick.

4. **Documentation sweep.** Beyond the concept-doc mutations in `## Design changes`, the plan should sweep `docs/concepts/`, `docs/protocols/`, `docs/agents/llms.txt`, `docs/humans/landing.md`, `docs/glossary.md`, and the README for any places that document `attribute_overrides`'s shape and need updating with the third routing dimension.

5. **Migration ordering vis-à-vis userdata-collapse.** This spec assumes the userdata-collapse plan has fully landed (including its `userdata_overrides` → `attribute_overrides` rename migration) before any of this plan's migrations run. The plan should call out the dependency in its preflight checks and the `Unreleased` CHANGELOG entry.

6. **Per-entry match-counter array growth on `by_match` array mutation.** The current design assumes `by_match` is fixed at instance-create. If a future spec adds a "mutate `by_match` on a running instance" operation, the counter array needs to grow / shrink accordingly. Not in scope for this spec, but worth a one-liner in the concept doc's Notes that the array length is fixed at create-time.
