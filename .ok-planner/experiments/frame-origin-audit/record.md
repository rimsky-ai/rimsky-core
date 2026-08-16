---
experiment: frame-origin-audit
commit: PENDING
---

# Every frame names the message that opened it

## What it ran against

A private docker network carrying a `rimsky-all-in-one` orchestrator and a
`rimsky-sensor-webhook` container whose HTTP listener is published to the host.
The template produces all three trigger kinds in one instance: an operator posts
a declared message type, a node with `sends_message` sends a message of its own
when the woken node settles, and an external POST to the webhook sensor arrives
as a publisher message. `run.py` builds and removes everything.

## What was observed

Ten checks, none failing. The instance opened three frames. Each one names a
triggering message id, a message type, a message sender and a message sender
kind. The three sender kinds are exactly `operator`, `publisher` and `instance`:
the operator-posted message, the webhook sensor's message, and the message the
instance itself sent, which carries the sending instance's id as its sender.

Reading each frame on its own returned the same trigger as the list gave, for
all three. Each frame's triggering message id resolved through the messages
route to a message whose type and sender kind match what the frame reported, for
all three. Narrowing the frame list by one triggering message id returned that
one frame.

One naming difference is worth recording: the story calls the third kind a
cascade-sent message, and the product reports it as sender kind `instance` with
the sender `instance:<id>`.
