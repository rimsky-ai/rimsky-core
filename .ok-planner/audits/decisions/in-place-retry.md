---
audit: in-place-retry
artifact: decision:in-place-retry
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:16:46Z
---

# Both retry surfaces loop in place on the same node-run row under one shared error-policy mechanism

Supported on every clause. Both failure surfaces loop on the same dispatch: the executor-side runner re-enters its dispatch call with the same acquisition and the same run identifier after sleeping the policy delay, and the acquire-side helper loops on the same queue candidate the same way, with all five acquire failure classes routed through one shared policy-application function that the executor side also calls. The retry branch of that function emits its signal, stamps the run as its own prior dispatch — literally pointing the row at itself, which is where no-new-row is visible — records the decision, and returns before any state update or lock release, so the row keeps its pre-error state and its held claims across iterations; the give-up and pass branches are the ones that transition to failed or fresh. The counter is a single integer column on the node-run row read and written through the evaluator state, and the policy evaluator increments it on any retry and compares it against one node-level budget with one backoff config, with no per-class counter and no reset when the class changes; a later run row for the same node starts the column at zero. Coverage spans both surfaces and the in-place property itself: a cap scenario proves give-up fires only after exactly the budgeted retries and records the error settling signal, an acquire scenario proves the node stays out of its settled state through silent acquire retries and then reaches it once the upstream resource appears, and the work-pairing scenario proves the retried run emits exactly one started and one completed event under one dispatch identifier, which a fresh-row retry could not produce.
