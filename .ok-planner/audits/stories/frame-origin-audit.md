---
audit: frame-origin-audit
artifact: story:frame-origin-audit
determination: supported
compliance: noncompliant
commit: PENDING
audited: 2026-08-10T07:25:00Z
---

# Every frame names the message that opened it, and which of the three kinds it was

Supported. One instance was driven to produce all three trigger kinds — an
operator posting a declared message type, an external POST arriving through the
bundled webhook sensor, and a node sending a message of its own — and opened
three frames. All 3 name a triggering message id, a message type, a message
sender and a message sender kind, and the three sender kinds are exactly
`operator`, `publisher` and `instance`. All 3 read back individually with the
same trigger the list gave, and all 3 triggering message ids resolved to a
message whose type and sender kind match what the frame reported. Narrowing the
frame list by one triggering message id returned that one frame. One naming
difference is worth recording: the story's "cascade-sent message" is reported as
sender kind `instance` with the sender `instance:<id>`.

## Compliance

The capability clause names the delivery surface — "through the existing frame
observability surface" — which the story rules place in decision territory; the
compliant text ends the clause at the capability: "I can see for every frame what
triggered it (an operator message, a publisher message, or a cascade-sent
message),".
