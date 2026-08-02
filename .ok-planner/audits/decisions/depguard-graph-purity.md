---
audit: depguard-graph-purity
artifact: decision:depguard-graph-purity
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:38:50Z
---

# Graph layer is unconditionally pure, with the tick loop kept in runtime

Supported. The `.golangci.yml` `graph-purity` rule scopes to `**/graph/**` with no negated globs anywhere in its files list and denies `lib/runtime`, `lib/control`, and `cmd`, matching the "no per-site exemptions" claim; a repo-wide grep found zero `nolint` depguard suppressions anywhere in the tree and zero imports of `lib/runtime` or `lib/control` under `lib/graph`. The specific architecture claim also holds: `lib/graph/scheduler/steps.go` exports `ListPureCascadeReady`, `AcquiresClaims`, and `PrepareNativeClaimRouting`, and `lib/runtime/scheduler/pure_cascade.go` imports `lib/graph/scheduler` and calls exactly those three functions downward from the runtime-layer tick loop, so the graph layer needs no interface machinery to stay a clean dependency target.
