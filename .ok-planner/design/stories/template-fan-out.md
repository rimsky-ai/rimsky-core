---
story: template-fan-out
---

# Template author declares fan-out partitioning

## Story

As a template author writing a data-pipeline template, I can declare a fan-out node whose claim partitions into sub-claims and have rimsky dispatch one work unit per sub-claim concurrently, with the parent settling once all sub-claims resolve, so that I express parallel partition processing as a single template declaration.
