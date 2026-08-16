---
audit: migrations-no-compat-shims
artifact: decision:migrations-no-compat-shims
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:47:25Z
---

# Whether schema rethinks drop and recreate instead of threading compatibility shims

Supported. The two migration sets hold 29 and 30 numbered files, and the pattern is exactly the one the choice names: 18 of them are named for dropping or retiring something, they carry 18 column drops plus constraint, index and default removals between them, the embedded backend rebuilds a whole table outright where its dialect requires it, and the consolidated initial migration collapsed the pre-history rather than carrying it forward. No compatibility shim exists on either side of the boundary: no migration threads a dual-shape transition, and the persistence code contains no dual-read, legacy-column or backward-compatibility fallback for a retired shape. The vocabulary rename was executed the same way, as direct column renames rather than paired old-and-new columns.
