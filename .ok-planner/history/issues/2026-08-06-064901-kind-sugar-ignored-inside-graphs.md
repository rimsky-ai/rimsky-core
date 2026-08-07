---
issue: kind-sugar-ignored-inside-graphs
kind: audit
category: bug
artifacts:
  - concept:sub-graph
status: answered
opened: 2026-08-06T06:49:01Z
---

# Does a `kind:` node inside `graphs:` skip kind-sugar canonicalization and validation, registering silently with an empty executor?

No. Re-verified against the current tree: `node.ValidateTemplate` calls
`canonicalizeGraphs` (`lib/graph/node/template_validator_graphs.go:86`)
before any node-level checks run, and `canonicalizeGraphs` calls
`flatten` (`template_validator_graphs.go:139-186`), which copies every
`Graphs[].Nodes` entry — main-graph and subgraph alike, including each
entry's `Kind` field — into `spec.Nodes` before returning. The
subsequent per-node loop (`template_validator.go:118-124`, calling
`validateKindDeclaration`) and the registration handler's later
`node.CanonicalizeKindSugar(&spec, ...)` call
(`lib/control/controlapi/templates.go:290`) both then operate on that
already-flattened `spec.Nodes`, which is the same `*TemplateSpec` value
threaded through the whole handler by pointer. So both checks the issue
claimed were bypassed already cover graph-nested nodes.

Confirmed with a throwaway reproduction against
`node.ValidateTemplate`/`node.CanonicalizeKindSugar` using a
`graphs:`-shaped spec: a `graphs[0].nodes[]` entry with an unregistered
`kind:` value is rejected with a `nodes[0].kind: kind "..." is not
registered` validation error (not silently accepted), and one with a
registered `kind:` value is correctly rewritten to `Executor` with
`Kind` cleared by `CanonicalizeKindSugar` (not left as a dangling empty
executor). The filed gap does not exist in the current tree — likely
already fixed by `flatten`'s introduction, or the issue was filed
against a pre-flatten codepath. No corpus or code change made.
