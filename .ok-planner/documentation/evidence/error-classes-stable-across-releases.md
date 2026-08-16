---
trap: error-classes-stable-across-releases
release: d977250c
---
# Evidence set — the 28 error classes are a stable, versioned vocabulary safe to hard-code in templates across upgrades.

Source of the prior: ecosystem-prior — a published, enumerable error-class catalog in an extensibility surface

## What the audit ran and observed (assumption record)

The experiment `assumption-error-classes-stable-across-releases` read the class
list each bundled executor advertises and put the same names through template
registration. The catalog is not a versioned vocabulary; it is whatever the
configured peers advertise at this deployment. The four bundled images
advertise 9, 5, 2 and 13 classes — 27 distinct — and the capabilities handshake
carries no version or deprecation marker anywhere in its fields
(`declaredErrorClasses`, `declaredTags`, `expectedAttributesSchema`,
`retentionAfterTerminalSeconds`, `supportsTraceGet`, `supportsTraceStream`).
Validity is per executor, not global: `agent/refused` warned on an http-node
node and registered clean on a claude-agent node, and `http/timeout` did the
reverse. The published list of 28 also matches neither what is advertised nor
what is raised — `spawn_failed` is advertised by no executor, and eight further
names (`template_resolution_failed`, `template_validation_failed`,
`executor_schema_unavailable`, `attributes_schema_failed`,
`unresolved_executor`, `executor_sync_timeout`, `executor_protocol_violation`,
`abandoned`) plus the whole `acquire/` family are accepted by the validator and
advertised by nobody. Whether the names survive an upgrade is not observable at
one commit; what is observable contradicts the prior's premise, because there
is no versioned catalog to be stable — swapping which executor a node names, or
which peers a deployment configures, changes which classes are recognised.

## Experiment record (experiment:assumption-error-classes-stable-across-releases)

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

Runnables: `src:.ok-planner/experiments/assumption-error-classes-stable-across-releases/` at the stamped commit.
