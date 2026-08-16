---
audit: typed-message-substitution
artifact: story:typed-message-substitution
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:07:07Z
---

# Reading and composing message bodies by declared type name

Supported, in both directions the story claims. On the reading side, one node
that subscribes to two declared message types was woken by each in turn: in the
frame the first type opened it resolved that type's field and fell back to its
literal for the other, and in the frame the second type opened it resolved the
other way round — so a node that could react to several types disambiguates by
declared name and never mixed them. A directive reading a field the declared body
schema does not carry is refused at registration, so the type name is a real
contract rather than a label. On the composing side, a node composed a typed body
from an attribute it held, that message landed in the ledger attributed to the
instance, and it opened the next frame, where another node read the value back
through the same grammar — the flow-across-frames half, taken as an observed
frame boundary rather than inferred. Seven checks, none failing.
