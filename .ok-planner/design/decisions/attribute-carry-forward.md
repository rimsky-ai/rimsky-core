---
decision: attribute-carry-forward
status: as-is
aliases: []
---

# Pre-substitution carry-forward step in attribute hydration

## Choice

On each dispatch of node X in RunScope S, the runtime hydrates the attribute bag from the most-recent prior node-run of X in S (via a lookup joining the per-run attribute ledger — indexed by node-run identity with denormalized node-id — with the node-run rows that carry their run-scope reference, selecting the most recent prior run for the (node, scope) pair), then applies per-field source-substitution from this dispatch's wait-set senders, overwriting carry-forward values for source-bound properties only. First dispatch of X in S falls back to the schema's static-default values. Executor-written read-only properties carry forward unchanged unless overwritten by a subsequent executor writeback. Cross-RunScope hydration is forbidden — sub-graph and fan-out RunScopes start with no carry-forward source, schema defaults apply. The existing sub-graph sealing semantics carry over. Default is on for all attribute properties uniformly — no opt-in flag.

**Frame-scope note.** Because RunScopes never span frames (per `concept:run-scope`, `concept:frame`), carry-forward is intra-frame by construction. Every new frame's root RunScope is freshly created and contains no prior node-runs of any node; the first dispatch of X inside a new frame's root RunScope falls into the "first dispatch of X in S" branch and hydrates from schema defaults. There is no path in the persistence layer that would hydrate a new frame's run from a prior frame's row: the same-(node, scope) lookup returns nothing across a frame boundary because the scope itself does not span the boundary. Cross-frame carry-forward is impossible, not merely discouraged.

## Rationale

Stateful nodes (the loop-counter's count attribute, the claude-agent executor's session-token attribute, and other executors that hold state in their own attributes) need their own prior writeback visible on the next dispatch. The denormalized index already exists for forensic lookups; making it load-bearing for hydration is the minimum change. Substitution overlay preserves cross-node data-flow semantics. Scope-bounded persistence matches sub-graph sealing. Both modes — stateful via executor-written carry-forward, refresh-from-upstream via source-bound substitution — coexist naturally in the same hydration path; template authors pick per property. Default-on keeps the model uniform; the pre-v1 break-freely posture covers any edge case, and the invisibility worry is asymmetric (executors that had no prior absence to rely on can't have logic that breaks when prior values appear).

## Alternatives

A self-substitution source kind (a new substitution grammar that lets a node read its own prior attributes) — requires extending the closed substitution grammar, doesn't naturally cover executor-written read-only properties. Shared bundle per (node, scope) mutated in place — loses per-run audit trail. Opt-in via schema flag — splits the property model and adds schema surface for a capability stateful nodes need by default.
