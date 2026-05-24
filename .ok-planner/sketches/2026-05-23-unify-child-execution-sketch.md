# Unify sub-graph and fan-out under one child-execution primitive — Design Sketch

**Date:** 2026-05-23
**Status:** Sketch (not a spec; not authorization to build)

## Idea

A single sub-graph call is conceptually a fan-out of one — one partition, empty `partition_key`, "carry verbatim" aggregation policy. The downstream RunScope/closure/aggregation machinery the runtime needs is the same in both cases. Today the code has it duplicated: two emission sites and two settlement paths sit in parallel files, each handling the structurally identical work. The just-landed RunScope-first reshape made the duplication newly visible — pre-RunScope the two had different inline disambiguators on `rimsky_node_runs` and weren't structurally parallel; now they are.

Unifying the two would eliminate the duplication and give the platform a single mental model for "parent dispatches one-or-more children into their own execution contexts." Templates keep `delegate:` and `fan_out:` as separate ergonomic surfaces (different intent shapes for the author); the canonicalizer translates both into the same internal child-execution declaration.

## Shape

### The unified concept

One concept — **child execution** — replaces the structural overlap between `concept:delegation` and `concept:fan-out`. The unified concept owns:

- The parent → child(ren) RunScope relationship (per `concept:run-scope`)
- The N≥1 child dispatch shape
- The settlement primitive (children terminate → aggregate via policy → close child RunScopes → settle parent)

`delegation` and `fan-out` survive as two **invocation patterns** on top of the unified primitive, distinguished by their template surface and their canonicalizer-time defaults:

| Pattern    | Template surface | Partition count                                          | Partition keys      | Aggregation policy default | Entry absorption |
| ---------- | ---------------- | -------------------------------------------------------- | ------------------- | -------------------------- | ---------------- |
| delegation | `delegate:`      | always 1                                                 | empty               | `carry_verbatim`           | yes              |
| fan-out    | `fan_out:`       | N (producer-decided via `SplitClaimScope`)               | non-empty per child | author-specified           | no               |

The entry-absorption asymmetry is delegation-specific (one row, calling node IS the entry) and stays a property of the delegation pattern, not of child execution generally. So delegation isn't *fully* "fan-out of one" — it adds entry absorption on top — but the run-side machinery (child execution + settlement) is the same.

### The unified runtime primitive

A single function — call it `DispatchChildExecution` — replaces the run-side work of both `applyTerminalCompleteSubgraphCaller` and `CreateFanOutChildren`:

```go
// Sketch — real signature would be refined during brainstorm.
type ChildExecutionInput struct {
    ParentRun         persistence.NodeRunRow      // the calling/parent run
    ParentRunScope    persistence.RunScopeRow     // the parent's RunScope
    Partitions        []PartitionDescriptor       // 1 for delegation, N for fan-out
    AggregationPolicy AggregationPolicy           // carry_verbatim, strict, threshold, best_effort, first
    ChildGraphName    string                      // sub-graph name for delegation; parent's graph for fan-out
    EntryAbsorbed     bool                        // true for delegation; false for fan-out
}

type PartitionDescriptor struct {
    PartitionKey       string                     // empty for the delegation case
    SubClaimHandleID   *shared.UUID               // present for fan-out (sub-claim already acquired)
    PartitionInput     []byte                     // inert payload for the child
}

func DispatchChildExecution(ctx, args, tx, in ChildExecutionInput) ([]shared.UUID /* child run IDs */, error)
```

Per partition the primitive:
1. Allocates a child RunScope (`partition_key`, `parent_run_id = ParentRun.ID`, `parent_run_scope_id = ParentRunScope.ID`, `graph_name = ChildGraphName`)
2. Allocates the child's leaf-run row in that scope (via `AffirmNodeRunRow` or the equivalent eager-insert)
3. Wires the child's claim handle (if applicable) to the partition

