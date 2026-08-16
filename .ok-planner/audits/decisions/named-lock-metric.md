---
audit: named-lock-metric
artifact: decision:named-lock-metric
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:34:05Z
---

# Named-lock acquisitions counted on the existing acquisition metric family, labeled by kind

Supported. There is one acquisition counter family, named to the same convention as the other counters in the registry, and it carries a label whose two values distinguish a producer claim from a named lock; the named-lock helper increments that same family with the named-lock value, and the producer helper increments it with the producer value, so dashboards can compare the two kinds directly. No second family was added for named locks, which is what the decision chose. The named-lock helper is called from both points on the named-lock acquisition surface — the successful acquisition and the unavailable outcome — and the metric-registry test covers the labeled increment. Nothing records these acquisitions as event-log rows instead, the rejected alternative.
