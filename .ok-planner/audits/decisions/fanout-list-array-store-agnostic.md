---
audit: fanout-list-array-store-agnostic
artifact: decision:fanout-list-array-store-agnostic
text: compliant
implementation: unsupported
commit: PENDING
audited: 2026-08-16T04:43:41Z
---

# One list fan-out grammar on both bundled stores, and the count of store-specific idioms

Unsupported, on the Rationale's enumeration; the Choice itself holds. Both bundled claim producers serve the list partition request through the same split-scope RPC and through the same shared list-grammar package for parsing and sub-scope construction, differing only in how each synthesizes a child claim scope from a key — a synthetic path under the parent for the filesystem store, a parent-row-and-key object for the Postgres store. Neither advertises the list grammar as a distinguishing capability, so which store holds the parent claim is indeed a deployment choice. What fails is the Rationale's claim that folder expansion is "the one genuinely store-specific partition idiom that exists". Enumerating every partition-request discriminator both producers accept gives four grammars: the list grammar on both stores, and three served by exactly one store each — folder expansion and batch pick on the filesystem producer, partition policy on the Postgres producer. Each producer rejects the other's store-specific discriminators by name in its own error text, so the split is explicit in the code rather than incidental. Three store-specific idioms exist, not one.
