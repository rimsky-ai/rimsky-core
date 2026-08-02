---
audit: producer-declared-classes-capability
artifact: decision:producer-declared-classes-capability
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:18Z
---

# CapabilitiesResponse.declared_error_classes flows into the same discovery cache as executor vocabularies

Supported. `claim_producer.proto::CapabilitiesResponse.declared_error_classes` (field 6) is documented as optional and mirrors the executor-observability declaration exactly as the decision states. The out-of-process handshake path probes it (`lib/control/observability/handshake.go::ProbeClaimProducerDeclaredErrorClasses`) and stores it on the shared `ObservabilityCapabilities.DeclaredErrorClasses` field used by both executor and claim-producer discovery-cache entries; the in-process bundled-registration path (`lib/control/config/bundled.go::AdvertiseInto`) sets the same field for claim producers while deliberately leaving schema/tags unset for them — matching `concept:discovery-cache`'s statement that an in-proc claim-producer handler advertises declared error classes only. Declaring nothing remains legal: the validator and runtime both treat an empty/absent vocabulary as "no vocabulary known" rather than an error, confirmed by the `TestProducerClassRouting_*` scenario tests which only assert warnings when a vocabulary is actually declared.
