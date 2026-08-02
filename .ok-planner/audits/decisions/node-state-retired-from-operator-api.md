---
audit: node-state-retired-from-operator-api
artifact: decision:node-state-retired-from-operator-api
determination: unsupported
commit: 3918d24e
audited: 2026-08-02T09:58:10Z
issue: 2026-08-02-095820-no-per-run-lifecycle-state-endpoint
---

# Operator surfaces do not expose a synthesized node-level state field

Unsupported for the decision's second named surface. The first surface — no synthesized node-level state field, replaced by a categorical per-state run summary — is real and confirmed by inspection. But the second surface the decision names, a per-run endpoint for a specific run's lifecycle state, does not exist: checked all route-registration groups wired into the control-API router, and none exposes a run-scoped read of node-run lifecycle state. The one run-keyed read endpoint that does exist is a different projection entirely — a lineage/provenance record populated only at leaf-run terminals, reporting a lineage outcome rather than a lifecycle state — so an operator with a specific in-flight or non-terminal run in mind has no endpoint that answers what state it is in.
