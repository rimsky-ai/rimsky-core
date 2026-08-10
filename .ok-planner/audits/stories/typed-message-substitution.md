---
audit: typed-message-substitution
artifact: story:typed-message-substitution
determination: supported
compliance: noncompliant
commit: PENDING
audited: 2026-08-10T07:25:00Z
---

# Addressing each message type by name, and carrying a body into the next frame

Supported. Against a zero-config all-in-one deployment, one node subscribed to
both of a template's 2 declared message types and sourced an attribute from each
type by its declared name. In the frame the first type opened it resolved that
type's field and fell back on the other; in the frame the second type opened it
resolved the other way round, so a node that could react to either type never
mixed them. A directive reading a field the declared body schema does not carry
is refused at registration. On a second template, a sender node composed a
message body from an attribute it held, that body opened the second of the
instance's 2 frames, and a node there read the value through the same
substitution channel — so a value crossed a frame boundary in a message body.

## Compliance

The benefit clause ends by asserting what message bodies are internally
("first-class typed attribute blocks"), a data-shape claim the story rules place
in decisions rather than in a story; the compliant text is "so that I can
disambiguate when a node could react to several types, and carry a value from
one frame into the next."
