---
assessment: one-message-per-frame--coalescing-burst
subject: story:one-message-per-frame
way: coalescing-burst
release: d977250c
outcome: held
warrant: experiment:one-message-per-frame
---
# One message per frame in the mode where merging is most plausible

The same burst was run against the instance mode that discards stale wakes (`catalog:template-keys/message_queue_mode`) — the mode where merging two bodies into one frame is the most plausible failure. It too delivered one message per frame: two messages across two frames, with one body resolved per run. Neither mode ever put two bodies in one frame, which is why no template has to refuse a coalesced frame at run time. A template author can rely on the guarantee whichever mode the instance is running in.

## Unverified remainder

The coalescing mode was driven with the same three-message burst, of which two survived. The way does not establish the guarantee under a burst large enough to cancel many wakes at once.
