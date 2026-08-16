---
assessment: uncovered-substitution-rejected--registration-refusal
subject: story:uncovered-substitution-rejected
way: registration-refusal
release: d977250c
outcome: held
warrant: experiment:uncovered-substitution-rejected
---
# Registration refuses a read the template never subscribes to, and shows the fix

The audit submitted two templates that each read something they never subscribe to — one an upstream node's attribute, one a typed message's field — to `catalog:http-routes/POST /v1/templates`. Both registrations were refused and no template id came back. Each refusal carried a structured finding naming the offending reference, the receiving node, the place in the template the reference sits, and the exact subscription entry that would cover it. The remedy was proved rather than described: adding precisely the shown entry to the same template made it register. The author therefore meets the wiring mistake at registration with the fix in hand, instead of meeting an orphan read as a deferred runtime failure.

## Unverified remainder

Two uncovered-reference shapes were exercised. The demonstration does not establish what a template carrying several uncovered references at once reports.
