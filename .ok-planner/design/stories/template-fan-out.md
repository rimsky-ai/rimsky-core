---
story: template-fan-out
status: as-is
---

# Template author declares fan-out partitioning

## Story

As a template author writing a data-pipeline template, I can declare a fan-out node whose claim partitions into sub-claims and have rimsky dispatch one work unit per sub-claim concurrently, with the parent settling once all sub-claims resolve, so that I express parallel partition processing as a single template declaration.

Fan-out node declaration: rimsky materializes N sub-claims (via the producer's `SplitScope`), dispatches one node-run per sub-claim concurrently, and settles the parent with an aggregate outcome once all sub-claims resolve.

Template authors express parallel partition processing as a single template declaration — no manual per-partition wiring, no surprise serial dispatch.
