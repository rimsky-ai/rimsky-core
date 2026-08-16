---
audit: cascade-graph
artifact: concept:cascade-graph
text: compliant
implementation: unsupported
commit: PENDING
audited: 2026-08-16T05:06:31Z
---

# The operator-dashboard read backplane: transaction discipline, read-only handlers, and the frame-to-message join

Unsupported. Two of the three invariants hold; the third does not hold across the population it claims. The backplane is two route groups mounted under the control API: an eighteen-route dashboard sub-router covering peers, templates, instances, frames, nodes, node-runs, claim-handles, events and system reads, plus two instance-scoped frame routes registered directly in the control-api router. Every handler that touches persisted tables opens a fresh transaction per read, the two composite reads (per-instance and per-node) take exactly one so the read cannot tear, and the peer handlers serve from the in-memory capabilities cache with no table transaction at all; a test drives twenty-six paths across all eighteen dashboard routes and asserts a full database snapshot is byte-identical before and after each, so read-only is proved rather than asserted. The per-instance read does compute the namesake cascade graph — nodes joined to their template subscription edges in both directions, each node's run summary, and its last terminal event. The frame-to-message join, however, is present on only two of the four frames-read routes in the family: the instance-scoped list and get return the message's type, sender and sender kind, while the dashboard's own frame list and frame get return the bare frame row carrying the triggering message's id and no joined message fields. The store layer offers both a joined and an unjoined read shape and the dashboard handlers call the unjoined pair, so the invariant's universal is false as written.
