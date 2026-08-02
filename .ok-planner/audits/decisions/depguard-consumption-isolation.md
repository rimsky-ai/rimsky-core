---
audit: depguard-consumption-isolation
artifact: decision:depguard-consumption-isolation
determination: supported
commit: b767a27d
audited: 2026-08-02T09:38:50Z
---

# Bundled services import protocols only, enforced by lint not the module graph

Supported. The `.golangci.yml` `consumption-side-isolation` depguard rule scopes to `lib/services/**` (minus its `test/` tree) plus the root-anchored `claim_producers/**`, `sensors/**`, `subscribers/**`, `executors/**` globs and denies `lib/foundation`, `lib/graph`, `lib/runtime`, `lib/control`, and `cmd`; a repo-wide grep of every non-test `.go` file under `lib/services/claim_producers`, `lib/services/sensors`, `lib/services/subscribers`, and `lib/services/executors` (the four shipped-service directories) found zero imports of any of those four rimsky-internal packages, matching the "never" claim. `lib/services/go.mod` does require the root module and `lib/foundation` directly (used only by the out-of-tree `lib/services/test/` tree), confirming the module graph alone cannot exclude the edge and the lint rule is the real guard, as the rationale states.
