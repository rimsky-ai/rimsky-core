---
story: cascade-defers-during-flight
status: as-is
---

# In-flight node-runs are sealed against cascade-driven invalidation

## Role

As a template author whose executor relies on a fixed view of its inputs across the dispatch arc, I can know that once my node-run is dispatched, no upstream event can re-invalidate it, mutate its state, or rewrite its substituted attribute bag. Upstream cascades that arrive during my run produce a NEW node-run that the system queues and dispatches after the current one settles.

## Capability

A node-run in any in-flight state — `pending`, `stale`, `running`, `held`, `parked` — is sealed: the cascade walker never mutates it. When a cascade walk targets a receiver that already has an in-flight run, the walker creates a new cascade-driven node-run (subject to the per-template `cascade_mode`) and records the cascade against the new run's wait-set. The new run waits in line via the dispatcher's serialization gate (no claim while another run for the same (node, run-scope) is in running/held/parked).

## Business value

Without this guarantee, an executor cannot rely on the inputs it received at dispatch — they could be rewritten under it by an upstream re-run, breaking continuation primitives (parking, held-claim work, long-running async-callback flows) and any executor that reads its inputs more than once. With this guarantee, "this is a single dispatch" means what an executor author expects: one bag in, one outcome out, no retroactive mutation.

## Acceptance

An author writes a graph in one frame where B's executor is long-running (parked, held-claim, or mid-async-callback) and A re-runs intra-frame via the cascade self-edge pattern (per `concept:cascade`). While B is in-flight, A's self-edge fires and A re-runs to settle. B's executor is NOT re-invoked, B's bag is NOT rewritten, B's state row is NOT mutated. After B settles, a new B'_2 node-run dispatches with the cascade from A's re-run incorporated into its bag.

## Falsifier

B's executor is re-invoked while still in-flight, OR B's attribute bag is mutated by the cascade from A's re-run, OR B's state transitions out of running/held/parked without B's own executor terminating. Observable by counting B's executor invocations and inspecting B's persisted bag at terminal-handler times.

## Proof

The in-flight-seal mechanism is exercised by cascade-walker unit tests in `lib/runtime` and by conformance tests on the node-run state machine. A scenario-level proof driving A's intra-frame self-cascade with B parked is deferred — the mechanism's in-flight-seal guarantee is verified at the unit level; the scenario proof will land in the intra-frame proof-cluster follow-up.
