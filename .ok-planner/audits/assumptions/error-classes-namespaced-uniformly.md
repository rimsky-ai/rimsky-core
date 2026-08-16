---
assumption: error-classes-namespaced-uniformly
commit: d977250c
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# every error class is namespaced by its emitter, so runtime-synthesized classes carry a prefix the way `agent/`, `http/`, and `verifier/` do, and `error_types` keys can always be written as `<prefix>/*`.

As template author writing error routing, I would take it that every error class is namespaced by its emitter, so runtime-synthesized classes carry a prefix the way `agent/`, `http/`, and `verifier/` do, and `error_types` keys can always be written as `<prefix>/*`.

## Source

sibling-symmetry — `spawn_failed` carrying no prefix among 27 prefixed classes

## What a run would observe

declare `error_types` keyed on `spawn_failed` and on a bare `*` catch-all and see which the registration validator accepts.

## Measured

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
