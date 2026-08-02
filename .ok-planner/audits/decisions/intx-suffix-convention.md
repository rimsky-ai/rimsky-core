---
audit: intx-suffix-convention
artifact: decision:intx-suffix-convention
determination: unsupported
commit: 3918d24e
audited: 2026-08-02T09:58:10Z
issue: 2026-08-02-095822-intx-suffix-live-wrapper-pairs-exist
---

# The InTx suffix means "requires an open transaction"

Unsupported. Enumerated all twenty production functions carrying the suffix this decision defines, the same population size the decision's own alternatives section estimates. Two are exactly the forbidden pattern: a public function whose entire body opens a transaction and delegates to a private, identically-suffixed sibling used nowhere else in production code, with the public wrapper called from multiple live sites. A third population member, the persistence layer's transactional blob interface, is confirmed to be the legitimate capability-split shape the decision describes rather than a duplicated pair, but its correctness does not offset the two confirmed violations.
