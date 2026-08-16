---
assessment: message-bus--replay-without-duplicate
subject: story:message-bus
way: replay-without-duplicate
release: d977250c
outcome: held
warrant: experiment:message-bus
---
# Replaying a send returns the original message instead of creating a second one

Replay was measured in the two ways it can come apart. The audit re-sent under the same dedup key with an identical body, and again under the same key with a different body; both returned the identity the first send created, and the instance's history held one row for that key rather than three. A replay therefore neither duplicates the message nor smuggles a second body in under an already-accepted key — the second point is the one that could quietly not hold, and it does hold. Downstream nodes consumed only the bodies the bus actually accepted.

## Unverified remainder

Replays were driven sequentially against a live instance. The way does not establish the outcome of replays that arrive concurrently, or after the instance has reached a terminal state.
