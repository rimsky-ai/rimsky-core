---
concept: attribute
---

# Attribute

## What it is

Attributes are the typed inputs, outputs, and configuration of a node, declared by a schema the template carries for that node. Each property the schema declares takes one of three shapes. A source-bound property carries a substitution directive that resolves when the node-run's persisted bag is built (see `concept:node-run`). A static-default property carries a value the effective schema fixes at registration. An instance-level override replaces that value at dispatch, the substitution grammar does not apply to it, and an override that looks like a directive stays an ordinary literal string. An executor writes an executor-written property at commit, and marks that property read-only in the schema it advertises. Every value a run holds — source-resolved, static-default, and executor-written alike — persists on that run's own row in the per-run attribute ledger, so a later change to a template's default rewrites no history. Rimsky validates a node's attributes twice: once after substitution at dispatch, and once after the executor's writeback at commit.

## Purpose

Attributes give a node a typed, validated contract for what it consumes and what it produces. The substitution grammar lets a downstream node read an upstream node's outputs and a live claim's payload without rimsky understanding the data, and the two validation gates catch shape problems on both the input and the output side.

## Boundaries

Attributes own the schema, the substitution grammar, the three property shapes, the two validation gates, and the writeback ledger. They own five override layers, applied in order: the template's per-executor defaults, the node's own schema declaration, the instance's per-executor override, the instance's per-node override, and an ordered list of matcher-and-overlay entries whose matchers select on dispatch-time identity. A sixth layer, a one-shot resume overlay applied after those five and never persisted into the instance's stored overrides, belongs to `concept:breakpoint`.

An attributes delta rides on each of the two run-terminating executor verdicts and commits with the verdict in one transaction. The delta also appears in the emitted signal payload, so a subscriber can predicate on it. A long-running executor may post incremental writebacks mid-dispatch: each merges a delta onto that run's own attribute row and bumps the run's progress timestamp in the same transaction, because a genuine writeback is itself a liveness signal (see `decision:writeback-bumps-progress`). A mid-dispatch writeback touches only the in-flight run's own row, emits no signal, and fires no cascade; a cascade-visible attribute change rides the settling verdict alone. A park ends a dispatch but not a run and writes no attributes, so an executor threading state across a park uses scratch instead (see `concept:parked-state`), and its next run-terminating verdict after the wake is the next chance to settle the attribute row. An executor with nothing to write signals liveness on the dedicated keepalive surface without touching the attribute bag (see `decision:keepalive-endpoint`).

Attributes do not own a claim's payload, which belongs to `concept:claim`. They do not own assets, which are claims rather than attributes (see `concept:asset`): a template author writes both side by side, attributes for a run's transient inputs and outputs, assets for durable datasets. They do not own semantic validation, which a co-holding verifier node covers by running its own shape checks.

The substitution grammar is a closed enumeration of source kinds, and it admits no functions (see `decision:substitution-grammar-closed`); a directive admits at most one literal fallback (see `decision:substitution-grammar-fallback-routing`). Per-field substitution names one value per field, so many-to-many fan-in belongs to `concept:node-subscription` rather than to the grammar (see `decision:substitution-per-field-arity-one-to-one`). Cascade shape is declared on the receiver's subscriptions; the grammar carries no cascade-shape token. A subgraph is sealed: its internal nodes read no attribute from the calling graph's upstream nodes by free reference, and an author threads calling-graph state through the calling node instead (see `decision:subgraph-closure-no-free-upstream-reference`). The runtime reads no attribute row from an earlier frame while it decides anything in the current frame; state that must travel between frames rides the message payload, the instance params, a claim payload, or a threaded subgraph input (see `decision:no-cross-frame-attribute-caching`).

see also: `node`, `node-run`, `node-subscription`, `inertness`, `asset`, `terminal-tag`, `breakpoint`, `parked-state`, `run-scope`, `frame`
