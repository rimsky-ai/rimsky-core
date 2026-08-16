---
assessment: audit-artifact--compose-one-shot
subject: story:audit-artifact
way: compose-one-shot
release: d977250c
outcome: held
warrant: experiment:audit-artifact
---
# Inspecting the record a manifest-driven one-shot run left behind

`catalog:cli-verbs/rimsky compose run` drove a manifest with a mixed roster — one leg succeeding, one leg failing against a third-party executor — and finished in the invocation that started it, exiting non-zero and reporting each leg's outcome by name. It left a per-run artifact directory carrying the run's state, its blob store, and the configuration the run used. That the record can be inspected without re-running was established rather than assumed: the executor process the run had spawned was gone before anything was read, and the record was read by serving a copy of the artifact through an ordinary deployment. Both halves of the benefit came back — the failing instance, its worker node and its own error class replayed through `catalog:cli-verbs/rimsky instance get`, `catalog:cli-verbs/rimsky instance nodes` and `catalog:cli-verbs/rimsky instance events`, alongside the succeeding run's node and its attribute writeback. Two consecutive reads returned the same event count, so reading the record does not disturb it. Twenty-three checks ran across both one-shot modes and none failed.

## Unverified remainder

None: the passing run demonstrates the way as promised.
