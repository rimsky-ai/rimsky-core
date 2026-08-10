---
audit: api-key-management
artifact: story:api-key-management
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T05:45:00Z
---

# The whole api-key lifecycle runs from the operator's own verbs

Supported. All seven administration verbs the story names were driven against a
fresh stack and each effect checked independently through the control API:
bootstrap minted the first admin key on an anonymous deployment and refused a
second attempt; minting with a role produced a key that could read and could not
write; listing and inspection named every key, carried no plaintext field, and
never echoed a live plaintext; revoking made the key's next request 401 while
keeping it inspectable on request; rotating handed out a new plaintext that
worked immediately and left the old key working until its grace window closed,
after which the old key was refused and the new one still answered; and the
status verb reported the mode and the key counts at each stage.
