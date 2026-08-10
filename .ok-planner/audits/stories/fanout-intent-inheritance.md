---
audit: fanout-intent-inheritance
artifact: story:fanout-intent-inheritance
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:40:00Z
---

# Every sub-claim a fan-out opens carries the intent the template declared

Supported. Against an all-in-one deployment with the bundled filesystem claim
producer configured, a node declaring a claim with `intent: r` and fanning it
into three partitions opened one parent handle and three sub-handles; the
claim-handle read surface reported all three sub-handles pointing at that parent
and all three carrying intent `r`, and all four acquisitions the run recorded
named `r` and no other value. The same template with `intent: rw` produced one
parent and three sub-handles all carrying `rw`, so the value the sub-claims carry
follows the declaration rather than a producer default.
