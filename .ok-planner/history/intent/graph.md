# Intent Dossier: graph

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- The root module splits the old modeling/ directory into `graph/` (templates, nodes, instances, frames, scheduler, attributes) and `control/` (controlapi, cli, observability, config). The boundary is one-way and lint-enforced: control may import graph (read access plus a small mutation set through hand-rolled service interfaces); **graph never imports control** — "graph → control: zero" (2026-05-12).
- Sub-graphs are a first-class graph construct: templates declare a top-level `graphs:` block with `main` as the reserved top-level graph; sub-graphs declare entry and exit nodes; a node invokes one via `delegate:`, mutually exclusive with `executor:` (2026-05-15).
- Instance wake is message-driven: an **empty message is the root trigger**, with structural root-edge injection at registration and instance-create-is-idle; the synthetic-envelope wake mechanism is retired (2026-06-17, transcript).
- Empty-string sentinels were swept out of the graph-adjacent surfaces: `lookupGraphName` renamed `graphContainingNodeType` so the MainGraphName fallback contract is loud; the BuildAttributeDeps empty-type filter became a named `MessageRow.IsEmptyWake()` predicate (2026-06-19, transcript).

## Required behaviors (open promises)

- One-way graph/control boundary enforced by a depguard rule; single Go module (2026-05-12, nomenclature-resolution, artifact) (artifact-only).
- `graphs:` block, reserved `main`, sub-graph entry/exit nodes, `delegate:` mutually exclusive with `executor:` — "Sub-graphs as a first-class graph construct, exercising the run-tree's general-tree shape." (2026-05-15, data-platform-extensions, artifact) (artifact-only)
- Empty-message root trigger with structural root-edge injection at registration and instance-create-is-idle (ratified and committed, 7d71ef32, ~119 files; new cmd/rimsky/cli/structural_root.go) (2026-06-17, daf59a14, transcript, assistant-ratified).
- Sentinel-free contracts at the five swept sites, including: AggregationPolicy.Kind as a typed AggregationKind enum with strict stamped at template-load/row-insert when the author omits kind (read-side blank patches deleted); ChildState.SettlingSignalType as a nullable *TypePath preserving nil-ness so IsSuccess and aggregateFirst do honest nil-checks; `graphContainingNodeType` naming; `MessageRow.IsEmptyWake()` — "yes, fix them all" (2026-06-19, 08d65bfe, transcript, user).

## Intentional absences

- **Synthetic-envelope wake mechanism** — retired with the empty-message-wake-trigger work: `lib/runtime/synthetic_envelope.go` deleted, TestParkedLifecycleResumeOnExternalInvalidate deleted (2026-06-17, daf59a14, transcript).
- **Empty-string sentinels** at the five swept sites — replaced by typed enums, nullable pointers, and named predicates; read-side blank-patching must not return (2026-06-19, 08d65bfe, transcript).

## Superseded / historical

- Synthetic-envelope wake → empty message as root trigger with structural root-edge injection (2026-06-17).
- `lookupGraphName` → `graphContainingNodeType` (2026-06-19).
