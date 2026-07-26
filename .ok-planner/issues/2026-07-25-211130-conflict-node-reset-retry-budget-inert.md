---
issue: conflict-node-reset-retry-budget-inert
kind: audit
category: conflicting
artifacts:
  - decision:node-reset-as-pure-retry-budget-clear
  - concept:node-run
status: verified
opened: 2026-07-25T21:11:30Z
---

# Node-reset's stated purpose — clearing a retry budget — describes something the operation cannot do

Operators can "reset" a failed node. The decision justifying the operation says it clears the node's error budget so the next attempt isn't skipped for budget exhaustion, and the node-admin story sells it the same way. But rimsky's retry budget is per-dispatch by design: every new run of a node starts its retry counter at zero (`concept:node-run`, `decision:in-place-retry`), so there is no cross-run budget for reset to clear. Reading the handler confirms it: reset only clears the failed run's persisted settling-signal marker (`code:lib/control/controlapi/nodes.go::handleResetNode`) — an observability effect on the node-inspect surface, useful for operator clarity, but causally unrelated to whether the next run dispatches.

The operation is fine; its documented justification is wrong. Left as is, an operator debugging a stuck node would reach for reset expecting it to unblock acquisition, and it will not.

## Options

- Rewrite the decision's Choice/Rationale (and the story's business-value clause) to state the real effect: clearing the failed run's settling-signal marker for the operator surface. Cost: sprint work only.
- Retire node-reset entirely if the real effect isn't worth an operator verb — a product call the observability value argues against.

## Ruling

> Generated ruling (/verify-issues): amend `decision:node-reset-as-pure-retry-budget-clear`
> (and `story:node-admin`'s corresponding clause) to describe the operation's actual,
> code-verified effect — clearing the failed run's settling-signal marker — and drop
> the retry-budget claim, which the per-dispatch budget invariant makes impossible.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
