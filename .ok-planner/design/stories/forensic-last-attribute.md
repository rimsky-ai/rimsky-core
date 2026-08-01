---
story: forensic-last-attribute
status: as-is
---

# Operator reads node's latest attribute bag

## Story

As an operator debugging a node that has run at least once, I can read the node's most recent resolved attribute bag from the read surfaces directly, instead of hand-reconstructing it from the event log, so that I see what values the node actually computed without forensic effort.

Read surface that returns a node's most recent resolved attribute bag — the values dispatched to the executor, read from real persistence — alongside the rest of the node's state.

Operators see what values a node actually computed without forensic effort against the event log.
