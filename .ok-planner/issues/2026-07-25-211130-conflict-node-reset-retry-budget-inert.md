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

Operators can "reset" a failed node. The decision justifying the operation says it clears the node's error budget so the next attempt isn't skipped for budget exhaustion, and the node-admin story sells it the same way ("so that I restore acquisition eligibility for a subsequent invalidate-via-message"). But rimsky's retry budget is per-dispatch by design: every new run of a node starts its counter at zero (`concept:node-run`), so there is no cross-run budget for reset to clear — a subsequent invalidate already dispatches with a fresh budget whether or not reset ever ran. The handler confirms it: reset clears only the failed run's persisted settling-signal marker (`code:lib/control/controlapi/nodes.go::handleResetNode`), a genuinely observable effect on the node-inspect/CLI read surface, and nothing in gate evaluation or dispatch eligibility reads that field.

The operation is fine; its documented justification is impossible. Left as is, an operator debugging a stuck node reaches for reset expecting it to unblock acquisition, and it will not — the corpus is actively steering its reader wrong. Which way to fix it is a product call no rule forces: the honest narrowing keeps a verb whose whole effect is cosmetic clarity on the inspect surface, and whether that's worth a named operator verb is a judgment about the operator surface, not a compliance question.

## Options

- **Narrow the claim** — rewrite the decision's Choice/Rationale (and the story's benefit clause) to the real, code-verified effect: clearing the failed run's stale error marker for operator clarity, causally unrelated to dispatch eligibility. Keeps the endpoint; the story's promised benefit changes materially.
- **Retire the verb** — if a cosmetic-only marker-clear isn't worth a dedicated operator verb, remove endpoint, decision, and story clause together. Loses real, if modest, operator-clarity value.

The ruling decides between honesty about a small verb and not having the verb.

## Ruling

> Recommended ruling (/verify-issues): narrow the claim — amend
> decision:node-reset-as-pure-retry-budget-clear (title included)
> and story:node-admin's benefit clause to state the operation's
> actual effect, clearing the failed run's settling-signal marker on
> the operator inspect surface, and drop the retry-budget
> justification the per-dispatch budget makes impossible.
>
> Rationale: the marker-clear is genuinely observable and useful for
> operator clarity, and an honest small verb beats deleting working
> surface — retirement would spend churn to remove value. The flip
> case: if operators are ever observed reaching for reset to
> unstick nodes even after the docs narrow, the verb's name itself
> is the trap and retirement becomes the right call.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