Callers:
- Delegation case: `applyTerminalCompleteSubgraphCaller` becomes a thin wrapper that builds a one-partition `ChildExecutionInput{ EntryAbsorbed: true, AggregationPolicy: CarryVerbatim, ChildGraphName: node.Delegate, Partitions: []{{}} }` and calls `DispatchChildExecution`.
- Fan-out case: `acquireFanOutIfDeclared` (or its successor) acquires sub-claims via `AcquireSubClaims`, then builds the N-partition input and calls `DispatchChildExecution`.

### The unified settlement primitive

Today's two settlement paths converge:

- `CarryExitWriteback` (delegation): on exit's terminal, copy exit's outcome to the calling node's writeback, close the sub-graph RunScope.
- `resolveParentClaimChain` (fan-out): on each child's terminal, walk the claim tree, aggregate per policy, close partition RunScopes when the parent settles.

The unified path — call it `SettleChildExecution` — fires on every child terminal:

1. Record the child's outcome
2. Apply aggregation policy:
   - `CarryVerbatim` (delegation): the single child IS the outcome; copy verbatim to parent's writeback
   - `Strict | Threshold | BestEffort | First` (fan-out): per existing semantics
3. If the aggregation policy says "parent settles now," close the relevant child RunScopes and propagate the parent's terminal (which kicks the parent-settlement cascade bridge added during the recent reshape)

`CarryVerbatim` is just a degenerate aggregation policy that settles on N=1 with the single child's outcome. The "carry rule" stops being a separate code path and becomes a row in the policy switch.

### Persistence (no schema change)

The reshape already gave us everything the unified primitive needs:

- `rimsky_run_scopes.partition_key` — empty for delegation, non-empty for fan-out; the discriminator
- `rimsky_run_scopes.parent_run_id` + `parent_run_scope_id` — same structure both ways
- `rimsky_run_scopes.graph_name` — sub-graph name or parent's graph
- `rimsky_run_scopes.closed_at` — same closure semantics

The current `kind`-flavored language ("subgraph RunScope" vs "fanout_partition RunScope") becomes derivable: `parent_run_scope_id IS NULL → main; partition_key != '' → fan-out partition; else → sub-graph (delegation)`. The persistence layer doesn't gain or lose a column; the runtime stops treating them as separate kinds.

### Concept-doc reshape

Two options worth considering:

α. **Promote `child-execution` as a new concept; demote `delegation` and `fan-out` to "invocation patterns" docs.** The new concept owns the shared invariants (RunScope tree shape, settlement primitive, aggregation policy as a property); `delegation` and `fan-out` shrink to the surface differences (entry absorption, template field, partition cardinality default).

β. **Keep `delegation` and `fan-out` as the two concepts; add a cross-cutting "child-execution" concept that both reference.** Less disruptive to existing references; explicit that the runtime primitive is shared.

α is cleaner; β is lower churn. The sketch defers the choice to spec-time but flags α as the prescriptive direction.

Either way, `concept:run-scope` stays unchanged.

### Template-surface compatibility

Templates keep `delegate:` and `fan_out:` as separate fields. The canonicalizer continues to recognize both. Internally, both translate into the same `ChildExecutionDeclaration` representation that the runtime consumes — but template authors don't see this. No template migration needed.

### Test impact

- Existing scenarios (F1-F4 for fan-out, S1-S4 for sub-graph) stay green; they exercise the same observable behavior.
- Conformance tests for RunScope lifecycle / state isolation are unaffected.
- A new scenario or two could pin the "delegation = N=1 fan-out" claim — e.g., assert that a delegation invocation produces a RunScope with `partition_key = ""` and a single child run, vs a fan-out with N=3 producing three RunScopes with non-empty keys; both go through `DispatchChildExecution`.
- Carry-rule scenario tests get rewritten to assert against `SettleChildExecution` + `CarryVerbatim` policy rather than `CarryExitWriteback` directly.

### Migration story

No schema change. No template change. The refactor lands in three commits roughly:

1. Introduce `DispatchChildExecution` + `SettleChildExecution` primitives alongside existing code; cover with new tests.
2. Re-route `applyTerminalCompleteSubgraphCaller` and `CreateFanOutChildren` to the new primitive; re-route `CarryExitWriteback` and `resolveParentClaimChain` to `SettleChildExecution` (or have them call the new primitive internally).
3. Delete the now-empty shells; consolidate the file layout (likely a new `runtime/child_execution.go` replacing `subgraph_dispatch.go` + `fanout_dispatch.go`).

