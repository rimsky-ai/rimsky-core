---
story: cascade-signal-blind
status: as-is
---

# Template author wires reactive nodes against any cascade-firing signal

## Role

As a template author wiring reactive nodes, I want every cascade-firing signal an upstream can emit to be observable through a single uniform subscription mechanism, so that I write "react to X" topologies without learning which signal types are first-class and which behave specially.

Every signal type the runtime publishes for cascade purposes — terminal outcomes, transient transitions, attribute changes — is observable through the same subscription surface. New cascade-firing signal types added to the canonical taxonomy become observable automatically without platform changes. For attribute-change signals specifically (`attribute/<key>/changed`), emission is diff-gated against the immediately-prior settled run of the same node in the same RunScope — i.e., within the current frame's cascade rounds — so that when a node self-cascades multiple times inside one frame, the receiver wakes only on rounds whose value for `<key>` actually differs. Same-value resettlements within a frame emit nothing for that key. First dispatch of a node in a fresh frame's RunScope has no prior to diff against — every populated key emits, uniformly.

Reactive topologies compose uniformly. Authors don't memorize which signal types are first-class and which are second-class — they write "react to X" once and the platform delivers. The "react to upstream error" topology composes the same way as "react to upstream success." The diff-gate on attribute changes lets a self-cascading node converge intra-frame: a subscriber to `attribute/<key>/changed` reacts only on cascade rounds inside the same frame that actually change `<key>`, not on same-value rounds. Cross-frame convergence is not part of this promise — frames are isolated (per `concept:frame`), so each new frame's diff-gate starts fresh regardless of what earlier frames settled.

## Acceptance

For every cascade-firing signal type in the canonical taxonomy (terminal kinds, transient kinds, attribute changes), a subscriber whose subscription type-path matches the emitted signal receives a wake, and the signal's audit row lands in the event log. Exact-type and trailing-`*` prefix shapes both fire. Tag-based filters on terminal signals (CEL `when:` filter over `payload.tags`) fire when the sender's settling outcome carries the tag and don't fire when it doesn't. For `attribute/<key>/changed` specifically: within a single frame, a self-cascading sender's first settlement writing `<key>` wakes the subscriber (no in-scope prior); subsequent same-frame rounds that re-settle `<key>` to the same value do not wake; the next round that changes `<key>` wakes. The suppression rule applies only to same-frame, same-RunScope prior runs — across frames, every new frame's diff-gate has no in-scope prior to compare against and fires unconditionally on the settling bag's populated keys.

## Falsifier

Any cascade-firing signal type produces no subscriber dispatch when its subscription matches the type-path, OR the audit row for that emit is missing, OR a tag-based filter doesn't behave as described, OR a subscriber to `attribute/<key>/changed` wakes a second time on a same-value cascade round within the same frame, OR the diff-gate suppresses emission across a frame boundary (a frame-isolation violation — see `concept:frame`).

## Proof

Executable proof — scenario test that iterates the cascade-firing signal types and asserts, for each, that a per-sender subscription dispatches its subscriber when the upstream emits the signal; that trailing-`*` prefix subscriptions match every leaf with that prefix; and that the audit row for the signal lands in the event log. One iteration covers tag-filtered `terminal/*` (a CEL `when:` filter fires only when the named tag is present in the sender's settling tags). One iteration covers the intra-frame `attribute/<key>/changed` diff-gate: the sender self-cascades within one frame, settling twice with the same value for the key; the receiver wakes exactly once (the first round). The audit-only `transient/park/*` family is explicitly out of the proof's scope: those emit a bare audit row without cascading because the run continues rather than terminating, and operators reacting to park subscribe to the eventual run-terminating `terminal/*` settlement that follows the wake.
