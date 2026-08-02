---
audit: services-source
artifact: decision:services-source
determination: supported
commit: b767a27d
audited: 2026-08-02T09:28:57Z
---

# Compose manifest carries executors/claim-producers blocks; publishers/named-locks pass through from a sibling rimsky.yml

Supported. `cmd/rimsky/cli/compose/manifest.go`'s `Manifest` struct carries `Executors`/`ClaimProducers` maps with the same entry shape as `rimsky.yml` (transport, endpoint, TLS, protocols, and — matching the decision's specific call-out — a per-entry `WriteSemanticsAllowed` list on claim producers only), validated by `validateExecutorEntry`/`validateClaimProducerEntry` reusing the same `validProtocols`/`validWriteSemantics`/`validTLSMode` sets as the unified config loader. `Manifest` has no `Publishers` or `NamedLocks` field; `SiblingRimskyYMLPath` auto-discovers a `rimsky.yml` beside the manifest, and `synthetic_config.go` folds that sibling's `Publishers`/`NamedLocks` blocks straight through into the generated runtime config. Both the manifest schema (23 `Test*` functions in `manifest_test.go`) and the sibling passthrough (`TestLoadSiblingBlocks_PublishersAndNamedLocksFromSibling`, `TestLoadSiblingBlocks_EmptyPathNoOp` in `synthetic_config_test.go`) are directly tested.
