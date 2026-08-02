---
audit: claim-producer-filesystem
artifact: story:claim-producer-filesystem
determination: supported
commit: b767a27d
audited: 2026-08-02T09:35:26Z
---

# Operator acquires directory-per-scope, sync-write, store-driven fan-out claims via the filesystem claim producer

Supported. The filesystem claim producer (`lib/services/claim_producers/filesystem/store`) advertises only `WriteSemanticsSync` and every `Open` returns `RealizedWriteSemantics: WriteSemanticsSync`, matching the promised synchronous in-place write semantics; `Store.Open` addresses a claim by a caller-given relative path under the configured root (the directory-per-scope addressing), and `openPickPolicy`/`runSync` derive the fan-out pool by listing the store's own directory entries under a pick policy's configured sub-root, filtering to directories and syncing an available/in-progress split from what actually exists on disk — partitioning work from the store's own contents rather than an external index. The gRPC-served conformance suite (`TestFsStore_ClaimProducerConformance` in `lib/services/test/scenarios/claim_producers/fs_claim_producer_conformance_test.go`) drives the full protocol conformance run plus the four split-scope checks end to end against a live filesystem-store server, and passes.
