---
audit: service-address-book
artifact: concept:service-address-book
text: compliant
implementation: unsupported
commit: PENDING
audited: 2026-08-16T05:08:13Z
---

# The shared executor and store catalog, its writer, and its five invariants

Unsupported: the catalog is published at control-plane startup, but the second publish trigger the concept names — configuration reload — does not exist anywhere in the codebase, so a claim covering two events is carried on one. Everything else checked out. The control plane is the sole writer: exactly one production call site publishes, it is in the control-api start path, and the publish replaces the full catalog inside a transaction, with a conformance suite proving a re-publish that omits a name removes it and that a publish outside a transaction is refused. Both resolution sides are read-through with a short-lived cache in front of the shared record — the executor resolver and the claim-producer registry each keep a two-second entry cache and fall back to a cached value on a lookup fault — and no supervisor keeps a private accept list: the supervisor registry row carries only concurrency and callback host and port, the two accept-list columns having been dropped by migration, and candidate selection in both storage dialects filters on claim state, run state, instance pause, instance termination, wait-set drain, and sibling serialisation, with no predicate over executor or store names. Template registration consults the same catalog for both declared executor and declared store names, with late-bind names and built-in aliases excepted. A name that resolves nowhere fails inside the claimed dispatch on both sides, each with its own terminal error class and each covered by a scenario test.
