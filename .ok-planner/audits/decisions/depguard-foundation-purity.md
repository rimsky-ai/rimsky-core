---
audit: depguard-foundation-purity
artifact: decision:depguard-foundation-purity
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:47:25Z
checked: 23
unaccounted: 0
---

# Whether the foundation module stays below the graph, runtime, and control layers

Supported. The dependency lint carries a foundation rule matching every file in the module and denying the graph, runtime, and control layers, the binary layer, and two test-support stub trees; a fitness test fails if any of the four layer denials disappears. Reality matches across all 23 foundation packages: a search for imports of the three higher layers or the binary layer returned nothing, and the module's own manifest does not even require the root module, so an upward import would not resolve outside the workspace either. The positive half of the sentence also holds today — the manifest's direct requirements are the protocols module plus ten third-party libraries — though that budget is held by the manifest rather than by the lint, whose rule is a denial list.
