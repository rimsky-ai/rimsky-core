---
experiment: assumption-error-classes-namespaced-uniformly
commit: d977250c
---

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
