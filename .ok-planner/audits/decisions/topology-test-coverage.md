---
audit: topology-test-coverage
artifact: decision:topology-test-coverage
determination: supported
commit: b767a27d
audited: 2026-08-02T09:36:46Z
---

# Both the all-in-one and three-container split topologies have standing integration proofs

Supported. The services integration harness carries a booted-topology proof for each of the 2 supported deployment shapes: `lib/services/test/harness.BringUpRimsky` boots the single-process `rimsky-all-in-one` image and backs the large majority of `lib/services/test/scenarios`, while `lib/services/test/harness.BringUpRimskySplit` boots three separate containers running `rimsky-scheduler`, `rimsky-supervisor`, and `rimsky-control-api` against shared storage. `lib/services/test/scenarios/split_topology_test.go`'s `TestSplitTopology_DriveNodeToTerminal` (tagged `@decision: topology-test-coverage`) deploys a template, creates an instance, and waits for a real node dispatch to reach a fresh/terminal state through the split topology end to end — a genuine cross-process proof, not a per-role unit test.
