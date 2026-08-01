---
story: claim-handoff
status: as-is
---

# Template author wires multi-node atomic staging via claim handoff

## Story

As a template author building a multi-node atomic-staging workflow, I can declare an upstream acquirer node that opens a claim and downstream co-holder nodes that share the same claim via the template's co-holdership directive — reading the live claim's address, payload fields, and scope bytes through alias-keyed substitution into the co-holder's attribute schema to do work against the staged location — then have the runtime fire Commit (all-success) or Abandon (any-failed) atomically across the holding subgraph, so that I compose stage-then-write-then-verify-then-commit pipelines (and similar all-or-nothing patterns) without re-acquiring the same claim from every node.

A downstream node declaring a co-holdership directive that binds an alias to an upstream node-type co-holds the upstream's claim by alias; at dispatch the runtime resolves the alias-keyed substitutions for the held claim's address, payload fields, and claim-scope against the held claim's actual bytes — the same acquired result the original acquirer received. Auto-terminal fires once every node in the holding subgraph settles non-active: Commit on all-success, Abandon on any-failed.

Multi-node atomic-staging composes naturally from existing template-DSL primitives. The author writes one acquirer plus N co-holders; rimsky enforces the all-or-nothing guarantee without bespoke rollback logic in template-land.
