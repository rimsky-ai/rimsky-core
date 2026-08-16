---
experiment: assumption-error-classes-stable-across-releases
commit: d977250c
---

# Asking what kind of thing the error-class catalog is

## What it ran against

All four bundled executor images, each probed on its
`ExecutorObservability.Capabilities` handshake by a client built against the
published `github.com/rimsky-ai/rimsky-core/lib/protocols` module, plus a
`rimsky-all-in-one` stack wiring two of them so the same class names can be put
through template registration.

## What was observed

The catalog is assembled at runtime from whatever executors are configured.
The four bundled images advertise 9, 5, 2 and 13 classes — 27 distinct between
them — and the capabilities message carries no version or deprecation marker:
its keys are `declaredErrorClasses`, `declaredTags`,
`expectedAttributesSchema`, `retentionAfterTerminalSeconds`,
`supportsTraceGet`, `supportsTraceStream`.

Class validity depends on which executor a node names. `agent/refused` warned
on an http-node node and registered clean on a claude-agent node;
`http/timeout` warned on the claude-agent node and registered clean on the
http-node one.

The advertised set is neither a superset nor a subset of what the product
raises. `spawn_failed` is advertised by none of the four. Eight further class
names — `template_resolution_failed`, `template_validation_failed`,
`executor_schema_unavailable`, `attributes_schema_failed`,
`unresolved_executor`, `executor_sync_timeout`, `executor_protocol_violation`,
`abandoned` — plus the whole `acquire/` family are accepted by the validator
without warning and advertised by no executor at all.
