---
audit: protocol-version-v1-namespaced
artifact: decision:protocol-version-v1-namespaced
text: compliant
implementation: unsupported
commit: d977250c
audited: 2026-08-16T05:37:59Z
---

# One v1 namespace over the whole contract surface, with no version-omitted carve-outs

Unsupported, on the no-carve-outs clause. Nearly all of it holds. All ten proto files declare the same single versioned package and the same generated-code path, so the proto half is uniform with nothing to reconcile. Every one of the 48 routed actions in the control API's registry carries a versioned path, the whole router is built inside one versioned mount, and a test walks the mounted routes and fails on any the registry does not know — so a route cannot appear outside that mount unnoticed. The observability sub-router is reached at a versioned path and only appears unversioned as an internal handler-side prefix, not as anything a caller can address. The three contract routes of the executor async-callback surface — the callback post and the per-run keepalive and attribute-writeback posts — are all versioned. The exception is on that same async-callback surface, which the decision names as included: its listener also mounts a liveness route at a bare, version-omitted path. The control API puts its own liveness probe under the version prefix, so this is an inconsistency within the project's own convention rather than a category the decision overlooked, and the decision's text explicitly admits no bare-path carve-outs.
