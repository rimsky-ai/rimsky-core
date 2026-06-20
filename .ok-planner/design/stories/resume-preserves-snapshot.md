---
story: resume-preserves-snapshot
status: as-is
---

# Parked node-run resumes against its dispatch-time substitution snapshot

## Role

As a template author who relies on a parked node continuing from where it left off, I can know that when the parked node resumes (deadline elapses, timer fires) its executor is re-invoked with the same substituted upstream values it saw when it parked, even if upstream nodes have re-run during the park.

## Capability

A node-run's substituted attribute bag — the values produced by the substitution context built at dispatch from the node's subscribed upstreams — is persisted at dispatch time and is the only attribute state the executor ever sees for that node-run, across park and resume. The deadline-driven resume path does not rebuild the substitution context from current upstream attributes; it loads the persisted bag and dispatches.

## Business value

A parked node-run is a mid-resolve state, not a fresh dispatch. The author of the parking executor expects continuity: the inputs the executor saw on first dispatch are the inputs it sees on resume. Without this guarantee, an upstream that re-ran during the park would silently rewrite the parked node's inputs, breaking the "the value I read at dispatch must be stable for the lifetime of my node-run" expectation that lets parking work as a continuation primitive rather than a re-evaluation.

## Acceptance

An author writes a template where receiver A substitutes from upstream U (`{{nodes.U.attribute.X}}`). A is scripted to park on its first dispatch with a resume-at deadline. While A is parked, U re-runs in the same frame (driven by some other in-frame cascade) and produces a new value for X. When A's deadline elapses and the parked sweep resumes A, A's executor is invoked with X equal to the value it saw at original dispatch, NOT the post-rerun value. Observable by comparing A's two executor invocations against U's attribute ledger at each point in time.

## Falsifier

A's resumed executor invocation receives X equal to U's post-rerun value (matching the substitution context built from current upstream attributes at resume time, not the dispatch-time snapshot) — observable by inspecting A's second executor input against U's attribute-ledger entries.

## Proof

An executable scenario test where receiver A parks holding X-as-seen, U re-runs in the same frame and writes a new X, the parked sweep resumes A, and the test asserts A's second executor invocation receives the original X (not the new one). Backed by a unit test on the state machine that confirms `parked + deadline_resume → resuming` (distinct from `parked + handler_resume → stale`, which remains the cascade-driven path) and `resuming + dispatch_claimed → running`.
