---
audit: commit-response-honored
artifact: story:commit-response-honored
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:35:26Z
---

# Base-protocol Commit response's version-id and producer-metadata are honored, not just the data-processing mix-in's

Supported. The base claim-producer protocol's `Commit` returns a `CommitResult{VersionID, ProducerMetadata}` (`lib/protocols/claimproducer/types.go`), and every base-protocol Commit is delivered through the single producer-verb-outbox dispatch path (`deliverProducerVerb` in `lib/runtime/producer_verb_outbox.go`), which calls `applyDeferredCommitResult` to persist `VersionID` onto the claim-handle row via `ClaimHandles.SetVersionID` and, when the claim has a fan-out parent, surface `ProducerMetadata` into the parent's writeback via `recordChildCommitMetadata` — independent of the separate data-processing `CommitCandidate` path in `terminal_decision.go` that already handled this for that mix-in. Two dedicated end-to-end scenario tests (`test/scenarios/commit_response_fields_test.go`) drive a stub producer configured to return a fixed version-id and metadata through the base protocol and assert: the plain node's claim-handle row lands with that exact `version_id`, and — for a three-way fan-out — the parent's writeback JSON carries `producer_metadata` keyed by partition with the children's base64-encoded metadata bytes verbatim.
