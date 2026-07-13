# Intent Dossier: delegation

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- Delegation is a node invoking a sub-graph via `delegate: <graph-name>` (mutually exclusive with `executor:`); the delegating node settles only once the sub-graph settles, with the sub-graph's terminal outcome propagated back.
- **Fan-out and delegation are distinct mechanisms, not variants of one primitive** (2026-06-23, user, final word). Fan-out clones the calling node N times and does not aggregate attribute results; delegation substitutes into the entry node and retrieves results from the single exit node. A cloned fan-out node can itself delegate — composition, not identity. Settle is intentionally split (the fan-in shapes differ); only dispatch shares a thin helper.
- Settlement mode is implicit in the invocation: delegation settles by **carry** — the exit node's attributes are copied verbatim onto the calling node's attribute row — with no aggregation policy involved.
- Entry absorption is structural: the entry node is absorbed into the calling node (same `rimsky_nodes` row, same executor, same dispatched run; entry-alias references resolve to the calling node; the caller's `holds:` merge into the absorbed entry). Exit identity is conceptual: the exit keeps its own row; only its writeback carries to the parent.
- Sub-graphs are sealed — no closure semantics; calling-graph state reaches the sub-graph only by explicit threading through the calling node.
- Each delegation invocation runs in its own sub-graph RunScope (parent = calling node's RunScope and run), created atomically with the entry-success internal cascade, closed when the exit terminates and the carry-rule fires.

## Required behaviors (open promises)

- Templates declare a top-level `graphs:` block (`main` reserved); sub-graphs declare entry and exit nodes; a node invokes one via `delegate:`, mutually exclusive with `executor:` (2026-05-15, data-platform-extensions, artifact).
- Delegating node settles with the sub-graph's terminal outcome once the sub-graph settles (2026-06-08, corpus-bootstrap, artifact).
- Carry settlement: "in sub-graph, the terminal node attribues are copied back into the calling node" (2026-06-19, 08d65bfe, transcript, user) — exit writeback persisted to the calling node's (parent run's) attribute row in the same transaction as validation; the exit's own attribute row stays empty because the exit is not externally addressable (2026-05-20, attribute-pull-resolution, artifact).
- Entry absorption: same row, same executor context, same run; caller's `holds:` merge into the absorbed entry; if the entry executor fails or parks, the parent terminals immediately and the internal cascade does not fire (2026-05-15 + 2026-05-19, artifact).
- Sealed sub-graphs: internal nodes may read attributes only from same-invocation siblings, the calling node via the delegation contract, and always-available source kinds (params, claims, trigger messages, child.partition_key) — never from upstream calling-graph nodes by free reference (2026-05-20, attribute-pull-resolution, artifact): "The calling graph's namespace is not visible inside the subgraph."
- Entry-absorbed dispatches report the **outer** graph's identity for attribute-override matching (GraphName lookup); flat legacy templates imply a single `main` graph and only `graph: "main"` validates against them (2026-05-21, attribute-overrides-matcher-overlay, artifact).
- Sub-graph RunScope rows are created eagerly, in the same transaction as the calling node's success terminal, atomic with the internal cascade firing; closure when the exit terminates and the carry-rule fires; cross-RunScope cascade happens only at entry-success (into the sub-graph) — partition/sub-graph-internal cascades do not propagate outward (2026-05-22, fan-out-safety-scope-first, artifact).
- Delegation settlement is its own named code path — SettleFromDelegate (carry over run-scope ancestry), typed input distinct from the fan-out settle — not a tagged-union fork inside one function (2026-06-19, 08d65bfe + 8a3b8c19, transcript).
- Fan-out composes with delegation: each partition's cloned node can run a delegated sub-graph; a fan-out over one node is implicitly a sub-graph of one (2026-06-18, 9fb55f08, transcript, user).
- Bounded iteration is a template pattern, not a construct: N statically declared delegate nodes invoking the same sub-graph, iteration counter tracked producer-side and returned in claim payloads, no-work iterations short-circuiting through the zero-sub-scope fall-through; rimsky itself has no loop construct (2026-05-19, crimefinder, artifact-only).

## Intentional absences

- **A loop construct in rimsky** — deliberately absent; bounded iteration is expressed as statically declared delegate nodes (2026-05-19).
- **Closure semantics / calling-graph namespace visibility inside sub-graphs** — rejected for reusability, debuggability, and memoization-forwards compatibility (2026-05-20).
- **`carry_verbatim` as an author-facing aggregation-policy value** — it was a runtime routing tag, never a policy; `AggregationKindCarryVerbatim` was removed so the enum is the real four-value family (strict|threshold|best_effort|first), which belongs to fan-out only (2026-06-19, 08d65bfe, transcript, user): "two *settlement modes* and *one* of those modes (fan-out) has four user-configurable *aggregation policies*."
- **A unified DispatchChildren/SettleChildren primitive treating delegation as fan-out with N=1** — explicitly reversed 2026-06-23 (see drift-fight record); the settle paths are split on purpose.

## Corrections and restorations (drift-fight record)

- **Carry-rule was aspirational** (2026-05-20, attribute-pull-resolution): the blessed invariant "exit-node-writeback flows to parent run writeback" existed only in docstring form — CarryExitWriteback validated and logged but never persisted, dropping exit writebacks on the floor. Ruled: promised capability missing; restored with same-transaction persistence.
- **Sub-graph routing scenario not delivered** (2026-05-21, divergences): the landed test used a flat template asserting only `main` resolution because the scenario harness couldn't drive delegation end-to-end; sub-graph-name matching, internal-node routing, and entry-absorbed disposition had only unit coverage. Recorded coverage gap (artifact-only) — adjudicators should not assume end-to-end proof existed then.
- **Unification-then-reversal arc**: 2026-06-11 (last-mile-stability) unified delegation and fan-out over DispatchChildren/SettleChildren ("delegation is fan-out with N=1"), deleting the parallel implementations to cure fixes-landing-in-one-path-only. 2026-06-19 the user corrected the model to two settlement modes and had SettleChildren split into SettleFromDelegate / SettleFromFanoutChild with typed inputs. 2026-06-23 the user reversed the unification framing outright: "fan-out is literally just an operation that clones a node. nothing to do with sub-graph… these are distinct things and should be documented as such." Net ruling: distinct mechanisms, split settle, shared thin dispatch helper only. Adjudication findings resting on the "N=1" framing assume superseded expectations.

## Superseded / historical

- "Delegation is fan-out with N=1 plus a carry-verbatim policy" and the unified SettleChildren primitive (2026-06-11) → two distinct mechanisms with split, explicitly named settle paths (2026-06-19 refactor; 2026-06-23 user reversal).
- Five-value aggregation enum including carry_verbatim (2026-06-11) → four-value fan-out-only policy family; carry is delegation's policy-free mode (2026-06-19).
- The `carry_verbatim_requires_single_child` validation rejection (2026-06-11, located at template validation rather than the spec's canonicalization) → mooted once carry_verbatim ceased to be a declarable policy (2026-06-19).
