---
audit: wire-commit-response-fields
artifact: decision:wire-commit-response-fields
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:40:04Z
---

# Both base Commit response fields are read and land where the contract promises

Supported. The commit result type carries the two fields the protocol's base response declares, and both client transports populate both: the gRPC client converts the response through a shared converter that reads the version id and the producer metadata, and the in-process client returns the handler's result unchanged. Production code calls the producer's Commit at exactly one site, the producer-verb outbox drain, and its result handler does both things the decision names — it writes the version id onto the claim-handle row through the claimant-guarded setter, which is that setter's only production call site and carries a cross-driver guard-suite case, and where the committed handle has a parent it merges the producer metadata into the fan-out parent's writeback row under a metadata map keyed by the child's partition. The same metadata merge is reached from the settle-children path, so both routes into parent settlement surface it. Two end-to-end scenarios cover the pair: a plain node whose version id is persisted, and a fan-out whose parent writeback carries the child's producer metadata.
