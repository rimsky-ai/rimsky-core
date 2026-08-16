---
audit: module-split
artifact: decision:module-split
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:47:25Z
checked: 4
unaccounted: 0
---

# Whether the workspace is the four named modules tied by local-path replaces

Supported. The workspace definition lists exactly four module directories — root, foundation, protocols, services — and each has its own manifest declaring the matching module path. Every intra-project requirement carries a local-path replace: three in the root manifest, one in the foundation manifest, three in the services manifest, and none needed in the protocols manifest, which requires nothing of the project at all and so is the zero-internal-dependency contract module the rationale describes. A fitness test walks all four manifests and fails both if the workspace stops using a module directory and if any manifest requires a workspace sibling without a local replace, which would make a build resolve it over the network.
