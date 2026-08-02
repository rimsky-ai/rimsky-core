---
audit: forensic-last-attribute
artifact: story:forensic-last-attribute
determination: supported
commit: b767a27d
audited: 2026-08-02T09:33:46Z
---

# Operator can read a node's latest resolved attribute bag from the control API directly

Supported. `GET /nodes/{id}` (`lib/control/controlapi/nodes.go::handleGetNode`) loads the node's latest run via `GetLatestRunForNode` and its resolved bag via `NodeAttributes().GetLatestByNode`, returning it as `latest_attributes` in the JSON response — a direct read, no event-log reconstruction required. This is exercised end-to-end (not just unit-level) by at least one services-harness scenario (`lib/services/test/scenarios/bundled_inproc_dispatch_test.go`), which polls the node via the control API and asserts a specific delta value is present under `latest_attributes` in the HTTP response, and the field is consumed by several further e2e scenarios (`portable_template_across_modes_e2e_test.go`, `claude_agent_cross_stack_e2e_test.go`, `claude_agent_per_node_divergence_e2e_test.go`, `single_process_allinone_test.go`, `verifier_severity_partition_e2e_test.go`, and `test/scenarios/breakpoints/debugger_lifecycle_e2e_test.go`).
