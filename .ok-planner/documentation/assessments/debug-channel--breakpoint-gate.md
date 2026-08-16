---
assessment: debug-channel--breakpoint-gate
subject: story:debug-channel
way: breakpoint-gate
release: d977250c
outcome: held
warrant: experiment:debug-channel
---
# Overriding at a breakpoint hit, on an instance that was never paused

A second instance, never paused but sitting at an unresumed pause-mode breakpoint hit, accepted both override actions, each answering with the breakpoint gate state, and the overridden value read back off the node under inspection. The invalidated node ran again once the hit was released. The channel then shut behind the session: with the hit released, the breakpoint deleted and the instance settled, the same override was refused again, naming the same two states. The channel is therefore open exactly in the two debug states the story names and shut on either side of them. Seven checks ran in this way and none failed.

## Unverified remainder

None: the passing run demonstrates the way as promised.
