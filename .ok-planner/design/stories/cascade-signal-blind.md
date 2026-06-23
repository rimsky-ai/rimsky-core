---
story: cascade-signal-blind
status: as-is
---

# Template author wires reactive nodes against any cascade-firing signal

## Role

As a template author wiring reactive nodes, I want every cascade-firing signal an upstream can emit to be observable through a single uniform subscription mechanism, so that I write "react to X" topologies without learning which signal types are first-class and which behave specially.

## Capability

Every signal type the runtime publishes for cascade purposes — terminal outcomes, transient transitions, attribute changes — is observable through the same subscription surface. New cascade-firing signal types added to the canonical taxonomy become observable automatically without platform changes. For attribute-change signals specifically (`attribute/<key>/changed`), emission is diff-gated against the prior run: a subscriber is woken only when the key's value actually differs from the prior run's persisted value; same-value resettlements emit nothing for that key and don't wake subscribers.

## Business value

Reactive topologies compose uniformly. Authors don't memorize which signal types are first-class and which are second-class — they write "react to X" once and the platform delivers. The "react to upstream error" topology composes the same way as "react to upstream success." The diff-gate on attribute changes means a subscriber to `attribute/<key>/changed` reacts to real value changes, not to every upstream resettlement.

## Acceptance

For every cascade-firing signal type in the canonical taxonomy (terminal kinds, transient kinds, attribute changes), a subscriber whose subscription type-path matches the emitted signal receives a wake, and the signal's audit row lands in the event log. Per-sender (`{ node: X, type: ... }`) and cross-cutting (`instance: true`) subscription shapes both fire. Exact-type and trailing-`*` prefix shapes both fire. Tag-based filters on terminal signals (CEL `when:` filter over `payload.tags`) fire when the sender's settling outcome carries the tag and don't fire when it doesn't. For `attribute/<key>/changed` specifically: a subscriber wakes on the upstream's first settlement that writes `<key>` (no prior value to diff against), and wakes again only when the upstream re-settles with a different `<key>` value. Two consecutive upstream settlements emitting the same `<key>` value produce one wake, not two.

## Falsifier

Any cascade-firing signal type produces no subscriber dispatch when its subscription matches the type-path, OR the audit row for that emit is missing, OR a tag-based filter doesn't behave as described, OR a subscriber to `attribute/<key>/changed` wakes a second time on a same-value resettlement of `<key>`.

## Proof

Executable proof — scenario test that iterates the cascade-firing signal types and asserts, for each, that a per-sender subscription dispatches its subscriber when the upstream emits the signal; that a cross-cutting (`instance: true`) subscription on the same type-path also dispatches; that trailing-`*` prefix subscriptions match every leaf with that prefix; and that the audit row for the signal lands in the event log. One iteration covers tag-filtered `terminal/*` (a CEL `when:` filter fires only when the named tag is present in the sender's settling tags). One iteration covers `attribute/<key>/changed`'s diff-gate (the sender settles twice with the same value for the key; the receiver wakes exactly once). The non-cascading signal types — `terminal/park/<reason>` and `terminal/infra/<class>` — are explicitly out of the proof's scope: those emit a bare audit row without cascading because the node resumes rather than settling.
