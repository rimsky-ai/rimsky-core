---
audit: depguard-runtime-purity
artifact: decision:depguard-runtime-purity
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:47:25Z
checked: 14
unaccounted: 0
---

# Whether the runtime layer stays below the control layer while consuming the layers beneath it

Supported. The dependency lint carries a runtime rule matching every file in the layer and denying the control layer, the binary layer, and the two test-support stub trees, with a fitness test failing if the control or binary denial disappears. Reality matches across all 14 runtime packages: a search for imports of the control layer, the binary layer, or the stub trees returned nothing. The positive half holds too — 243 files in the layer import the foundation module, the graph layer, or the protocols module, so the runtime does consume exactly the three tiers below it and nothing above.
