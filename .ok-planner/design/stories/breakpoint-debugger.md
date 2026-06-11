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

## Acceptance

Through the control-api or MCP, an operator installs a breakpoint on a node's checkpoint; when the supervisor evaluates the checkpoint, the hit appears co-transactionally in both the breakpoint-hits ledger and the unified event feed (a debugger tailing the event stream sees the hit); resuming a paused hit with an attribute overlay causes the next dispatch to actually carry the overlaid attributes; deleting the breakpoint removes both it and its hits.

## Falsifier

Hit appears on one surface but not the other (not co-transactional), OR resume's overlay isn't applied at the next dispatch, OR breakpoint deletion leaves orphaned hits.

## Proof

Executable proof.
