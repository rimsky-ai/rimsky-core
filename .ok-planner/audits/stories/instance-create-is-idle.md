---
audit: instance-create-is-idle
artifact: story:instance-create-is-idle
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:38:45Z
---

# Creating an instance of a deployed template starts nothing until work is invoked

Supported. Both of the story's ways were driven through the public surface
against a container of the released all-in-one image: creating an instance of
a deployed template returned an id and materialized the node graph while every
node run counter read zero, the event log was empty and the message queue was
empty; and invoking work was a separate act that then ran the node. The
negative is anchored to a second instance rather than to the clock — a sibling
instance was created, woken and driven to completion, proving the scheduler was
live, and the untouched instance was still event-free and still at zero
counters when re-read afterwards. The body states a role, a capability and a
mandatory benefit, names no surface and no mechanism, and carries no history or
forward-looking text.
