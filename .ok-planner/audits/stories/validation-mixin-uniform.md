---
audit: validation-mixin-uniform
artifact: story:validation-mixin-uniform
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:29:05Z
---

# Service author advertises the validation mix-in from executor or publisher peers, not only claim producers

Supported. `lib/control/config/publishers.go::DialPublisherAndValidationRegistries` walks all 3 peer kinds a validation mix-in can ride alongside — claim producers (via `Capabilities().ValidationSupportedRoles`), executors (via `peer.FetchExecutorValidationRoles` against the executor-observability capabilities RPC), and publishers (via `peer.FetchPublisherValidationRoles` against the publisher capabilities RPC) — dialing each into the same `ValidationRegistry` when `validation` appears in its declared protocols. `lib/control/config/validation_mixin_uniform_test.go::TestValidationMixinUniformAcrossPeerKinds` stands up 3 stub peers, one per kind, each advertising identical `validation_supported_roles` on its own capability surface, and asserts the dialed `ValidationClient.SupportedRoles()` for all 3 come back non-empty and matching — proving the roles are honored identically regardless of which peer kind advertised them.
