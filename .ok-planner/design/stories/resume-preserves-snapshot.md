---
story: resume-preserves-snapshot
status: as-is
---

# Parked node-run resumes against its dispatch-time substitution snapshot

## Role

As a template author who relies on a parked node continuing from where it left off, I can know that when the parked node resumes (deadline elapses, timer fires) its executor is re-invoked with the same substituted upstream values it saw when it parked, even if upstream nodes have re-run during the park.

## Capability

A node-run's substituted attribute bag — the values produced by the substitution context built at the moment the run's gates cleared — is persisted on the run's attribute store (per `concept:attribute`) keyed to the run row, and is the only attribute state the executor ever sees for that node-run, across park and resume. The deadline-driven resume path transitions the parked node-run directly to `stale` and the dispatcher loads the persisted bag by run-id; there is no separate distinct-resuming-state and no rebuild of substitution. Every dispatch in the seven-state model loads the persisted bag from its own row — resume is not a special case.

## Business value

A parked node-run is a mid-resolve state, not a fresh dispatch. The author of the parking executor expects continuity: the inputs the executor saw on first dispatch are the inputs it sees on resume. Without this guarantee, an upstream that re-ran during the park would silently rewrite the parked node's inputs, breaking the "the value I read at dispatch must be stable for the lifetime of my node-run" expectation that lets parking work as a continuation primitive rather than a re-evaluation. Under the seven-state model this guarantee generalizes: every in-flight state (pending, stale, running, held, parked) is sealed against cascade-driven mutation, and resume is simply "re-dispatch the same row with its persisted bag."

## Acceptance

An author writes a template where receiver A substitutes attribute X from upstream U. A is scripted to park on its first dispatch with a resume-at deadline. While A is parked, U re-runs in the same frame (driven by some other in-frame cascade) and produces a new value for X. When A's deadline elapses and the parked sweep transitions A from parked to stale, the dispatcher claims A and re-invokes A's executor with X equal to the value it saw at original dispatch, NOT the post-rerun value. Observable by comparing A's two executor invocations against U's attribute ledger at each point in time.

## Falsifier

A's resumed executor invocation receives X equal to U's post-rerun value (matching a substitution context rebuilt from current upstream attributes at resume time, not the dispatch-time snapshot) — observable by inspecting A's second executor input against U's attribute-ledger entries.

## Proof

An executable scenario test where receiver A parks holding X-as-seen, U re-runs in the same frame and writes a new X, the parked sweep wakes A, and the test asserts A's second executor invocation receives the original X (not the new one). Backed by a unit test on the state machine that confirms `parked + deadline_resume → stale` (the deadline-wake case) and that the dispatcher loads the persisted bag by run-id, returning the bag persisted at the original dispatch.
