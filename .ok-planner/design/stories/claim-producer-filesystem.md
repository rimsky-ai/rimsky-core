---
story: claim-producer-filesystem
status: as-is
---

# Operator uses filesystem-backed claim-producer

## Story

As an operator wiring a workflow whose claims persist on a POSIX filesystem, I can use the bundled filesystem claim-producer to acquire directory-per-scope claims with sync write semantics, trigger on-demand queue refresh through an admin sync route when the sync strategy is set to explicit, and partition fan-out via SplitScope's per-request discriminator, so that I have a filesystem-backed claim-producer whose lifecycle guarantees match the write semantics it advertises.

Bundled filesystem claim-producer: directory-per-scope claims; sync-only write semantics (in-place writes, no staging); pick-policy actions on Commit/Abandon; explicit-sync admin route; SplitScope partitioning via a per-request discriminator (list / batch_pick / expand_folder).

Operators get a filesystem-backed claim-producer whose lifecycle guarantees match the sync write semantics it advertises; nothing about the filesystem implementation undermines what holds for richer backends.
