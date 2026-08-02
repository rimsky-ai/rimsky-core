---
audit: opaque-executor-scratch
artifact: story:opaque-executor-scratch
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:35:36Z
---

# Opaque bytes carried across recovery re-dispatch of the same node-run

Supported. A `scratch` byte field is defined on the dispatch request and on all three settling-Outcome variants (success, error, park) on the wire, documented and enforced as inert to rimsky — persisted on the dispatch row, never decoded or validated. An end-to-end test suite proves the full round trip for all 3 disposition kinds the protocol declares (retry-after-error, stale-recovery, recalculate): it writes random non-JSON binary content on a settling outcome and asserts the recovery dispatch's request carries those exact bytes back to the executor. Using non-decodable binary content is itself evidence of opacity — any attempted inspection or re-encoding by rimsky would corrupt the round trip, and it does not.
