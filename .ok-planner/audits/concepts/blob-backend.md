---
audit: blob-backend
artifact: concept:blob-backend
text: compliant
implementation: unsupported
commit: d977250c
audited: 2026-08-16T04:46:57Z
---

# The blob-backend abstraction, its four implementations, and the invariants over spilled reads

Unsupported. Most of the concept holds: one interface with byte-stream IO plus a backend-name accessor, four pluggable implementations distinguished by where bytes live, a configurable spill threshold that short-circuits for the non-spilling backend, a startup validator that rejects the in-memory backend outside the single-process topology (wired into both configuration entry points and covered by a table test), self-describing handles carrying a backend prefix, and a persisted orphan ledger whose sweep is scoped to the running backend by query, so rows written under a different backend are skipped every pass and retained — the cross-backend case has its own tests, including one proving those rows do not starve the same-backend page. The contradicted claim is the backend-mismatch read rule. The concept states that on a backend-name mismatch reads fall back to the inline data column rather than erroring, a deliberate silent downgrade; in the code exactly one of the four spilled-read paths does that — the carry-forward of a prior run's bag into a new run. The two attribute-row readers (one per backend, both funnelling every spilled attribute read through a single scan helper) and the runtime scratch loader at dispatch acquisition all return an error naming the row's handle, the row's backend, and the active backend, and none of the three consults the inline column. As written the invariant describes a fallback that three of the four read paths do not implement.
