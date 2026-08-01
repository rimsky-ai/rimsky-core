---
story: live-progress
status: as-is
---

# Operator sees per-node lifecycle as it happens

## Role

As an operator watching a one-shot run, I can see per-node lifecycle as it happens, so that I have situational awareness during execution and can distinguish hangs from healthy work.

## Capability

While the run is in flight, the one-shot verb streams per-node lifecycle output to the operator's terminal in time with execution: one line per instance creation, one line per node-run terminal naming the node and its outcome, and one line per instance terminal. Lines appear as the events occur, flushed individually rather than batched, so a slow node visibly stalls the stream and a healthy run visibly progresses.

## Business value

An operator watching a long run can tell at a glance which nodes have completed and which are still working, distinguish a stall from a healthy slow node, and abort early if a run is going wrong without waiting for a final summary.

