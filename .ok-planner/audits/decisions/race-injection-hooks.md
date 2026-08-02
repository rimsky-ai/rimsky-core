---
audit: race-injection-hooks
artifact: decision:race-injection-hooks
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:36:46Z
---

# Deterministic injection hooks pin all four named defended seams

Supported. Checked all 4 seams the decision names against `lib/runtime`'s hook fields (`runner.go`'s `PostCommitHook`, `PreAcquireUnavailableHook`, `CheckAndFireHook`; `orphan_reaper.go`'s `PreReapHook`) and their test-side firings: the acquire-unavailable abandon path is pinned by `test/scenarios/acquire_unavailable_abandon_injection_test.go` via `PreAcquireUnavailableHook`; the folded ownership-bail path (see `decision:fold-ownership-bail`) by `test/scenarios/verify_before_run_post_commit_test.go` via `PostCommitHook`; the held-claim aggregate check-and-fire by `test/scenarios/held_claim_check_and_fire_race_test.go` via `CheckAndFireHook`; and the orphan-reaper vs. in-flight-terminal overlap by `test/scenarios/orphan_reaper_terminal_race_test.go` via `PreReapHook`. Each test asserts the hook actually fired (e.g. `require.True(t, hookFired, ...)`), so the interleaving is deterministically forced rather than left to detector luck, matching the decision's claim.
