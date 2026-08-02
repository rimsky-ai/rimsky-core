---
audit: compose-engine-reuse
artifact: decision:compose-engine-reuse
determination: supported
commit: b767a27d
audited: 2026-08-02T09:40:22Z
---

# `compose run` drives the same query/plan/apply functions as `compose up`

Supported. `compose run` constructs a control-api HTTP client against its self-hosted stack's loopback endpoint and calls the identical `QueryState` / `ComputePlan` / `ApplyPlan` functions that `compose up` calls against a remote endpoint — the same one wiring path through validation, idempotency, and error mapping on the HTTP boundary, with no in-process bypass of that boundary anywhere in the one-shot verb's implementation.
