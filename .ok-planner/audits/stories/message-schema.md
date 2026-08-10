---
audit: message-schema
artifact: story:message-schema
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:25:00Z
---

# Declared message types accepted, undeclared ones refused at the send

Supported. Against a zero-config all-in-one deployment, a template declared 2
message types with body schemas and a node per type. Both declared types were
accepted with conforming bodies and each reached its subscribing node. A message
of an undeclared type was refused at the send with a response naming the refused
type and listing the 2 types the template declares. All 3 ways a body can breach
its declared schema — wrong field type, missing required field, undeclared extra
field — were refused the same way. The instance's history held only the two
accepted messages and the event log carried no dead-letter row, so the unknown
type failed at receipt rather than entering the bus and disappearing. A template
whose node reads a type it never declared is refused at registration.
