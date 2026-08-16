---
assessment: typed-message-substitution--compose-and-carry
subject: story:typed-message-substitution
way: compose-and-carry
release: d977250c
outcome: held
warrant: experiment:typed-message-substitution
---
# Composing a typed body and carrying its value into the next frame

On the composing side, a node built a typed body from an attribute it held. That message landed in the instance's ledger attributed to the instance, it opened the next frame, and a node there read the value back through the same grammar the author uses for node attributes. The frame boundary was observed rather than inferred, so message bodies are typed attribute blocks the author both writes and reads, with one grammar for both directions.

## Unverified remainder

None: the passing run demonstrates the way as promised.
