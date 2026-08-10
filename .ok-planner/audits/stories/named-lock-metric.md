---
audit: named-lock-metric
artifact: story:named-lock-metric
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:45:00Z
---

# Lock acquisitions are a labelled counter beside producer claims

Supported. Three nodes were made to contend for a named lock of limit 1 while a
fourth took a claim from a bundled claim producer, and the platform metrics
endpoint served one counter family carrying both: three named-lock acquisitions
labelled by the lock's name, beside one producer-claim acquisition labelled by
the producer's name, on the same `rimsky_claim_acquisitions_total` series with
prometheus help and type lines. Saturation is in the same family rather than
reconstructed: the contention the three holders caused came back as its own
labelled series at twenty-one unavailable acquisition attempts.
