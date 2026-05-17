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
  bytes node_userdata = 1;        // opaque
  string node_alias = 2;
  string role = 3;                // "executor" | "claim_producer" | "lifecycle_subscriber" | "sensor"
  oneof context {
    ExecutorContext executor = 4;
    ClaimProducerContext claim_producer = 5;
    LifecycleSubscriberContext lifecycle = 6;
    SensorContext sensor = 7;
  }
}

ValidateResponse {
  bool valid = 1;
  repeated ValidationError errors = 2;
  repeated ValidationWarning warnings = 3;
}
```

Used at template-registration time to give services a say in whether a node's userdata + bindings make sense in their domain.

## Boundaries

Owns: the `Validate` RPC surface, the role discriminator + per-role context types, the registration-time pipeline integration (`validation_pipeline.go` after the static `userdata_schema` JSON-Schema check). Does NOT own: the per-service domain logic (lives in each service's impl), runtime per-call validation (registration-only V1). Adjacent: `concept:executor`, `concept:claim-producer`, `concept:lifecycle-subscriber`, `concept:sensor`, `concept:template`.

## Invariants

- Pipeline order at template registration: (1) static `userdata_schema` JSON-Schema check from the executor's `Capabilities` (pure rimsky-side, no RPC); (2) `Validate` RPC against each service the node references that advertises `validation` for the relevant role; (3) errors at either step reject the registration, warnings surface to the operator.
- `Capabilities` of a Validation-supporting service advertises `validation_supported_roles: [...]` — the role discriminators the service is willing to validate.
- Preserves `@blessed-invariant 11` (userdata inert in rimsky): rimsky forwards opaque bytes; receives a verdict; never inspects content.
- Failure mode for unreachable services at registration: `permissive_warn` default (registration succeeds with warning); operator-configurable to `strict` via `rimsky.yml`'s `registration.unreachable_validator: strict | permissive_warn`.

## Annotation sites

- `code:protocols/proto/v1/validation.proto` — protobuf surface.
- `code:runtime/validation_pipeline.go` — registration-time orchestration.
- `code:cmd/rimsky-validation-conformance/` — conformance suite (per-role test battery).
- `code:executors/verifier-shape-checks/validation.go` — reference impl for the executor role.

## Notes

Introduced by `.ok-planner/specs/2026-05-15-data-platform-extensions-design.md`. The method name is plain `Validate` (not `ValidateUserdata`) because the request carries more than userdata: claim bindings, attribute schemas, sensor config, etc. The role discriminator + `oneof context` makes the request self-describing.
