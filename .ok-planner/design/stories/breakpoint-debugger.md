---
story: breakpoint-debugger
status: as-is
---

# Operator debugs live instance via breakpoints

## Role

As an operator, I can install a breakpoint on a running instance's checkpoint, see hits appear both on the unified event log and on the breakpoint-hits ledger, resume a paused hit with an attribute overlay that the supervisor applies on re-fire, and delete a breakpoint to cascade-clear its hits, so that I debug a live instance.

## Capability

Live-instance debugging: install / list / delete breakpoints; co-transactional hit emission to both the unified event feed and the breakpoint-hits ledger; resume with attribute overlay.

## Business value

Operators debug live instances — pause at a checkpoint, inspect, overlay attributes on re-fire — through a coherent surface where the event feed and the breakpoint-hits ledger never disagree.

