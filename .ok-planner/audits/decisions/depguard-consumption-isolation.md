---
audit: depguard-consumption-isolation
artifact: decision:depguard-consumption-isolation
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:47:25Z
checked: 36
unaccounted: 0
---

# Whether shipped bundled-service packages import the protocols module only, guarded by lint

Supported. The dependency lint carries a consumption-side rule whose file globs match the services module in both the workspace-rooted and module-rooted forms while negating the module's test tree, and it denies the foundation module, the graph, runtime, and control layers, and the binary layer — the five edges the choice names. Reality matches: scanning all 36 shipped service packages under the claim-producer, sensor, subscriber, executor, and shared-internal trees for imports of the project's own module paths returned only protocols packages and sibling services packages, with no rimsky-internal import anywhere. The rationale's claim about the module graph also holds — the services module's manifest requires both the root module and the foundation module, so the forbidden edge is representable and the lint is genuinely the only mechanical guard. A fitness test in the pin-test package asserts the rule exists and still denies all five.
