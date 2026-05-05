---
error: unresolved_executor
surfaced_to: operator
---

# Unresolved executor

## What it means

A node's `executor:` field references an executor name that is not present in `rimsky.yml`'s `executors:` block. The supervisor cannot route the dispatch.

## When it happens

When deploying a template that references an executor the operator has not yet wired up, or when an `rimsky.yml` change drops an executor entry that templates still reference.

## What to do

Either add the executor entry to `rimsky.yml` and restart the supervisor, or change the template's `executor:` field to a name that is configured.

## See also

- [`../../concepts/executor.md`](../../concepts/executor.md)
- [`../../concepts/node.md`](../../concepts/node.md)
