---
assumption: error-classes-stable-across-releases
commit: PENDING
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# the 28 error classes are a stable, versioned vocabulary safe to hard-code in templates across upgrades.

As operator whose templates key on error classes, I would take it that the 28 error classes are a stable, versioned vocabulary safe to hard-code in templates across upgrades.

## Source

ecosystem-prior — a published, enumerable error-class catalog in an extensibility surface

## What a run would observe

compare the class list a bundled executor advertises in its capabilities handshake against the published catalog.

## Measured

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
