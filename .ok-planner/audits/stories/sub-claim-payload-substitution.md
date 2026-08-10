---
audit: sub-claim-payload-substitution
artifact: story:sub-claim-payload-substitution
determination: supported
compliance: noncompliant
commit: PENDING
audited: 2026-08-10T07:40:00Z
---

# The same payload path in a fan-out child and on a regular claim

Supported. Against an all-in-one deployment with a filesystem-backed claim
producer that supplies a payload per claimed item, two nodes carried
byte-identical attribute sources — the payload field path and the bare payload
path — and differed only in how the claim arrived. On the node that opened a
claim directly, the field path resolved to that claim's payload field and the
bare path to the whole payload object. On the fan-out node, three sub-claims
were opened and each clone settled carrying its own sub-claim's payload: the set
of resolved values equalled the set of sub-claim partition keys, no two clones
resolved the same value, and the bare path returned the object whose field the
field path had returned. The resolved attribute shape was identical in both
contexts, and the parent and all three clones settled fresh.

## Compliance

The capability clause quotes the substitution grammar literally
(`{{claim.<alias>.payload[.<field>]}}`) and names a protocol verb ("a regular
Open'd claim"), both delivery-surface detail the story rules place in
`decisions/`; the compliant text is "I can read producer-supplied per-sub-claim
data through the standard claim-payload path in a fan-out child's substitution
context, and the path resolves identically to how it resolves on a claim the
node opened itself".
