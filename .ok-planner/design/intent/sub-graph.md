# Intent Dossier: sub-graph

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- Sub-graphs are a first-class template construct: a top-level graphs: block (main is the reserved top-level graph), each sub-graph declares entry and exit nodes, and a node invokes one via delegate: (mutually exclusive with executor:) (2026-05-15, data-platform-extensions, artifact).
- Fan-out and sub-graph delegation are distinct mechanisms, not variants of one primitive: fan-out clones the calling node N times and does not aggregate attribute results; sub-graph delegation substitutes into the entry node and retrieves results from the single exit node. A cloned node can itself delegate — orthogonal composition (2026-06-23, 10cf843b, transcript, superseding the child-execution-unification framing). Run-scope exists exclusively to handle sub-graphs and recursion; a fan-out node can also be (and most often will be) a sub-graph (2026-06-18, 9fb55f08, transcript).
- The three RunScope kinds (main, sub-graph, fan-out partition) with shared persistence stand as designed; a sub-graph's RunScope has as its parent whatever runscope created it — nothing to do with the frame's root; any runscope can spawn a child runscope (2026-06-19, 8a3b8c19; 2026-06-30, 8a8539a4, transcript).
- Sub-graphs are sealed and externally opaque: no closure semantics, internals cannot free-reference the calling graph, cascade never descends from outside; calling-graph state travels only through the explicit entry/exit channel (2026-05-15; 2026-05-20, attribute-pull-resolution, artifact; 2026-06-22, 10cf843b, transcript).
- Every new RunScope (sub-graph invocation, fan-out partition) starts with clean attributes from schema defaults; carry-forward hydration applies only within a scope (2026-06-14, 752fe200, transcript).
- Sub-graph exit nodes are normal executor-bearing nodes that emit leaf_run lineage records; only fan-out parents and pure-cascade nodes are pass-through (2026-06-23, 10cf843b, transcript).

## Required behaviors (open promises)