Each step is independently buildable; rollback is local.

## Open questions

- **Entry absorption stays delegation-only — does that complicate the unified primitive's signature?** Carrying an `EntryAbsorbed bool` through the input feels right, but it's an asymmetry that hints delegation might want a thin pre-step (entry-absorb + then dispatch a fan-out-of-one) rather than a single primitive parameterized by a bool.
- **Where does the sub-claim acquisition live?** Today fan-out acquires sub-claims in `AcquireSubClaims` (claim-tree side), then `CreateFanOutChildren` consumes them. Delegation doesn't have sub-claims. Does the unified primitive accept already-acquired sub-claims as input, or does it call the producer's `SplitClaimScope` itself? The first preserves existing factoring; the second is cleaner but more refactoring.
- **`AggregationPolicy` as a type — is the spec for delegation's "carry verbatim" already crisp enough?** Today the carry-rule is hard-coded behavior in `CarryExitWriteback`; promoting it to a policy value means defining the value's semantics formally (does it require N=1 by construction? what if a delegation somehow produces multiple children — error at canonicalize time?).
- **Concept-doc choice (α vs β above).** A real decision worth taking through `/refine-design` against the tension entry, not assumed here.
- **Cascade bridge interaction.** The parent-settlement cascade bridge (added in the recent reshape as Issue 12 / Phase B fix) fires `cascadeSubscribersStaleInTx` for the parent. Under the unified primitive, the bridge stays at the parent-settlement site — but does it sit inside `SettleChildExecution` or alongside it? Likely inside, but worth confirming.
- **Naming.** "child execution" is descriptive but generic. "delegation" is overloaded already. Worth considering "sub-execution," "spawned execution," or keeping the two pattern names and just naming the runtime function (`DispatchChildren` / `SettleChildren`).
- **Does sub-claim machinery (claim-tree side) need to converge too?** Delegation doesn't use sub-claims today. If we wanted the symmetry to be complete, delegation could be modeled as acquiring a single sub-claim from a "self" producer that always returns one descriptor. That's probably over-engineering; the run-side unification is the win, not the claim-side.

## Risks / unknowns

- **The asymmetries (entry absorption, sub-claim presence, aggregation default) might be larger than the symmetries.** If pulling them into a single primitive makes the primitive look like a switch on `pattern`, the unification has cost without buying parsimony.
- **`CarryExitWriteback`'s "atomic with sub-graph RunScope closure" invariant has subtle ordering.** The exit's terminal closes the scope; the calling node's writeback updates in the same tx. Folding this into `SettleChildExecution` needs to preserve the atomicity without accidentally widening or narrowing the tx boundary.
- **The recent reshape's parent-settlement cascade bridge is fresh code.** Refactoring through it before the bridge is well-tested in production risks compounding bugs. Probably worth letting the reshape settle (one or two operator cycles) before pursuing this.
- **Test rewriting cost.** S1-S4 scenarios are written against `CarryExitWriteback` directly; they'd need re-targeting. Not hard, but real.
- **Mental-model cost vs win.** Some operators / future contributors might find "delegation and fan-out are separate things" easier to learn than "they're patterns of one underlying primitive." The win is in the codebase, not necessarily in the docs.

## What this is not

- Not a migration story for `rimsky_node_runs.parent_run_id` / `child_key` — those columns are already gone (RunScope-first reshape).
- Not a template-surface change — `delegate:` and `fan_out:` keep their existing shapes.
- Not a re-derivation of aggregation policies — the existing `strict | threshold | best_effort | first` set stays, plus `carry_verbatim` added.
- Not a claim-tree change — the claim-handle machinery (`AcquireSubClaims`, `ListByClaimHandleID`, etc.) stays as-is; the unification is run-side only.
- Not a proto / wire change — `ExecuteRequest` / `Park` / `ExecutorReply` are unaffected.
- Not a rework of the just-landed RunScope-first reshape — this builds on it.
