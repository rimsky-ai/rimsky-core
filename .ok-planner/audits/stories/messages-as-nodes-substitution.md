---
audit: messages-as-nodes-substitution
artifact: story:messages-as-nodes-substitution
determination: supported
compliance: noncompliant
commit: PENDING
audited: 2026-08-10T07:25:00Z
---

# One substitution channel over message bodies and node attributes

Supported. Against a zero-config all-in-one deployment, a declared message type
appeared in the instance's node list as an ordinary node, and one node resolved
a messages-directive and a nodes-directive side by side in a single dispatch,
settling with both values. The value the messages-directive read is an
attribute-changed write on the message type's own node, so both directives read
the same attribute store. The two namespaces are held apart at registration in
both directions: a messages-directive naming an undeclared message type is
refused, and a nodes-directive naming a message type is refused as an unknown
node. The subscription-coverage check treats the two forms identically,
refusing an uncovered reference of either with the same finding and naming the
subscribes entry to add. Uniformity stops at the subscription side, which the
story does not claim: an attribute-changed edge on a message type is refused,
because message delivery only ever manifests as a success terminal.

## Compliance

The body quotes the substitution-directive syntax and names the registration
check that enforces the two namespaces, both of which the story rules place in
decisions or specs rather than in a story; the compliant text is "As a template
author, I can read a message body with the same substitution channel I use for a
node's attributes, so that I learn one channel rather than two."
