---
story: attribute-carry-forward
status: as-is
aliases: []
---

# Stateful attribute carry-forward within a RunScope

## Role and capability

As a template author, I can write a node whose executor sets an output attribute and observe that value present in the incoming attribute bag on subsequent dispatches of the same node within the same RunScope; in a new RunScope (sub-graph invocation, fan-out partition) the same node starts with the schema's defaults, so stateful nodes hold their state in their own attributes uniformly across the platform.

## Acceptance

I declare a node whose executor sets a counter attribute on its terminal; cascade re-fires the node within the same RunScope; on the next dispatch, the incoming attribute bag contains the prior counter value. The same node in a fresh RunScope's first dispatch sees the schema's default for that counter attribute, not any prior scope's value.

## Falsifier

A node's executor sets a counter attribute to a specific value on terminal; the cascade re-fires within the same scope; the next dispatch's incoming attribute bag is missing the prior writeback (either absent, set to the schema default, or replaced only by source substitution with no carry-forward overlay). OR: a sub-graph invocation's first dispatch inherits the calling scope's prior writeback.

## Proof

Demo — a scenario test that runs a deterministic stateful node through three dispatches in one RunScope (observes the counter attribute advance step by step via writeback plus carry-forward), then invokes a sub-graph and observes a node in the sub-graph sees the schema default.
