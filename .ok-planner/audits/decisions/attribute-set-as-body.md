---
audit: attribute-set-as-body
artifact: decision:attribute-set-as-body
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:03:38Z
---

# A message-sender node's attribute set is the message body, matched exactly and with no mapping layer

Supported. Template registration validates a send node against its destination message type's body schema in both directions and on three axes: an attribute the body does not declare is an error, a body field the attribute schema omits is an error, a declared type mismatch on a shared field is an error, and the two required sets must match each way — five distinct checks, each with its own registration-level test, and registration returns a validation failure rather than accepting the template. The no-mapping-layer claim holds structurally: the node definition's send surface is a single message-type string with no mapping sub-block anywhere in the template spec, and the built-in send handler builds the body by marshalling the resolved attribute struct as it stands, with no renaming, projection, or field selection between the two. The validator walks every node in the flattened template, so subgraph nodes are covered on the same pass as main-graph ones. Behavior is exercised end to end by the cascade-send scenarios and by handler-level tests asserting the callback receives the marshalled attributes verbatim.
