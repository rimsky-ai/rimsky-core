---
concept: cancel-siblings
---

# Cancel siblings

## What it is

Cancel siblings is the proactive cancellation a strict aggregation policy always performs. When one sub-claim of a fan-out parent resolves to abandonment — the child's own outcome, or a cancellation forced on it — and the parent's policy is strict, the runtime walks the parent's other in-flight sub-claims and force-abandons each one. This is what fail-fast means for a strict aggregation policy. It is not a setting of its own: choosing strict chooses this walk. A workflow that wants the surviving siblings to keep running after one failure chooses a different aggregation policy instead (see `concept:fan-out`). The parent claim-handle carries the aggregation policy the parent was acquired under, and the walk reads the policy there.

## Purpose

Cancel siblings stops work a strict fan-out can no longer use. Once one partition has failed, a strict parent can only fail. The remaining partitions then run for a verdict that is already settled, and they hold claims other work waits for. The walk ends that work and releases those claims at the moment the verdict becomes certain, rather than when the last sibling finishes on its own.

## Boundaries

Cancel siblings owns the proactive cancel walk and the filter that keeps the walk to the siblings the walking supervisor holds. A supervisor mutates only the claims it holds itself (see `concept:claim-handle`), so where a deployment runs several supervisors and they acquired sibling sub-claims of one parent in parallel, a strict parent's fail-fast reaches the siblings that supervisor acquired and no others. The siblings another supervisor holds run to their own outcomes, and the parent's verdict combines both sets.

The aggregate verdict computed once the sub-claims resolve belongs to `concept:fan-out`, and so does the meaning of the strict policy itself; `concept:node-run` only carries the policy value on the parent run for run-tree aggregation. Promoting a held claim to durable belongs to `concept:claim-lifetime`. The recursive descendant-cancel walk that fires on any abandonment, whatever the aggregation policy, belongs to `concept:claim-tree`. The run-level cancellation of the remaining in-flight clones — keyed off the run tree rather than the claim tree, so it reaches a clone that holds no claim to walk — belongs to `concept:fan-out` and `concept:node-run`.

see also: `claim-tree`, `fan-out`, `claim-handle`, `claim-co-holdership`, `claim-lifetime`, `node-run`
