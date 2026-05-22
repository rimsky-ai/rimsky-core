---
concept: validation
status: as-is
aliases: []
references:
  - ../../specs/2026-05-15-data-platform-extensions-design.md
---

# Validation

## Definition

Cross-cutting service protocol. Any service may advertise it via `protocols: [..., validation]`. One method: `Validate(request) → response`.

```
ValidateRequest {
  string node_alias = 1;
  string role = 2;                // "executor" | "claim_producer" | "lifecycle_subscriber" | "sensor"
  oneof context {
    ExecutorContext executor = 3;
    ClaimProducerContext claim_producer = 4;
    LifecycleSubscriberContext lifecycle = 5;
    SensorContext sensor = 6;
  }
}

ExecutorContext {
  string node_alias        = 1;
  bytes  attributes_schema = 3;   // merged effective attribute schema for the node
  repeated string claim_aliases = 4;
}

ValidateResponse {
  bool valid = 1;
  repeated ValidationError errors = 2;
  repeated ValidationWarning warnings = 3;
}
```

Used at template-registration time to give services a say in whether a node's attributes + bindings make sense in their domain. The `ExecutorContext.attributes_schema` is the merged effective schema (executor's `expected_attributes_schema` ∪ template L1 defaults ∪ per-node L2 declaration).

## Boundaries

Owns: the `Validate` RPC surface, the role discriminator + per-role context types, the registration-time pipeline integration (`validation_pipeline.go` after the static `expected_attributes_schema` JSON-Schema check against the merged effective schema). Does NOT own: the per-service domain logic (lives in each service's impl), runtime per-call validation (registration-only V1). Adjacent: `concept:executor`, `concept:claim-producer`, `concept:lifecycle-subscriber`, `concept:sensor`, `concept:template`.

## Invariants

- Pipeline order at template registration: (1) static `expected_attributes_schema` JSON-Schema check from the executor's `ObservabilityCapabilities`, applied against the merged effective attribute schema (pure rimsky-side, no RPC); (2) `Validate` RPC against each service the node references that advertises `validation` for the relevant role; (3) errors at either step reject the registration, warnings surface to the operator.
- `Capabilities` of a Validation-supporting service advertises `validation_supported_roles: [...]` — the role discriminators the service is willing to validate.
- Failure mode for unreachable services at registration: `permissive_warn` default (registration succeeds with warning); operator-configurable to `strict` via `rimsky.yml`'s `registration.unreachable_validator: strict | permissive_warn`.

## Annotation sites

- `code:protocols/proto/v1/validation.proto` — protobuf surface.
- `code:runtime/validation_pipeline.go` — registration-time orchestration.
- `code:cmd/rimsky-validation-conformance/` — conformance suite (per-role test battery).
- `code:executors/verifier-shape-checks/validation.go` — reference impl for the executor role.

## Notes

Introduced by `.ok-planner/specs/2026-05-15-data-platform-extensions-design.md`. The method name is plain `Validate` because the request carries more than the executor's expected-attributes schema: claim bindings, sensor config, etc. The role discriminator + `oneof context` makes the request self-describing.

2026-05-21 — Userdata collapse. Validation pipeline input changes from `node_userdata` bytes to the merged effective attribute set. Schema check now against `expected_attributes_schema` (the executor's contribution to the effective schema). `@blessed-invariant 11` reference removed; attribute-value inertness covered by `concept:inertness`. See `.ok-planner/specs/2026-05-20-userdata-collapse-into-attributes-design.md`.
