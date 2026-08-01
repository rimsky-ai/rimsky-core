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

