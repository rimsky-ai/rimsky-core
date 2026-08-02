---
audit: wire-commit-response-fields
artifact: decision:wire-commit-response-fields
determination: supported
commit: b767a27d
audited: 2026-08-02T09:35:26Z
---

# Base Commit response fields are read and applied at the claim-spine and child-execution seams

Supported. `lib/runtime/producer_verb_outbox.go::applyDeferredCommitResult` (carrying the decision's own citation tag) is invoked from the single delivery path for base-protocol Commit verbs and persists the returned `version_id` onto the claim-handle row via `ClaimHandles.SetVersionID`, and — when the delivered claim has a fan-out parent — surfaces `producer_metadata` into the parent's writeback via `recordChildCommitMetadata`; both mechanisms are the same ones the data-processing mix-in's `CommitCandidate` path (`terminal_decision.go`) already used for its own version-id, closing the gap for the base protocol. `test/scenarios/commit_response_fields_test.go` exercises both effects end to end (`TestCommitResponseFields_PlainNode_VersionIDPersisted`, `TestCommitResponseFields_FanOut_ProducerMetadataInParentWriteback`) against a real stub producer returning fixed response fields through the base protocol.
