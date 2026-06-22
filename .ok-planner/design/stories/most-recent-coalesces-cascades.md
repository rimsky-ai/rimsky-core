---
story: most-recent-coalesces-cascades
status: as-is
---

# Most-recent mode coalesces cascade rounds during a single in-flight period

## Role

As a template author whose node has high-frequency upstreams and a long-running executor, I can know that under the default `cascade_mode=most-recent` configuration, M cascade rounds arriving during a single in-flight period produce ONE post-settle cascade dispatch with the latest view, not M dispatches. The redundant intermediate cascades are coalesced.

## Capability

When `cascade_mode=most-recent` (default), the gate evaluator deletes any prior cascade-driven stale-not-claimed run for the same (node, run-scope) at the moment a new cascade-driven pending transitions to stale. The replacement takes its sequence and its place in the dispatcher's claim queue. Non-cascade stales (operator-driven, policy retry, infra re-enqueue) are NOT deleted by this rule — they coexist alongside the cascade-stale and dispatch independently. Cascade-stale depth is bounded at ≤ 1 per (node, run-scope).

## Business value

Most workflows produce many low-value intermediate cascades for every meaningful update. Without coalescing, an executor that takes longer to run than its upstream's cadence falls infinitely behind: every cascade queues a new dispatch, the in-flight dispatch never catches up. With most-recent coalescing as the default, the executor catches up automatically to the latest state at each settle boundary, dropping intermediate views the downstream never observed. This matches the natural "I want the latest, not the history" semantic that most reactive systems converge on.

## Acceptance

An author writes a graph A → B with `cascade_mode=most-recent` on B. A is invalidated and re-runs multiple times (M > 1) while B is in-flight. Each A re-run triggers a cascade walk targeting B. The test asserts that at any moment during the cascade rounds, B has at most one cascade-driven pending and at most one cascade-driven stale-not-claimed. After B settles and A's last re-run completes, B dispatches exactly ONCE more, with its bag reflecting A's final post-rerun value (not any intermediate value). Observable as: B's lineage shows two runs (one for the original dispatch, one post-settle), and the second run's bag matches A's latest value.

## Falsifier

B dispatches M+1 times (one per cascade round) instead of twice — observable by counting B's executor invocations. OR B's second dispatch sees a bag from an intermediate A re-run, not the final — observable by inspecting B's second dispatch bag against A's value history.

## Proof

An executable scenario test where A re-runs 5 times in rapid succession while B is parked, B's deadline elapses and B settles, the test asserts B dispatches exactly once more after settle with A's 5th value (not the 1st through 4th).
