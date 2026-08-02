---
decision: uniform-attributes-delta
---

# attributes_delta is a feature of run-terminating verdicts only

## Choice

Both run-terminating executor verdicts — `terminal/success` and `terminal/error/<class>` — carry `attributes_delta` on the wire. The runtime merges the delta into the per-run attribute row atomically with the verdict commit, and embeds the delta on the emitted signal's payload so subscribers' CEL `when:` predicates can match against `payload.attributes_delta.<key>`. The persistence path and the signal-payload path are exercised uniformly across success and error; an executor expresses "fire when the producer's verdict carries this attribute value" once, and the predicate fires on either kind. This is separate from, and coexists with, the mid-dispatch attribute writeback callback (see `decision:writeback-bumps-progress`): that channel merges attributes into the per-run attribute row incrementally during a dispatch, but does not emit a signal or cascade-fire, so `attributes_delta` on the run-terminating verdict remains the only channel a subscriber's CEL predicate can match against.

Park (`transient/park`) does not carry `attributes_delta`. Park ends a dispatch but not the run: no cascade-fire happens at park, no CEL predicate ever evaluates against a park's payload, and the executor that resumes after the wake is the next opportunity to write attributes via its own run-terminating verdict. Letting park write to the per-run attribute row would smuggle persistent state mutation through a dispatch-internal transition.

## Rationale

Attribute writeback is a feature of run termination. A run-terminating verdict is the executor saying *"this is the final state of this work; commit it,"* and `attributes_delta` is the payload that commit moves into the per-run attribute row. Run-terminating signals also cascade-fire, which is what gives subscribers a place to predicate on the delta. Both effects — persistence and exposure — are functions of the verdict being run-terminating; they belong on the same set of signals because they describe the same architectural moment.

Park is the executor saying *"I'm not done; wake me later."* The dispatch ends without an authoritative result. Persistent state mutation in that moment is incoherent: the executor hasn't completed the work whose result the attribute change would describe. Executors that need to thread executor-managed state across the park-and-resume boundary use scratch — opaque bytes the supervisor copies forward onto the resume dispatch's row — which is the channel intended for that purpose.

## Alternatives

A. Keep `attributes_delta` on park for persistence-only uniformity (signal-payload exposure excluded). Rejected — "uniformity" presupposes that park belongs in the same set as success and error. Park is dispatch-internal; success and error are run-terminating. There is no set of three to be uniform across, and forcing one would create a path for persistent state mutation outside of run termination.

B. Drop `attributes_delta` from the run-terminating verdict entirely and rely solely on the mid-dispatch writeback callback for attribute mutation. Rejected — the verdict carries the result, and coupling the final attribute state to the verdict commit is what lets a signal-payload predicate match against it; the mid-dispatch callback has no such exposure and cannot substitute for it.
