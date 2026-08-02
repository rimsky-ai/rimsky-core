---
audit: runtime-diagnostics
artifact: story:runtime-diagnostics
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:35:34Z
---

# Operator inspects runtime wedge state

Supported. All 4 surfaces the story names exist as gated read endpoints and
each has dedicated tests: parked nodes
(`GET /admin/diagnostics/parked-nodes`, tested by
`TestAdminParkedNodes_ReturnsEntries`), pending wake dependencies
(`GET /admin/diagnostics/wait-sets`, tested by 6 cases in
`admin_waitset_test.go` covering both frame-scoped and receiver-scoped
listing), frames a holding sub-graph is gripping
(`GET /admin/diagnostics/held-frames`, grouping parked nodes by frame,
tested by `TestAdminHeldFrames_GroupsByFrame`), and current holders of a
claim (`GET /claim-handles/{claim_handle_id}/holders`, tested by
`TestClaimHoldersRoute` and `TestClaimHoldersRoute_EmptyList`). All four
read directly from persistence rather than requiring database access,
matching the "without ad-hoc database spelunking" framing.