- Delegation settle: a node declaring delegate: <graph-name> dispatches the sub-graph as its execution unit and settles only once the sub-graph settles, with the terminal outcome propagated back to the parent (2026-06-08, corpus-bootstrap, artifact).
- Carry-verbatim: the exit node's attribute output is copied unmodified onto the calling node's attribute row in the same transaction that records the exit's terminal; the sub-graph run-scope closes; cascade fires to the calling node's subscribers carrying the new attributes; if exit never runs, the parent's writeback stays empty (2026-05-15, data-platform-extensions, artifact; 2026-06-19, 08d65bfe, transcript).
- The explicit attribute channel: the calling node's attributes are copied into the sub-graph's dedicated entry node ("a special node that exists for that purpose") and copied back out at exit. The cross-RunScope-hydration prohibition bans implicit reach only, not this designed channel (2026-06-22, 10cf843b, transcript).
- Encapsulation validation at registration: internals reference only same-sub-graph internals and the entry alias; rejections for recursive sub-graphs (subgraph_recursion_unsupported, HTTP 400 at POST /templates), missing entry/exit, disconnected internals, main declaring entry/exit, outer-graph references, and entry==exit (2026-05-15; 2026-06-02, acceptance-coverage-recovery, artifact; internal-references-local-only reaffirmed 2026-05-19, crimefinder, artifact).
- Depth gating as a runtime safety net: a sub-graph that would create a RunScope already present in its parent chain at any depth is rejected via parent-chain walks — defense-in-depth behind the static recursion rejection (2026-05-22, fan-out-safety-scope-first, artifact-only).
- The entry-alias invariant (invariant 5) is settled intent that was never completed: the marker is plumbed through but its consumer was never built — adjudicated fix-code: build the consumer rather than soften the invariant (finding 134) (2026-07-13, 3f71f90a, transcript).
- Held-subgraph coordination: cascade from a node in a held subgraph is filtered to subgraph members; on claim resolution each holder transitions per its full claim portfolio and fires its own deferred cascade; the deferred Commit/Abandon cascade fires only to non-members (members are coordinated via the held-state transition — no double-fire) (2026-06-21, 10cf843b, transcript).
- Parent-run state machine variant admitting terminal→stale, terminal→running, running→running, with audited reasons ReasonChildTransitioned and ReasonSubGraphInternalCascadeFired (2026-05-15, data-platform-extensions, artifact-only).
- Attributes do not persist across frames; sub-graph invocations get clean attributes (2026-06-14, 752fe200, transcript).
- The four sub-graph must-pass end-to-end scenarios (entry-absorbed dispatch + internal cascade; exit-writeback carry-rule; internal error retry within sub-graph; cascade through sub-graph exit), each exercising the full dispatch lifecycle rather than seeded rows (2026-05-22, fan-out-safety-scope-first, artifact).
- Fan-out composes with delegation: a fan-out parent whose dispatch is delegate produces a nested sub-graph RunScope inside each partition RunScope; fan-out does not require a sub-graph (a partition scope may hold just the calling node's own executor dispatch) (2026-06-19, 8a3b8c19, transcript).
- Bounded iteration is a supported template pattern: N statically declared delegate nodes invoking the same sub-graph, iteration counter tracked producer-side in claim payloads; rimsky itself has no loop construct (2026-05-19, crimefinder, artifact-only).
- Entry-absorbed dispatches report the outer graph's identity for matcher graph routing (2026-05-21, attribute-overrides-matcher-overlay, artifact-only — see Conflicts on the absorption model itself).

## Intentional absences

- A loop construct in rimsky — none exists by design; iteration is the pre-declared delegate-node pattern (2026-05-19, crimefinder, artifact).
- Cross-template sub-graph reuse — deferred to V2; sub-graphs are inline in V1 (2026-05-15, data-platform-extensions, artifact).
- entry==exit sub-graphs — rejected for V1 (collapses to a regular node) (2026-05-15, artifact).
- Unifying the fan-out-partition RunScope kind into the sub-graph kind — considered and explicitly declined: "we don't need to unify." (2026-06-19, 8a3b8c19, transcript)
- The child-execution-unification framing ("delegation is fan-out with N=1", unified settle primitive) — reversed; settle is intentionally split because the fan-in shapes differ, only dispatch shares a thin helper (2026-06-23, 10cf843b, transcript).

## Corrections and restorations (drift-fight record)

- The exit-writeback carry-rule was discovered aspirational: CarryExitWriteback validated and logged but never persisted, dropping exit writebacks on the floor — completed so the writeback persists to the parent run's attribute row in the same tx; the exit's own row stays empty (not externally addressable) (2026-05-20, attribute-pull-resolution, artifact).
- The fan-out-safety background recorded real sub-graph drift: entry absorption half-built (marker set but executor/stores/holds never merged), SplitScope infinite recursion, 30+ ambiguous WHERE node_id callsites — root-caused to the bolted-on data model and fixed by the first-class rimsky_run_scopes reshape (2026-05-22, fan-out-safety-scope-first, artifact).
- Verification sweeps restored a missing sub-graph exit cascade bridge in applyTerminalCompleteSubgraphExit and fixed MarkSourceNodeStale's missing run_scope_id on both backends (2026-05-22, fan-out-safety-scope-first-divergences, artifact).
- End-to-end coverage was scoped down twice against plan: the two-caller shared-subgraph isolation and Z-pattern fixtures were never built (2026-05-20 divergences), and the planned sub-graph matcher-routing scenario landed as a flat-template test because the harness did not drive delegation end-to-end (2026-05-21 divergences) — recorded coverage gaps, never retracted (artifact).
- An earlier sketch note listed subgraph exits among pass-through nodes emitting no leaf_run record — wrong; exits are normal executor-bearing nodes and the invariant was corrected (2026-06-23, 10cf843b, transcript).
- "Cross-RunScope hydration is forbidden" was being over-read as banning the entry/exit copy channel — corrected: it bans implicit reach only (2026-06-22, 10cf843b, transcript).
- The entry-alias marker was plumbed but its consumer never built — adjudicated fix-code (finding 134) (2026-07-13, 3f71f90a, transcript).

## Superseded / historical

- (parent_run_id, child_key) inline disambiguation on rimsky_node_runs → first-class rimsky_run_scopes with non-null run_scope_id FK (2026-05-22, fan-out-safety-scope-first, artifact).
- "Fan-out is NOT subgraphs" phrasing → "fan-out is distinct from subgraph" (2026-06-18, 9fb55f08, transcript).
- child-execution-unification (delegation as fan-out N=1, unified settle) → distinct-mechanisms ruling (2026-06-23, 10cf843b, transcript).
- The recursive run-tree via parent_run_id/child_key columns (2026-05-15) → the RunScope model (2026-05-22).

## Conflicts needing human ruling

- Entry-node identity model: the artifact tier (2026-05-15 data-platform-extensions; 2026-05-19 crimefinder; 2026-05-21 matcher-overlay) defines structural absorption — the entry node is absorbed into the calling node at canonicalization (same rimsky_nodes row, same executor, same dispatched run, holds: merged, entry-alias references resolving to the caller). The later transcript tier (2026-06-22 / 2026-06-23, 10cf843b) describes the entry as a dedicated special node into which the calling node's attributes "have to be copied", with the parent "substituting into the entry node". Under absorption no copy exists (caller and entry are one row); under the copy model they are distinct nodes. Precedence favors the transcript, but the record never explicitly retires absorption, and the unresolved entry-alias consumer (finding 134, 2026-07-13) sits exactly on this seam. A human must rule which model governs before adjudicating entry-related findings.
