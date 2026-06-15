---
story: store-filesystem
status: as-is
---

# Operator uses filesystem-backed store

## Role

As an operator wiring a workflow whose claims persist on a POSIX filesystem, I can use the bundled filesystem store claim-producer to acquire directory-per-scope claims, opt into atomic staging (stage-then-swap at Commit), trigger on-demand queue refresh through an admin sync route when the sync strategy is set to explicit, and partition fan-out via configurable partition keys, so that I have a filesystem-backed store with the same lifecycle and atomicity guarantees the protocol claims.

## Capability

Bundled filesystem store claim-producer: directory-per-scope claims; atomic stage-then-swap at Commit; explicit-sync admin route; configurable partition keys for the scope-split verb.

## Business value

Operators get a filesystem-backed store with the same lifecycle and atomicity guarantees the protocol claims; nothing about the filesystem implementation undermines what holds for richer backends.

## Acceptance

A template referencing `store-filesystem`: `Open` returns the local directory path; `Commit` performs an atomic POSIX rename swap of the staging dir into the canonical view; `Abandon` discards the staging dir; with `sync_strategy: explicit` and an empty queue, a call to the admin sync route picks up a newly-dropped folder and the next `Open` returns it; `SplitScope` partitions on the configured partition key.

## Falsifier

Commit's swap is a copy-then-overwrite, OR the explicit-sync route doesn't actually refresh the queue, OR staging dir survives Abandon.

## Proof

Executable proof.
