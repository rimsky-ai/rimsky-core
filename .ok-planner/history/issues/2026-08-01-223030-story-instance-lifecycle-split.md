---
issue: story-instance-lifecycle-split
kind: sprint
category: stories-splits
artifacts:
  - story:instance-lifecycle
status: retired
opened: 2026-08-01T22:30:30Z
---

# Should the instance-lifecycle story split, given create already stands alone?

The instance-lifecycle story bundles five operator verbs — create, watch, pause/resume, force-terminate, remove — in one sentence. A sibling story, `story:instance-create-is-idle`, already carves out create with a narrower promise: creating an instance has no side effects; creating and invoking are separate actions. So the filed worry is double: the bundle fuses five outcomes, and its "create" overlaps a standing sibling.

Re-verification shows the overlap is not a duplicate. The two stories promise different things: instance-lifecycle's benefit is broad (drive an instance's runtime existence and intervene when something goes wrong), while instance-create-is-idle's is the specific idle-on-create guarantee — a load-bearing commitment a design decision cites to justify its own choice (`decision:compose-driver-sends-empty-message-after-create`). The corpus documents the five verbs as separate mechanisms (`concept:instance`) but states no story-boundary rule.

## Options

- Split all five verbs into their own stories and drop the create overlap — five files for one operator persona's workflow.
- Keep the bundle but strip its create clause, leaving create solely to the narrower sibling — removes the redundancy, at the cost of a lifecycle story that no longer names its own starting verb.
- Keep the bundle whole and rule the overlap harmless — mild redundancy in what "create" promises, since the two benefits differ.

The ruling decides whether the umbrella survives and what happens to its create clause. Siblings `issue:story-node-admin-split`, `issue:story-http-node-split`, and `issue:story-runtime-diagnostics-split` pose the same granularity question and should be ruled consistently.

## Ruling

> Recommended ruling (/verify-issues): keep the umbrella story whole, create clause included, and rule the overlap harmless. The lifecycle story promises the verb exists in the operator's toolkit; the idle-on-create story promises what that verb does *not* do — different commitments that happen to share a word.
>
> Rationale: stripping the create clause leaves a lifecycle story that mysteriously starts at "watch", which misleads more than the redundancy costs; a five-way split fails the same test as the sibling split issues, since no single verb is an independently adoptable outcome. Flip case: if the two stories' create language ever drifts into stating the same guarantee twice — both claiming side-effect behavior — the overlap stops being harmless and the clause should move to one home.

Retired at /plan-sprint 2026-08-01-ruled-intake-drain: the accepted ruling keeps the umbrella story whole with the overlap ruled harmless — no corpus change, so nothing to promote.
