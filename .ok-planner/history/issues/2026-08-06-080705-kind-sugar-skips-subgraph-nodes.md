---
issue: kind-sugar-skips-subgraph-nodes
kind: human
category: bug
artifacts:
  - decision:kind-sugar-resolver
  - concept:sub-graph
  - concept:validation
status: repaired
opened: 2026-08-06T08:07:05Z
github: https://github.com/rimsky-ai/rimsky-core/issues/43
---

# Does a `kind:` node inside `graphs:` still register, deploy, and hang silently?

Partly rotted, partly still real. `CanonicalizeKindSugar`
(`lib/graph/node/kind_resolver.go`) still only loops
`tspec.Nodes` — but `lib/graph/node/template_validator_graphs.go::canonicalizeGraphs`
(via its `flatten` step) already runs at the very start of
`ValidateTemplate`, before `CanonicalizeKindSugar` is ever called, and
flattens every `Graphs[].Nodes` entry into `spec.Nodes` in place. So
by the time both the registration-time `kind:` check
(`validateKindDeclaration`) and the later `CanonicalizeKindSugar` call
run, an ordinary (non-absorbed) sub-graph node's `kind:` is already
present in `spec.Nodes` and resolves correctly — confirmed live with
a probe test — closing the "non-entry node hangs forever" half of the
filed Problem.

The other half reproduced: a probe test
(`graphs: {main: [{type: caller, delegate: sub}], sub: {entry: s_entry, exit: s_exit, nodes: [{type: s_entry, kind: counter}, ...]}}`)
showed the **absorbed entry** case was still broken. `flatten`'s
`absorbEntryIntoCaller` copies `entry.Executor` into the calling
node's `Executor` field *during* `canonicalizeGraphs` — i.e. before
`CanonicalizeKindSugar` ever runs — so when the entry declares `kind:`
instead of `executor:`, the caller absorbs an empty `Executor` and
dispatches as pure-cascade (a silent no-op), exactly the "entry node
absorbs into the caller as `executor: null`" failure mode the issue
describes.

**Fix.** `resolvedEntryExecutor` in
`lib/graph/node/template_validator_graphs.go` now resolves the
entry's `kind:` through the deployment's `KindAliasMap` at absorption
time, before copying it onto the caller — threaded down from
`ValidateTemplate`'s `hooks.KindAliases` through `canonicalizeGraphs`
→ `flatten` → `absorbEntryIntoCaller` (all four signatures gained an
`aliases *KindAliasMap` parameter; call sites and the existing direct
unit-test callers in `template_validator_graphs_test.go` were updated
to match). This restores `concept:sub-graph`'s stated invariant ("the
calling node dispatches with the entry's executor") for a kind-sugar
entry, without changing any registration-time error/warning shape for
the ordinary (non-absorbed) case. Added
`TestAbsorbEntryIntoCaller_ExecutorResolvedFromEntryKind` and
`TestCanonicalizeGraphs_KindAliasEntryResolvesCallerExecutor` to pin
the fix.

**Verified.** `go build ./...`, `go vet`, and `golangci-lint run` are
clean; `go test ./lib/foundation/... ./lib/graph/... ./lib/runtime/...`
pass.
