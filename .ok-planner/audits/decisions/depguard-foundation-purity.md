---
audit: depguard-foundation-purity
artifact: decision:depguard-foundation-purity
determination: supported
commit: b767a27d
audited: 2026-08-02T09:38:50Z
---

# Foundation module never imports graph, runtime, or control

Supported. The `.golangci.yml` `foundation-purity` rule scopes unconditionally to `**/foundation/**` (no negated per-site exemptions in the files list) and denies `lib/graph`, `lib/runtime`, `lib/control`, and `cmd`; a repo-wide grep of every `.go` file under `lib/foundation` for those four import paths returned zero hits, and `lib/foundation/go.mod` requires only `lib/protocols`, the stdlib, and third-party drivers — no upward-layer module dependency exists at all.
