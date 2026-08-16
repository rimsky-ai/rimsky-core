---
audit: substitution-failure-routes-with-substitution
artifact: decision:substitution-failure-routes-with-substitution
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:33:29Z
---

# Both substitution rounds classify and apply policy inline, never deferring the failure

Supported. At gate-eval, a classifiable substitution failure is caught where the bag is built and routed by a handler that takes the same transaction: it appends the failure event, transitions the row, and calls the error-policy application inline, all on the sender's terminal-apply transaction, since the gate evaluator runs inside the drain that terminal-apply performs. The classifier maps a missing-source error to the template-resolution-failed class and a schema-validation error to the distinct template-validation-failed class, with the default falling to template-resolution-failed, matching the decision's two-way mapping; unit tests cover both directions. The claim-substitution round is the other site: a lock-name, selector, or fan-out partition substitution failure returns a sentinel carrying the site and directive, and the acquire error-policy handler opens a fresh transaction and applies the policy there, classified as template-resolution-failed, with four tests covering the sentinel sites and the terminal it produces. Nothing propagates the failure to a later phase: neither site returns the substitution error to its caller. Non-cascade rows are consistent with the decision's premise — they compute their bag at row creation by carry-forward or from the message payload, with no substitution round in that transaction.
