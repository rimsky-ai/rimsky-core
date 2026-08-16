---
audit: message-queue-mode-per-instance
artifact: decision:message-queue-mode-per-instance
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:48:37Z
---

# One per-instance queue mode over two values, uniform across message types, warned on multi-type coalesce

Supported. The setting is a single column on the instance row, added by its own migration with a database-level constraint admitting exactly the two legal values and defaulting to the accumulating one; both drivers carry the same shape. It is declared on the template and materialized at instance creation, with the template's value taken as the default and the accumulating value substituted when the template leaves it blank. The uniformity claim is literal: the coalesce action is a single statement that cancels every undelivered, uncancelled message for the instance with no type predicate, so a newly received message of one type drops pending messages of every type. Neither ruled-out shape exists — there is no per-message-type field on the template's queue setting and no queue-mode field on the message-send request body, whose only inputs are the type, the payload, the sender, and a publisher-subscription reference. The registration warning is present and is a warning rather than an error: the validator rejects an unknown mode value outright, then, only for the coalescing mode, counts the distinct declared message types and appends a non-fatal warning naming the count and the sorted type list once there are two or more. Two scenario tests exercise both modes end to end, one asserting prior pending messages are dropped on receipt and one asserting the accumulating mode preserves every message. One fact the decision does not mention: the instance-create request may also supply the mode, overriding the template's declaration for that instance, so an instance does not always materialize the value it inherits — the shape implemented is a superset of the one the Choice describes, and none of the alternatives the decision rules out is among the additions.
