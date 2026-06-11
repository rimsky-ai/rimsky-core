---
story: template-fan-out
status: as-is
---

# Template author declares fan-out partitioning

## Role

As a template author writing a data-pipeline template, I can declare a fan-out node whose claim partitions into sub-claims and have rimsky dispatch one work unit per sub-claim concurrently, with the parent settling once all sub-claims resolve, so that I express parallel partition processing as a single template declaration.

## Capability

Fan-out node declaration: rimsky materializes N sub-claims (via the producer's `SplitScope`), dispatches one node-run per sub-claim concurrently, and settles the parent with an aggregate outcome once all sub-claims resolve.

## Business value

Template authors express parallel partition processing as a single template declaration — no manual per-partition wiring, no surprise serial dispatch.

## Acceptance

A template with a fan-out node whose claim-producer supports `SplitScope` and returns N sub-scopes; when the instance runs, rimsky materializes N sub-claims and dispatches one node-run per sub-claim concurrently against the producer's executor; once all N sub-runs reach terminal, the parent fan-out node settles with an aggregate outcome reflecting the sub-claims' resolutions.

## Falsifier

Sub-claims are materialized but not dispatched concurrently, OR the parent settles before all sub-claims resolve, OR aggregate outcome doesn't reflect the sub-claim resolutions.

## Proof

Executable proof.
