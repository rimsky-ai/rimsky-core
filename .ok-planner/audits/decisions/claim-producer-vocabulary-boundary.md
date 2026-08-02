---
audit: claim-producer-vocabulary-boundary
artifact: decision:claim-producer-vocabulary-boundary
determination: supported
commit: b767a27d
audited: 2026-08-02T09:28:18Z
---

# The store→claim-producer rename covers shipped surfaces; storage-layer and test-machinery "store" naming persists by design

Supported. Checked all 9 `cmd/` binary directories (`rimsky`, `rimsky-supervisor`, `rimsky-host-agent`, `rimsky-host-agent-proxy`, `rimsky-scheduler`, `rimsky-control-api`, `rimsky-migrate`, `rimsky-entrypoint`, plus `internal`) — none is named or contains user-facing "store" vocabulary; config grammar uses `claim_producers:`/`ClaimProducerEntry` with no residual `stores:`/`store_name` keys anywhere under `lib/control/config`; and the 13 top-level `examples/` directories are named with claim-producer vocabulary (e.g. `examples/claimproducer`), with the only "store" mentions in their READMEs referring to generic backing-storage concepts, consistent with the decision's storage-layer exemption. That exemption itself is visibly exercised: both shipped producers keep a separate `store/` subpackage (`lib/services/claim_producers/{filesystem,postgres}/store/`) for their internal storage layer, and the test-fake helper package used across scenario tests is `test/support/claim_producers/stub/store`, matching the decision's stated test-machinery exemption.
