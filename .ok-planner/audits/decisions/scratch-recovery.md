---
audit: scratch-recovery
artifact: decision:scratch-recovery
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:33:36Z
---

# Per-row scratch survives all three re-dispatch dispositions

Supported. Three dispositions stamp a prior-dispatch reference, and exactly one of them creates a new row: the cascade recalculate enqueue, which loads the prior run's scratch (inline or spilled handle plus backend) and passes it as the new row's initial scratch at insert. The other two reuse the same row — the deadline sweep's release sets the row's prior-dispatch reference to its own id while returning it to stale, and the error-policy retry stamps the same row as its own prior — so no copy is needed and the already-persisted scratch stands. Enumerated every production caller that creates a dispatch row: the one queue enqueue (recalculate) and the four non-cascade stale creators (message delivery plus three operator debug paths), of which only the recalculate path sets a prior-dispatch reference at all. Scratch is a column set on the dispatch row in both backends. End-to-end scenarios cover all three dispositions, each asserting the executor sees the prior attempt's bytes on the re-dispatch.
