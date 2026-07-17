---
story: store-filesystem
status: as-is
---

# Operator uses filesystem-backed store

## Role

As an operator wiring a workflow whose claims persist on a POSIX filesystem, I can use the bundled filesystem store claim-producer to acquire directory-per-scope claims with sync write semantics, trigger on-demand queue refresh through an admin sync route when the sync strategy is set to explicit, and partition fan-out via SplitScope's per-request discriminator, so that I have a filesystem-backed store whose lifecycle guarantees match the write semantics it advertises.

## Capability

Bundled filesystem store claim-producer: directory-per-scope claims; sync-only write semantics (in-place writes, no staging); pick-policy actions on Commit/Abandon; explicit-sync admin route; SplitScope partitioning via a per-request discriminator (list / batch_pick / expand_folder).

## Business value

Operators get a filesystem-backed store whose lifecycle guarantees match the sync write semantics it advertises; nothing about the filesystem implementation undermines what holds for richer backends.

## Acceptance

A template referencing `store-filesystem`: `Open` returns the local directory path; `Commit` applies the configured pick-policy action in place, with no staging step and no swap; `Abandon` reverts the claim's pick-policy state, leaving no staged data behind because none is ever created; with `sync_strategy: explicit` and an empty queue, a call to the admin sync route picks up a newly-dropped folder and the next `Open` returns it; `SplitScope` partitions on the discriminator supplied in the request (list / batch_pick / expand_folder), never on a configured partition-key field.

## Falsifier

The explicit-sync route doesn't actually refresh the queue, OR any staging directory or stage-then-swap behavior appears in the filesystem store, OR SplitScope reads a configured partition-key field instead of the per-request discriminator.

## Proof

Executable proof.
