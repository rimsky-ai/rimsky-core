---
audit: inertness
artifact: concept:inertness
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:33:36Z
---

# The don't-inspect discipline over eight carrier streams, its two sub-disciplines, and its sanctioned read sites

Supported. All eight named carrier streams exist as distinct carriers and each is handled as the taxonomy assigns it. The five byte-opaque streams — claim scope, claim address, claim payload, blob content, and scratch — are carried as raw byte fields end to end; the runtime layer's read sites for the three claim-borne ones are pinned by a source-parsing fitness test that enumerates exactly three sanctioned functions and fails both when one disappears and when a fourth appears, with an error message that tells the next author to update this concept alongside the allowlist. Scratch round-trips through park and re-dispatch with no mid-dispatch write channel, covered by both a runtime test and an end-to-end scenario. Of the three structurally-inert streams, the two content-matching sites the concept sanctions both exist and are annotated: the shared matcher walks a single named attribute path and compares primitives with no traversal beyond that path, and the subscription predicate compiler builds a CEL program over the emitted signal payload, checking the expression's field references against the payload's declared schema and, for message bodies, against the declared body fields. The no-logging clause was checked by sweeping the library tree for formatted-error and structured-log sites naming a payload, scope, address, or scratch value; none were found. The derived-hash carve-out is real and narrow — the lineage records carry claim-scope and attribute-bag hashes rather than bytes. The auth-audit section's verbatim-request-body policy is implemented and pinned by two tests: one asserting the stored parameters are byte-verbatim up to the capture cap, one asserting the api-key plaintext never reaches the row.
