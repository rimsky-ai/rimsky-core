---
audit: validation-author
artifact: story:validation-author
determination: supported
commit: b767a27d
audited: 2026-08-02T09:29:05Z
---

# Service author writes a validation mix-in implementing the single Validation RPC

Supported. `lib/protocols/proto/v1/validation.proto` declares one `Validation.Validate` RPC with a `role`-discriminated oneof covering all four role contexts (`executor`, `claim_producer`, `lifecycle_subscriber`, `publisher`); `lib/runtime/validation_pipeline.go::RunValidationPipeline` calls all four at template registration — `runExecutorRoleCheck` and `runClaimProducerRoleChecks` per node, a publisher loop, and `runLifecycleSubscriberRoleChecks` over every validation-advertising peer — routing each RPC's `errors`/`warnings` into the registration outcome as blocking vs informational findings via `appendFindings`. `lib/control/controlapi/validation_pipeline_test.go` directly exercises all four roles at registration (`TestValidationPipeline_RejectsOnError`/`PassesOnWarningsOnly` for executor, plus dedicated `ClaimProducerRoleHonoredAtRegistration`, `PublisherRoleHonoredAtRegistration`, and `LifecycleSubscriberRoleHonoredAtRegistration` tests), and capabilities-handshake advertisement is proven for claim-producer, executor, and publisher peers in `lib/control/config/validation_mixin_uniform_test.go`.
