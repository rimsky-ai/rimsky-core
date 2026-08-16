---
audit: substitution-deps-from-persisted-senders
artifact: decision:substitution-deps-from-persisted-senders
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:33:29Z
---

# Deps come from the subscription map and the senders' persisted rows in this frame's scope

Supported. One builder produces the substitution deps and it is called from exactly two places — the gate evaluator's pending-to-stale path and the acquisition-time resolve-context builder — which is the single-builder claim. It enumerates the receiver's senders from the template's subscription-edge map, then for each sender reads that sender's attribute row from the per-run attribute ledger. The lookup is scoped by the receiver run's RunScope, and because a RunScope belongs to exactly one frame, rows from any other frame are structurally unreachable rather than filtered out. Round-driving senders resolve through their pinned run: the builder reads the receiver's own wait-set rows solely to recover each sender's settled run identity, keeping the highest sequence per sender node, and falls back to the most-recent settled run in scope for subscribed senders that did not drive the round. Wait-set rows contribute no data — the row carries the sender run id and the attributes are then fetched from the ledger by that id. Delivered message payloads for the frame are merged in separately, and the empty wake message is skipped.
