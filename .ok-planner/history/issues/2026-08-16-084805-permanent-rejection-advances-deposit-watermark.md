---
issue: permanent-rejection-advances-deposit-watermark
kind: audit
category: conflicting
artifacts:
  - decision:deposit-detection-watermark
status: promoted
opened: 2026-08-16T08:48:05Z
sprint: 2026-08-17-accepted-intake-drain.md
---

# The deposit-watermark decision says a failed publish never advances the watermark; permanent rejections do

The object-store sensor tracks which deposits it has handed to rimsky with a watermark. The decision on that watermark says a failed publish does not advance it, giving at-least-once delivery. The sensor concept already says something more precise, with a rationale: a transient failure (transport error, server error, retryable status) does not advance state, but a permanent rejection (any other client error) is logged, dropped, and consumed — retrying forever wedges a misconfigured watch. The code does exactly the concept's split, with a test for each side. The decision's unqualified sentence is the stale one. The ruling decides the wording.

A reader of the decision alone expects a permanently rejected deposit to be redelivered; it never will be.

## Options

- Split the decision's Choice into transient/permanent to match the concept, referencing the concept's rationale; cost: none.
- Same, and move the rationale into the decision so guarantee and exception live in one place; cost: a placement choice, not a different outcome.

The ruling decides the wording; the split is already the design.

## Ruling

> Generated ruling (/verify-issues): Rewrite the decision's Choice to split by failure kind — a transient failure leaves the watermark alone (at-least-once holds); a permanent rejection is logged, dropped, and consumed like a success — carrying the sensor concept's rationale for not retrying forever. Forced by the current-state-only rule; the concept and the code already agree. Verified against the tree as it stands; nothing was applied.

<!-- Owner: this is a generated ruling, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
