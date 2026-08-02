---
concept: validation
---

# Validation

## What it is

Cross-cutting service protocol. Any service may advertise membership in this protocol alongside its primary protocol. One method: validate (request → response).

The request carries a role discriminator naming one of the protocols whose registration this call validates, and exactly one role-specific context selected by that discriminator. The executor-role context carries the node alias, the merged effective attribute schema for the node, and the node's claim aliases; the other roles carry their own analogous per-role context. The response carries a valid/invalid flag plus collections of validation errors and validation warnings.

Used at template-registration time to give services a say in whether a node's attributes + bindings make sense in their domain. The executor-role context's attribute-schema field is the merged effective schema (the union of the executor's advertised expected-attributes schema, the template's level-1 defaults, and the per-node level-2 declaration).

## Boundaries

Owns: the validate RPC surface, the role discriminator + per-role context types, the registration-time pipeline integration (run after the static expected-attributes schema check against the merged effective schema). Does NOT own: the per-service domain logic (lives in each service's implementation), runtime per-call validation (validation runs only at registration). Adjacent: `concept:executor`, `concept:claim-producer`, `concept:lifecycle-subscriber`, `concept:publisher`, `concept:template`.

## Invariants

- Pipeline order at template registration: (1) the static expected-attributes schema check from the executor's advertised observability capabilities, applied against the merged effective attribute schema (pure rimsky-side, no RPC); (2) validate RPC against each service advertising validation for the relevant role — per-node for the executor and claim-producer roles, per-template for the publisher and lifecycle-subscriber roles (a lifecycle subscriber is never named by the template, so every registered subscriber advertising the role is consulted template-wide); (3) errors at either step reject the registration, warnings surface to the operator.
- A validation-supporting service's capabilities advertise the set of role discriminators the service is willing to validate.
- Failure mode for unreachable services at registration: by default the registration succeeds with a warning; an operator-configurable deployment-level mode can flip this to strict so an unreachable validator rejects the registration outright.
