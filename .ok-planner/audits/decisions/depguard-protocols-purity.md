---
audit: depguard-protocols-purity
artifact: decision:depguard-protocols-purity
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:47:25Z
checked: 18
unaccounted: 0
---

# Whether the protocols module's dependency surface is the four named libraries with no rimsky-internal or test code

Supported. The module manifest's direct requirements are exactly the four the choice names — the UUID library, the official gRPC and protobuf libraries, and the YAML library — with everything else marked indirect, and all 18 non-generated packages in the module import nothing of the project's own outside the module itself. The lint rule matches the module and denies the foundation module, the graph, runtime, and control layers, the binary layer, and the container-based test-infrastructure library, so the "no rimsky-internal modules or layers and no test infrastructure" half is mechanically enforced and asserted by a fitness test. The four-library budget itself is carried by the manifest rather than by the lint: the rule is a denial list, so a fifth third-party dependency added to the module would draw no lint failure.
