---
audit: testing-scenario-based-e2e
artifact: decision:testing-scenario-based-e2e
determination: unsupported
commit: 3918d24e
audited: 2026-08-02T09:58:10Z
issue: 2026-08-02-095819-scenario-harness-waiters-lack-descriptive-reporting
---

# Testing discipline

Unsupported on one clause; the rest of the testing discipline holds throughout. The end-to-end-via-scenario-directories claim and the poll-until-success property both check out completely — none of the wait helpers checked carry a per-call deadline that fails a test on expiry. But the claim that these helpers descriptively report expected-versus-observed state on their exit path holds for only a minority of the population: of ten wait helpers found across the two harness packages, only three, all in one of the two packages, periodically log the awaited-versus-observed state while polling. The other seven, backing the larger of the two scenario directories, loop silently with no reporting at any point, despite one of them carrying a description-building method that reads as built for exactly this purpose and is never called from the wait loop.
