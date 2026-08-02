---
audit: cascade-defers-during-flight
artifact: story:cascade-defers-during-flight
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:29Z
---

# In-flight node-runs are sealed against cascade-driven invalidation

Supported. `concept:cascade`'s invariant states an in-flight run's bag and identity are never rewritten by a cascade walk; instead the walker either accumulates into a new cascade-driven pending row or, for the one carve-out, wakes a `parked` run through the single parked-wake path. `test/scenarios/cascade_defers_during_flight_test.go::TestCascadeDefersDuringFlight_WalkerQueuesNewPendingWithoutMutatingInFlight` checks this directly against a live run: while node `b` sits `parked`, its upstream `a` self-cascades twice more, and the test asserts `b` is invoked exactly once (its original park dispatch) while parked — no rewrite — with at least 2 new cascade-driven `pending`/`stale` rows queued behind it, then, after the park resume-at fires the single wake path (`parked_resume_started`), asserts `b` runs exactly 5 times in total across park, deadline-resume, and 3 queued rounds. This is the only test carrying the story's citation, and it exercises both halves of the claim (in-flight sealing, and the parked-wake carve-out) on one topology.
