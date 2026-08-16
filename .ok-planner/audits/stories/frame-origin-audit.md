---
audit: frame-origin-audit
artifact: story:frame-origin-audit
text: noncompliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:45:38Z
---

# Every frame names the message that opened it, and which kind of sender sent it

Supported. Driven through the public surface against a released-image
orchestrator paired with the released webhook sensor on one network, on a
template that produces all three trigger kinds in a single instance: an operator
posts a declared message type, a node sends a message of its own when the woken
node settles, and an external POST reaches the sensor as a publisher message.
Ten checks, none failing. The instance opened three frames, and all three named
a triggering message id, type, sender and sender kind; the three sender kinds
were exactly the three origins the story enumerates, with the instance-sent one
carrying the sending instance's id as its sender. Reading each frame
individually returned the same trigger the list gave for all three, each
triggering message id resolved to a message whose type and sender kind matched
what the frame reported for all three, and narrowing the list by one triggering
message returned that one frame. One naming difference: the story calls the
third origin a cascade-sent message and the product reports it as sender kind
`instance`.

## Compliance

- The body names the delivery surface — "through the existing frame observability surface" pins the capability to one surface, which decisions own, and "existing" is build-record language a durable story does not carry; the compliant clause drops it, leaving "I can see for every frame what triggered it".
