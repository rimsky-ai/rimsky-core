---
story: claim-handoff-across-frames
status: retired
---

# Template author wires a claim handoff that survives across frames

This story's cross-frame coupling shape (`frame: next` + `instance: true → frame: next`) retires with the `frame:` modifier. Cross-frame coupling under the message-schema-layer redesign is expressed through message-sender nodes (`concept:message-sender-node`) whose dispatch lands a message that opens the next frame. The original claim-lifetime-across-frames concern survives as the surviving `concept:claim-handle` invariant — a held claim's lifetime is governed by the holding subgraph, not by the frame. → `story:cross-frame-coupling`, `concept:message-sender-node`, `concept:claim-handle`.
