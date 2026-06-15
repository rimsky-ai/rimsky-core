---
decision: untagged-prose-by-module
status: as-is
---

# Untagged-prose sweep decomposes by module root

## Choice

Untagged-prose comment-hygiene violations decompose into one sweep per top-level module root, using the splitting axis described by `concept:module-layout`. Pass count equals module-root count; pass sizing is uneven by design. Within each pass, every site is resolved per `decision:comment-hygiene-uniform-rule`.

## Rationale

A single sweep over thousands of judgment-only sites is not validator-reviewable. The module-layout axis is the project's existing coherent review boundary — it's the axis the import-boundary rules and multi-module split already use — so per-module passes match how reviewers (human and agent) already navigate the tree.

## Alternatives

Bucketing the violations into fixed-size passes — rejected because module-coherent review surfaces beat uniform-size buckets when the work is per-site judgment.
