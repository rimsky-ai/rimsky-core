---
concept: validation
status: as-is
aliases: []
---

# Validation

## Definition

Cross-cutting service protocol. Any service may advertise it via `protocols: [..., validation]`. One method: validate (request → response).

The request carries the node alias, a role discriminator (`executor | claim_producer | lifecycle_subscriber | sensor`), and exactly one role-specific context selected by that discriminator. The executor-role context carries the node alias, the merged effective attribute schema for the node, and the node's claim aliases; the other roles carry their own analogous per-role context. The response carries a valid/invalid flag plus collections of validation errors and validation warnings.

Used at template-registration time to give services a say in whether a node's attributes + bindings make sense in their domain. The executor-role context's attribute-schema field is the merged effective schema (executor's `expected_attributes_schema` ∪ template L1 defaults ∪ per-node L2 declaration).

## Boundaries

Owns: the validate RPC surface, the role discriminator + per-role context types, the registration-time pipeline integration (run after the static `expected_attributes_schema` JSON-Schema check against the merged effective schema). Does NOT own: the per-service domain logic (lives in each service's implementation), runtime per-call validation (registration-only V1). Adjacent: `concept:executor`, `concept:claim-producer`, `concept:lifecycle-subscriber`, `concept:sensor`, `concept:template`.

## Invariants

- Pipeline order at template registration: (1) static `expected_attributes_schema` JSON-Schema check from the executor's advertised observability capabilities, applied against the merged effective attribute schema (pure rimsky-side, no RPC); (2) validate RPC against each service the node references that advertises `validation` for the relevant role; (3) errors at either step reject the registration, warnings surface to the operator.
- A validation-supporting service's capabilities advertise `validation_supported_roles: [...]` — the role discriminators the service is willing to validate.
- Failure mode for unreachable services at registration: `permissive_warn` default (registration succeeds with warning); operator-configurable to `strict` via a deployment-level unreachable-validator setting (`strict | permissive_warn`).
