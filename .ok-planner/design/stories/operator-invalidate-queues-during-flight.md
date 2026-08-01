---
story: operator-invalidate-queues-during-flight
status: as-is
---

# Operator-invalidate during in-flight queues rather than drops

## Story

As an operator forcing a re-run of a node that currently has an in-flight run, I can rely on my invalidate producing a queued run that dispatches after the in-flight one settles, so that my action is neither silently dropped nor destructive to the work already in flight.
