---
assessment: message-schema--undeclared-read-refused-at-registration
subject: story:message-schema
way: undeclared-read-refused-at-registration
release: d977250c
outcome: held
warrant: experiment:message-schema
---
# The contract is checked at authoring time too

The audit also took the contract in the other direction, before any message exists. A template whose node reads a message type the template never declares was refused at `catalog:http-routes/POST /v1/templates` rather than registering and failing later at run time. A template author therefore finds out about a mistyped or forgotten declaration while authoring, which is what makes the send-time refusal a narrow safety net rather than the only check.

## Unverified remainder

One authoring-time mistake — a node reading an undeclared type — was driven. The way does not enumerate every declaration mistake registration can catch.
