---
audit: named-lock-metric
artifact: decision:named-lock-metric
determination: unsupported
commit: 3918d24e
audited: 2026-08-02T09:58:10Z
issue: 2026-08-02-095823-named-lock-metric-separate-family-not-label
---

# Named-lock acquisitions are countable

Unsupported. The decision's Choice is to increment the existing claim-acquisition metric family with a distinguishing label, explicitly naming and rejecting a new dedicated family as the alternative. The observability registry does exactly the rejected thing: it registers a separate counter family for named-lock acquisitions, distinct in both name and label schema from the existing claim-acquisition family, rather than folding into it with a label.
