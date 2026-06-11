---
story: forensic-last-attribute
status: as-is
---

# Operator reads node's latest attribute bag

## Role

As an operator debugging a node that has run at least once, I can read the node's most recent resolved attribute bag from the read surfaces directly, instead of hand-reconstructing it from the event log, so that I see what values the node actually computed without forensic effort.

## Capability

Read surface that returns a node's most recent resolved attribute bag — the values dispatched to the executor, read from real persistence — alongside the rest of the node's state.

## Business value

Operators see what values a node actually computed without forensic effort against the event log.

## Acceptance

After a node executes at least once, the operator queries the node through the control-api and observability surface and sees the latest resolved attribute bag — the values that were dispatched to the executor, read from real persistence — in the response. When the node has executed across multiple runs, the surface returns the most recent run's bag.

## Falsifier

The latest-attribute surface returns an earlier run's bag (stale), OR returns synthesized values, OR is absent on a node that has executed.

## Proof

Executable proof.
