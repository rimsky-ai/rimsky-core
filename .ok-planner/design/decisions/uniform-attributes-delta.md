---
decision: uniform-attributes-delta
status: as-is
aliases: []
---

# attributes_delta is a feature of run-terminating verdicts only

## Choice

Both run-terminating executor verdicts — `terminal/success` and `terminal/error/<class>` — carry `attributes_delta` on the wire. The runtime merges the delta into the per-run attribute row atomically with the verdict commit, and embeds the delta on the emitted signal's payload so subscribers' CEL `when:` predicates can match against `payload.attributes_delta.<key>`. The persistence path and the signal-payload path are exercised uniformly across success and error; an executor expresses "fire when the producer's verdict carries this attribute value" once, and the predicate fires on either kind. The mid-dispatch attribute writeback callback that an earlier asymmetric design forced is retired; the run-terminating verdict is the only channel an executor uses to mutate node attributes.

Park (`transient/park`) does not carry `attributes_delta`. Park ends a dispatch but not the run: no cascade-fire happens at park, no CEL predicate ever evaluates against a park's payload, and the executor that resumes after the wake is the next opportunity to write attributes via its own run-terminating verdict. Letting park write to the per-run attribute row would smuggle persistent state mutation through a dispatch-internal transition, which is the same category error that the audit-only-but-payload-carrying signal exposure was — one layer deeper.

## Rationale

Attribute writeback is a feature of run termination. A run-terminating verdict is the executor saying *"this is the final state of this work; commit it,"* and `attributes_delta` is the payload that commit moves into the per-run attribute row. Run-terminating signals also cascade-fire, which is what gives subscribers a place to predicate on the delta. Both effects — persistence and exposure — are functions of the verdict being run-terminating; they belong on the same set of signals because they describe the same architectural moment.

Park is the executor saying *"I'm not done; wake me later."* The dispatch ends without an authoritative result. Persistent state mutation in that moment is incoherent: the executor hasn't completed the work whose result the attribute change would describe. Executors that need to thread executor-managed state across the park-and-resume boundary use scratch — opaque bytes the supervisor copies forward onto the resume dispatch's row — which is the channel intended for that purpose. Attribute mutation on park would also re-open the inconsistency the retired mid-dispatch writeback created: a path for mutating per-run attributes outside the run-terminating verdict.

## Alternatives

A. Keep `attributes_delta` on park for persistence-only uniformity (signal-payload exposure excluded). Rejected — the framing of "uniformity" presupposed that park belonged in the same set as success and error. Park is dispatch-internal; success and error are run-terminating. There is no set of three to be uniform across, and forcing one created a path for persistent state mutation outside of run termination.

B. Drop `attributes_delta` everywhere and require executors to write attributes via a separate channel (mid-dispatch writeback callback or attribute-set RPC). Rejected — that is the asymmetric design the current shape replaced. The verdict carries the result; coupling attribute writeback to the verdict commit removes a class of mid-dispatch concurrency problems and keeps the executor's mutation surface to one channel.
