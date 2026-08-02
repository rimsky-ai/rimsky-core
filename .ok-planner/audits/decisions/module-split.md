---
audit: module-split
artifact: decision:module-split
determination: supported
commit: b767a27d
audited: 2026-08-02T09:39:17Z
---

# Five Go modules tied by go.work with local-path replace directives

Supported. `go.work` lists exactly five module paths (`.`, `lib/foundation`, `lib/protocols`, `lib/services`, `examples`) and each of the four non-root `go.mod` files carries a `replace` directive pointing at the sibling module by relative path (foundation and services both replace `lib/protocols` and the root module; examples replaces protocols, services, foundation, and root), matching the "root + foundation + protocols + services + examples tied by local-path replaces" claim exactly.
