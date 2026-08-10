---
audit: peer-tls-enforced
artifact: story:peer-tls-enforced
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T05:45:00Z
---

# The transport setting on a peer entry means what it says

Supported, measured by driving one and the same plaintext peer process through
both settings so the setting is the only variable. Declared off, the peer was
reported reachable and a node dispatched at it settled successfully; declared
required, the same peer was reported unreachable with a failure naming both the
peer and the setting, and a node dispatched at it settled failed on a dial error.
Both peer kinds the story names were covered: on the store side the refusal is
louder still — a producer that cannot present credentials stops the stack
starting, exiting non-zero with the same named failure. Against a peer that can
present credentials the setting is satisfied rather than merely enforced: the
stack reported it reachable at the required setting, its certificate verified
against the deployment CA from an independent client holding an issued leaf, and
a node driven over that connection settled successfully.
