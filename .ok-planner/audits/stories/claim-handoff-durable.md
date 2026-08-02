---
audit: claim-handoff-durable
artifact: story:claim-handoff-durable
determination: supported
commit: b767a27d
audited: 2026-08-02T09:28:18Z
---

# Durable claims survive across instance dispatches and release only on explicit action

Supported. The `lifetime: durable` claim-producer-ref field flows into `spec.ClaimLifetimeDurable`, and `test/scenarios/claim_handoff_durable_e2e_test.go` exercises all five claimed behaviors as sub-tests: (A) a committed-durable claim survives both a normal and an artificially-aggressive retention sweep (`runtime.SweepClaimHandleRetention`), (B) a co-holder declared via `holds:` re-dispatches in a later message-triggered run within the same instance and reads the same durable claim's address by alias, (C) a competing acquirer against the same durable scope is refused (conflict detection includes committed-durable rows), (D) the asset-delete endpoint releases the durable row via the producer's release verb and a subsequent acquirer then succeeds, and (E) instance termination alone does not release a durable row but explicit `DELETE /v1/instances/{id}` does, firing the producer's release verb. All five sub-tests pass against a real gRPC claim-producer stub.
