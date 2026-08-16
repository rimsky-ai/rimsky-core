---
trap: error-classes-namespaced-uniformly
release: d977250c
---
# Evidence set — every error class is namespaced by its emitter, so runtime-synthesized classes carry a prefix the way `agent/`, `http/`, and `verifier/` do, and `error_types` keys can always be written as `<prefix>/*`.

Source of the prior: sibling-symmetry — `spawn_failed` carrying no prefix among 27 prefixed classes

## What the audit ran and observed (assumption record)

The experiment `assumption-error-classes-namespaced-uniformly` registered one
node per candidate key and then provoked a real `http/network_error`. Both
halves of the prior fail. Eight class names carry no emitter prefix and are
first-class vocabulary members the validator accepts without a warning:
`template_resolution_failed`, `template_validation_failed`,
`executor_schema_unavailable`, `attributes_schema_failed`,
`unresolved_executor`, `executor_sync_timeout`, `executor_protocol_violation`
and `abandoned` — the classes rimsky raises itself. `spawn_failed`, the
prefixless class in the published catalog, warned on both an http-node node and
a claude-agent node. And `<prefix>/*` is not a key at all: `http/*` registered
with a vocabulary warning, and at dispatch the exact key `http/network_error`
routed the failure and settled the node fresh while `http/*` — and even
`http/request_invalid/*`, a family the executor itself advertises — left the
same failure unrouted and the node failed. The one wildcard the validator
accepts is `acquire/*`, which is the synthetic acquire family, not an emitter
prefix. A template author who writes error routing in families gets a policy
that registers and never fires.

## Experiment record (experiment:assumption-error-classes-namespaced-uniformly)

# Asking whether error classes carry an emitter prefix, and whether prefixes route

## What it ran against

A `rimsky-all-in-one` stack with an ordinary `rimsky-executor-http-node` and a
stub-mode `rimsky-executor-claude-agent`. The run registers one node per
candidate class key and reads the validator's vocabulary warnings, then
provokes a real `http/network_error` — a node whose URL does not resolve —
against nodes keyed the exact way and the family way.

## What was observed

Eight prefixless class names registered with no vocabulary warning at all:
`template_resolution_failed`, `template_validation_failed`,
`executor_schema_unavailable`, `attributes_schema_failed`,
`unresolved_executor`, `executor_sync_timeout`, `executor_protocol_violation`
and `abandoned`. These are classes rimsky raises itself, and the validator
recognises every one — so the vocabulary contains names no `<prefix>/*` key can
address.

`spawn_failed` warned on both an http-node node and a claude-agent node: it is
prefixless and in no declared vocabulary either.

Class validity is per executor. `agent/refused` warned on an http-node node and
registered clean on a claude-agent node.

Prefix families are not keys. `http/*` registered with a warning, and
`acquire/*` was the only wildcard form the validator accepted. At dispatch the
distinction is total: the exact key `http/network_error` routed the failure and
the node settled `fresh`, while `http/*` and even
`http/request_invalid/*` — a family the executor itself advertises — left the
same failure unrouted and the node `failed`.

Runnables: `src:.ok-planner/experiments/assumption-error-classes-namespaced-uniformly/` at the stamped commit.
