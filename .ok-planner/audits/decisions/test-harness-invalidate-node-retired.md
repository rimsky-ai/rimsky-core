---
audit: test-harness-invalidate-node-retired
artifact: decision:test-harness-invalidate-node-retired
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:49:58Z
---

# The scenario harness carries no invalidate-node helper, and second firings run through real triggers

Supported. Searching both test harnesses the project has — the root test-support scenario harness and the services module's integration harness — turns up zero invalidate-node helpers and zero other test-only node-invalidation entry points; the only invalidation surfaces in the tree are production ones (the typed-message path and the paused-instance debug override), neither of which is test-only. The harness's message-post helpers carry the decision's annotation and are the replacement path, and the scenario suites that re-fire a node do so by posting a typed message through the control API. The direct-SQL fixtures that remain in the scenario suites build pre-existing race states — a stolen claim, an orphaned dispatch, retention-sweep rows — rather than invalidating a node for a second firing, so they are not the surface the decision rules out.
