---
audit: lifecycle-subscriber-author
artifact: story:lifecycle-subscriber-author
text: noncompliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:55:31Z
checked: 7
unaccounted: 0
---

# All seven lifecycle callbacks fire at their transition, carrying the context the story names

Supported, with the population enumerated: all seven callbacks the story names
fired, and none is unaccounted for. Measured with a third-party subscriber built
for the run — its own Go module depending only on the published protocols
module — registered as an ordinary peer of a released-image stack and driven
through one instance's whole life. Thirty-seven checks, none failing. Nothing
fired before anything happened. Six of the seven were already delivered by the
time the control-API call that caused them returned, checked without waiting,
which is what synchronous means from the caller's side; the seventh, run-scope
terminal, has no caller call to be synchronous with and arrived from the runtime
when the frame settled. Every context element the story names came through:
template hash on all four template callbacks and the spec on registration, the
deployment's tags on deploy, and on instance creation the instance id, template
hash, instance key, params, caller-supplied service bindings, owner key and
routing identity; the run-scope callback carried its scope id, instance and
terminal reason, and the termination callback its instance, template and
termination time. The seven fired in the order the transitions happened.

## Compliance

- The body prescribes the protocol and enumerates its surface — "implement the lifecycle-subscriber protocol — seven callbacks covering …" names a protocol and lists its callbacks, which decisions own; the compliant capability is that the author's own service is told when rimsky's templates, instances and run scopes change state.
- The body enumerates the payload's fields — the parenthesised context list is a data shape, which decisions own; the compliant clause is that each notification carries enough context to act on the transition.
