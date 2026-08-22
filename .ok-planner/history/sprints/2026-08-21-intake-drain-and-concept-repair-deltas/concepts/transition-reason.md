---
concept: transition-reason
---

# Transition reason

## What it is

The transition reason is the closed vocabulary carried on every node-state transition: a set of named values, each carrying a kind discriminator. The state-machine code owns membership of the set. Every path that changes a node-run's state writes a reason, and the next-state function is the only reader. The reason is never an audit identity.

## Purpose

The transition reason exists for one narrow role: state-machine validation inside the next-state function. Every transition consults that function, which switches on the current state paired with the reason and returns either the next state or an illegal-transition sentinel. The reason is the load-bearing input, and without it the machine could not reject a double execute or any other illegal sequence. Audit identity lives elsewhere: an audit-event row carries a canonical signal type-path or an operational kind (see `concept:signal`, `concept:event-log`), never the reason's kind discriminator.

## Boundaries

The transition reason owns the closed reason vocabulary and the per-state validation switch in the next-state function — the state machine's load-bearing rejection of illegal transitions. Audit-event kinds are out: signal type-paths belong to `concept:signal`, operational kinds and the event log's own mechanics belong to `concept:event-log`. Dispatch eligibility is out (see also `concept:node-run`), and so is the separate vocabulary that records why a node-run was created rather than why it transitioned. The cascade-fire gate is out, because subscribers drive it (see also `concept:cascade`).
