---
audit: depguard-foundation-internal
artifact: decision:depguard-foundation-internal
determination: supported
commit: b767a27d
audited: 2026-08-02T09:38:50Z
---

# foundation/internal is import-forbidden from outside the foundation module

Supported. The `.golangci.yml` `foundation-internal-isolation` depguard rule applies to `$all` files except `**/foundation/**` and denies `lib/foundation/internal`; a repo-wide grep for `rimsky-core/lib/foundation/internal` found 11 importing files, all of them under `lib/foundation/` itself (locks, lifecycle, and several postgres/conformance test files), so no external import of the package exists in the current tree, matching the "forbidden" claim exactly.
