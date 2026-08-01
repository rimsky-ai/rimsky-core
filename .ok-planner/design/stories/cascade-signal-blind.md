---
story: cascade-signal-blind
status: as-is
---

# Template author wires reactive nodes against any cascade-firing signal

## Story

As a template author wiring reactive nodes, I want every cascade-firing signal an upstream can emit to be observable through a single uniform subscription mechanism, so that I write "react to X" topologies without learning which signal types are first-class and which behave specially.

Every signal type the runtime publishes for cascade purposes — terminal outcomes, transient transitions, attribute changes — is observable through the same subscription surface. New cascade-firing signal types added to the canonical taxonomy become observable automatically without platform changes. For attribute-change signals specifically (`attribute/<key>/changed`), emission is diff-gated against the immediately-prior settled run of the same node in the same RunScope — i.e., within the current frame's cascade rounds — so that when a node self-cascades multiple times inside one frame, the receiver wakes only on rounds whose value for `<key>` actually differs. Same-value resettlements within a frame emit nothing for that key. First dispatch of a node in a fresh frame's RunScope has no prior to diff against — every populated key emits, uniformly.

Reactive topologies compose uniformly. Authors don't memorize which signal types are first-class and which are second-class — they write "react to X" once and the platform delivers. The "react to upstream error" topology composes the same way as "react to upstream success." The diff-gate on attribute changes lets a self-cascading node converge intra-frame: a subscriber to `attribute/<key>/changed` reacts only on cascade rounds inside the same frame that actually change `<key>`, not on same-value rounds. Cross-frame convergence is not part of this promise — frames are isolated (per `concept:frame`), so each new frame's diff-gate starts fresh regardless of what earlier frames settled.
