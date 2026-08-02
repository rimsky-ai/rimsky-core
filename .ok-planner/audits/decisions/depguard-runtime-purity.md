---
audit: depguard-runtime-purity
artifact: decision:depguard-runtime-purity
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:38:50Z
---

# Runtime layer never imports control

Supported. The `.golangci.yml` `runtime-purity` rule scopes to `**/runtime/**` and denies `lib/control` and `cmd`; a repo-wide grep of every `.go` file under `lib/runtime` for `rimsky-core/lib/control` returned zero hits, and `lib/runtime`'s actual imports of `lib/foundation`, `lib/graph`, and `lib/protocols` (e.g. the scheduler's use of `lib/graph/scheduler`) confirm the module does freely consume the layers below it, as the decision states.
