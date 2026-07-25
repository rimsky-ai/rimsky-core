---
issue: conflict-instance-kill-audit-row-signal-invariant
kind: audit
category: conflicting
artifacts:
  - concept:signal
  - concept:instance
  - concept:transition-reason
status: verified
opened: 2026-07-25T03:18:31Z
---

# Does killing an instance audit every run it kills? Three docs disagree; the code has an answer

When an operator force-terminates a running instance — a live execution of a workflow graph — every in-flight node-run inside it gets force-failed with an "instance killed" reason. Rimsky keeps a permanent audit ledger of node-run transitions, called signals; separately, "transition reason" is internal vocabulary for *why* a run changed state, deliberately kept out of the ledger's category system. Three design documents describe what the kill writes to that ledger, and they don't agree. The signal concept states an unconditional invariant: every transition affecting a node-run writes exactly one audit row, no exceptions. The instance concept agrees, saying the settling signal "is recorded on each run" during force-terminate. The transition-reason concept claims the opposite: an instance-kill produces exactly one audit row total — an administrative "instance terminated" event — with each per-run update touching only internal state, no audit row.

The code settles the factual question: the instance-kill handler updates each run's state *and* unconditionally writes an audit signal for that run — one row per killed run, always attempted. So the code and two of the three docs agree; only transition-reason's "no audit row" sentence disagrees. What remains is whether the code's behavior is the intended design.

## Options

- **Correct transition-reason's sentence** — the per-run write does emit a ledger row; the doc's broader, still-valid point (the reason kind itself is never an audit-event kind) stands untouched. No code change.
- **Treat the no-audit-row claim as intended** — remove the per-run audit write from the code, soften the instance doc's "recorded on each run" language, and add an explicit instance-kill exception to signal's unconditional invariant.

The ruling decides whether per-run audit rows on a kill are a promise or a bug.

## Ruling

> Recommended ruling (/recommend-rulings): The code is right: correct
> concept:transition-reason's 'no audit row' claim narrowly (the per-
> node kill write does emit a signal ledger row via the settling
> signal type-path), leaving its broader 'the reason kind is never an
> audit-event kind' point intact. concept:signal's unconditional-
> emission invariant stands.
>
> Rationale: Per-run audit rows are forensic value the unconditional
> invariant promises everywhere else; carving instance-kill out of
> signal's strongest commitment to save one sentence in transition-
> reason inverts the corpus's priorities.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
