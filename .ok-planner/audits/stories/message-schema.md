---
audit: message-schema
artifact: story:message-schema
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:03:56Z
---

# Declaring accepted message types so unknown ones fail loud

Supported, including the "instead of silently dead-lettering" half, which is the
part that could quietly not hold. A template declaring two message types with
body schemas accepted both, and each reached the node that reads it. An
undeclared type was refused at the send with a client error naming both the type
it refused and the types the template does declare, so the failure is loud at the
point of sending rather than discovered later. The typed contract binds the body
as well as the name: three non-conforming bodies — wrong field type, missing
required field, and an extra undeclared field — were each refused against the
declared schema. Two independent reads confirmed nothing refused leaked into the
system: the instance's history holds only the two accepted messages, and the
event log carries no dead-letter row at all. The contract is also checked in the
other direction, at authoring time — a template whose node reads a type it never
declared is refused at registration. Eleven checks, none failing.
