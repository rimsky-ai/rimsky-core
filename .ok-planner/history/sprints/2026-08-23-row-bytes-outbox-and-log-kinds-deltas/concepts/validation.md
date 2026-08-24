---
concept: validation
---

# Validation

## What it is

Validation is a cross-cutting service protocol. Any service may advertise membership in it alongside its primary protocol. The protocol carries one method: rimsky asks a service whether a registration is acceptable, and the service answers with a verdict plus the errors and advisories it found. Rimsky asks at template-registration time, so a service gets a say in whether a node's attributes and bindings make sense in that service's own domain. Each call names the role whose registration it validates and carries the context that role needs. For the executor role that context includes the merged effective attribute schema — the union of the executor's advertised expected-attributes schema, the template's defaults, and the per-node declaration.

A service reaches this protocol two ways. A service that also speaks a primary protocol — claim producer, executor, publisher — carries a capabilities handshake, and rimsky reads that service's validation roles from the handshake. A standalone validator speaks only this protocol and carries no handshake; it declares in its own deployment-configuration entry the protocols whose registrations it validates, and rimsky reads its roles from that declaration.

## Purpose

Validation lets a service refuse a registration rimsky itself cannot judge. Rimsky checks a template against what it knows: declared schemas, references, and bindings. Only the service behind a node knows whether those bindings make sense in its own domain. This protocol asks it at registration time rather than at dispatch time, so a template author learns of the objection while registering instead of at the first run. Because it is a mix-in, a service offers the opinion without changing what it primarily is.

## Boundaries

Validation owns its one method, the role discriminator and the per-role context that travels with it, and its place in the registration pipeline. That place is fixed. Rimsky first runs its own static expected-attributes schema check, drawn from the executor's advertised observability capabilities and applied against the merged effective attribute schema; that step calls nobody. Rimsky then calls each service advertising validation for the relevant role: once per node for the executor and claim-producer roles, and once per template for the publisher and lifecycle-subscriber roles, since no template names a lifecycle subscriber and every registered subscriber advertising the role is consulted. An error from either step rejects the registration, and a warning surfaces to the operator.

Per-service domain logic is out: it lives inside each service. Runtime per-call validation is out, because validation runs at registration only.

See also `concept:executor`, `concept:claim-producer`, `concept:lifecycle-subscriber`, `concept:publisher`, `concept:template`.
