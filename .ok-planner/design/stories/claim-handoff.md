---
story: claim-handoff
---

# Template author wires multi-node atomic staging via claim handoff

## Story

As a template author building a multi-node atomic-staging workflow, I can declare an upstream acquirer node that opens a claim and downstream co-holder nodes that share the same claim via the template's co-holdership directive — reading the live claim's address, payload fields, and scope bytes through alias-keyed substitution into the co-holder's attribute schema to do work against the staged location — then have the runtime fire Commit (all-success) or Abandon (any-failed) atomically across the holding subgraph, so that I compose stage-then-write-then-verify-then-commit pipelines (and similar all-or-nothing patterns) without re-acquiring the same claim from every node.
