---
audit: cascade-signal-blind
artifact: story:cascade-signal-blind
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T05:30:00Z
---

# Every cascade-firing signal is reachable through the one subscription block

Supported. The cascade-firing set has three members — `terminal/success`,
`terminal/error/<class>`, and `attribute/<key>/changed` — and one template
subscribed a receiver to each of the three through the same `subscribes` entry
shape, plus a fourth receiver adding only the optional `when` predicate to the
error wildcard. All three kinds were emitted in one run and all four receivers
dispatched exactly once, so no member of the set behaves specially. A second
template differing only by a subscription on a non-cascade-firing signal was
rejected at registration by name, so the boundary of the set is stated by the
same mechanism rather than learned by trial.
