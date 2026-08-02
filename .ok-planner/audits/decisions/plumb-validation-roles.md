---
audit: plumb-validation-roles
artifact: decision:plumb-validation-roles
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:29:05Z
---

# The validation-registry dial walks every peer kind identically

Supported. `lib/control/config/publishers.go::DialPublisherAndValidationRegistries` builds one `peerSpec` list spanning all 3 peer kinds that can carry the mix-in — claim producers, executors, publishers — and for each of the 3, when `validation` is among the peer's declared protocols, fetches `validation_supported_roles` off that peer kind's own primary capability surface (claim-producer `Capabilities`, executor-observability `Capabilities`, publisher `Capabilities`, respectively) and dials the same `ValidationClient` construction path regardless of kind. `lib/control/config/validation_mixin_uniform_test.go::TestValidationMixinUniformAcrossPeerKinds` checks all 3 of these peer kinds by starting one stub server per kind advertising identical roles on its own capability RPC, and asserts the roles the registry ends up with for each are non-empty and identical to what was advertised — refuting the "two of three silently ignore the field" alternative this decision rejects.
