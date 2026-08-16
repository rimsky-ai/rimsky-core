---
audit: atomic-staging
artifact: concept:atomic-staging
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:43:41Z
---

# Producer-side stage-then-swap discipline and its per-substrate atomicity caveats

Supported. The one producer in the tree that realizes staged-asynchronous write semantics is the bundled Postgres claim producer, and every decidable invariant the concept states lands there: a write-intent open reserves a distinct staging schema while a read-intent open returns the canonical pre-stage snapshot untouched; the open probes for objects outside the canonical schema that depend on objects inside it and fails fast with a declared not-atomically-replaceable error class before any staging exists; the swap re-runs that probe inside its transaction as a backstop and surfaces the producer's swap-failure error class rather than cascade-destroying a dependent that appeared mid-flight; and Commit performs the drop-and-rename inside one transaction, which is the concept's transactional-store atomicity caveat. Abandon and Release both drop the staging through the same guarded path, so releasing a claim whose staging was never committed leaves the canonical view untouched, and the terminal verbs refuse any address that is not a staging-prefixed schema. The producer's package-level suite covers all seven of those behaviours as named subtests, and the composition the concept describes — a subgraph-lifetime claim whose auto-terminal fires Commit on all-success and Abandon on any-failure with a co-holding verifier node — is driven end-to-end against a live stack by the atomic-staging scenario suite over the filesystem producer. The five remaining invariants classify substrate families rimsky does not implement (pointer flip, manifest flip, rename within a volume, copy across volumes, append log); they are statements about the pattern's applicability rather than claims on this codebase, and nothing in the tree contradicts them.
