---
issue: conflict-lifecycle-subscriber-vs-control-api-firing-site
kind: audit
category: conflicting
artifacts:
  - concept:lifecycle-subscriber
  - concept:control-api
  - concept:frame
status: verified
opened: 2026-07-25T03:18:31Z
---

# Who tells the outside world a run finished? The docs name two different senders

A "lifecycle subscriber" is an external service rimsky notifies when things happen to a template or an instance (a running workflow) — including a "run-scope-terminal" event fired when a top-level run of the graph finishes. Two rimsky processes plausibly send that notification: the scheduler, which drives the graph and notices ordinary completions, and the control API, the operator-facing HTTP surface that handles an operator force-terminating an instance. The lifecycle-subscriber concept document says the event always fires from the control API and never mentions the scheduler. Two sibling documents — control-API and "frame" (one pass through the graph) — say the opposite for the common case: an ordinary completion fires from the scheduler's engine at settlement, and the control API fires only for the separate administrative kill. The code confirms both firing sites are real and the two sibling docs describe them correctly; the lifecycle-subscriber doc is the odd one out, omitting the sender of the common case entirely.

There's a timing question layered on top. A separate, deliberately parked redesign sketch proposes decoupling how these notifications are delivered (a queue, so a slow subscriber can't block a database transaction) — and that redesign would rework this exact section of the doc anyway.

## Options

- **Fix the wording now, standalone** — reword lifecycle-subscriber to name both firing sites; code-verified, no behavior change; the parked redesign stays parked.
- **Fold the fix into the redesign** — avoids touching the section twice, at the cost of leaving the corpus contradicting itself until an unscheduled sprint happens.

The ruling decides now versus deferred.

## Ruling

> Recommended ruling (/recommend-rulings): Take the narrow fix now:
> reword concept:lifecycle-subscriber to name both firing sites
> (scheduler frame engine at settlement, control-api at administrative
> termination), matching control-api and frame, which stay untouched.
> The outbox revisit sketch stays parked, unblocked.
>
> Rationale: The fix is wording-only and code-verified; tying a live
> doc-vs-doc contradiction to an unscheduled redesign leaves the
> corpus disagreeing with itself indefinitely for no benefit.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
