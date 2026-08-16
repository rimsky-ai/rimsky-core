---
audit: depguard-graph-purity
artifact: decision:depguard-graph-purity
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:47:25Z
checked: 6
unaccounted: 0
---

# Whether the graph layer's lint rule is exemption-free and the tick loop sits above it

Supported, including the unconditional part, which is the claim worth testing. The graph rule's file list is a single unnegated glob over the layer — no negated paths, no per-file carve-outs — and it denies the runtime and control layers, the binary layer, and the two test-support stub trees; a fitness test asserts three of those denials remain. All 6 packages in the layer are clean: a search for imports of runtime, control, or the binary layer returned nothing. The structural claim that removes the need for an exemption holds as written — the periodic tick loop and its orchestration live in the runtime layer's scheduler package, which imports the graph layer's scheduler package and calls its three exported step functions downward, so the dependency runs in the permitted direction.
