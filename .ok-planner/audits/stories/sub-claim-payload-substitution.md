---
audit: sub-claim-payload-substitution
artifact: story:sub-claim-payload-substitution
determination: supported
commit: b767a27d
audited: 2026-08-02T09:32:02Z
---

# Sub-claim payload reads through the same `{{claim.<alias>.payload[.<field>]}}` path as a regular claim

Supported. A fan-out child re-acquiring its inherited sub-claim (`reuseLinkedSubClaim` in `lib/runtime`) returns an `AcquiredLock` whose `ClaimResult.Payload` is populated from the persisted sub-claim's `Payload` column, using the identical `claimproducer.ClaimResult` shape a regular Open'd claim produces; the dispatch-time resolve context (`claimsMapFromAcq`) builds its `claim.<alias>` map from `acquisition.Locks` without distinguishing sub-claims from ordinary claims, so both flow through the single `resolveClaimValue` payload-path-walking function in the substitution package — there is no second code path. A dedicated runtime test (`TestReuseLinkedSubClaim_ChildRunAttachesWithoutReOpen`) exercises the sub-claim reuse and its resulting `AcquiredLock` shape end to end.
