---
audit: operator-onboarding
artifact: story:operator-onboarding
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:36:49Z
---

# First dev-loop walkthrough: one shipped template, one CLI verb, watch to completion

Supported. `README.md` section 6 ("First-steps walkthrough") documents exactly this path: bring up an all-in-one stack plus the bundled `verifier-shape-checks` executor, then `rimsky run examples/onboarding-template.yaml` — described as "the headline dev-loop verb: register + deploy + create in one shot against the shipped TemplateSpec" — followed by `rimsky watch <instance_id>` to reach a terminal state, with the shipped `examples/onboarding-demo.sh` wrapping both verbs. Both CLI verbs exist in code (`cmd/rimsky/cli/run.go`, `cmd/rimsky/cli/watch.go`). The load-bearing proof is `TestOnboardingDemo_RunSettlesIdle` (`lib/services/test/scenarios/onboarding_demo_e2e_test.go`), which boots a real all-in-one-style stack via testcontainers, runs `examples/onboarding-demo.sh` as a subprocess against a freshly built CLI binary, asserts exit 0 and a printed `instance_id=<uuid>`, and confirms the instance reaches idle and its `verifier` node actually dispatched — i.e. the shipped template runs real work (`no_nulls` + `pk_unique` over an inline dataset) to a terminal outcome without the operator writing a template. The story's "copy a shipped example workflow" matches the shipped, run-as-is `examples/onboarding-template.yaml` (no hand-authored template needed); the demo is run directly rather than filesystem-copied, which is the intended reading given the README frames the same walkthrough as running the shipped file in place.
