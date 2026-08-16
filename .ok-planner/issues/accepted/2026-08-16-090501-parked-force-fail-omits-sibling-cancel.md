---
issue: parked-force-fail-omits-sibling-cancel
kind: audit
category: inconsistent
artifacts:
  - concept:parked-state
  - concept:node-run
status: verified
opened: 2026-08-16T09:05:01Z
---

# The parked-state concept says only an instance kill force-fails a parked run; sibling cancellation does too

A parked node-run waits for a wake. The parked-state concept says a parked row is force-failed only by an instance kill. The state machine — owned by the node-run concept, whose transition table already lists both reasons — also admits fan-out sibling cancellation out of parked into failed, and the cancellation walk force-fails every non-terminal run in the cancelled subtree, parked included. The two concepts disagree independently of the code. The ruling aligns parked-state to the owner of the transition table.

## Options

- Enumerate both reasons in parked-state's own invariant; cost: a duplicated enumeration that can drift again.
- Generalize the clause (a parked row is force-failed by the same run-tree cancellations that force-fail any non-terminal run) and defer the reason list to node-run; cost: none — it matches the ownership both concepts already state.

The ruling decides which concept owns the reason list.

## Ruling

> Generated ruling (/verify-issues): Rewrite parked-state's invariant so it no longer says "only an instance kill" — a parked row is force-failed by the run-tree cancellations that force-fail any non-terminal run — and defer the transition-reason enumeration to the node-run concept, which owns the state machine and already lists both. Forced by the self-containment rule and node-run's stated ownership. Verified against the tree as it stands; nothing was applied.

<!-- Owner: this is a generated ruling, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
