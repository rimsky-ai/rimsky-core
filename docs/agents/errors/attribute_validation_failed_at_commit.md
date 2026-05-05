---
error: attribute_validation_failed_at_commit
surfaced_to: executor
---

# Attribute validation failed at commit

## What it means

The executor's `Complete` event carried a writeback that did not validate against the node's `attributes:` schema. The supervisor rejected the commit; the node failed.

## When it happens

When an executor returns a writeback that drifts from what the node's schema declares. Common cases: missing required fields, wrong types, fields outside the schema's allowed shape.

## What to do

The executor must produce writeback shaped exactly to the schema. Check the writeback against the schema; correct either the writeback (if the executor is wrong) or the schema (if the schema is wrong). The validation runs at commit precisely to surface this drift early — don't add per-executor schema-loosening as a workaround.

## See also

- [`../../concepts/attributes.md`](../../concepts/attributes.md)
- [`../../concepts/executor.md`](../../concepts/executor.md)
