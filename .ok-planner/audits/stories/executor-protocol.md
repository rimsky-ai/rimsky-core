---
audit: executor-protocol
artifact: story:executor-protocol
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:45:00Z
---

# A custom executor plugs in on the protocol alone, and rimsky honours all of it

Supported. A third-party executor built as its own module against the protocols
module was wired into a stack that knew nothing but its endpoint, and rimsky did
each of the six things the story names. It discovered the peer at startup and
read back its whole advertisement — both declared error classes, both declared
tags, and the expected-attributes schema. It validated templates against that
schema, rejecting a property whose type contradicted the executor, rejecting a
property the executor's closed schema does not carry, flagging an error class
outside the declared vocabulary, and rejecting a subscription filtering on an
undeclared tag. It dispatched nodes to the peer's server and accepted each
settling outcome: success settled the node fresh with the peer's delta, error
settled it failed, and park parked it. It routed errors by the class raised —
the give_up class was dispatched once, the retry class once plus its two
retries. And the declared tags gated a real subscription: of two subscribers
filtering on different declared tags of the same sender, only the one matching
the emitted tag ran.
