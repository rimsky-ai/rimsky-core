---
assessment: permissive-peer-build--build-against-protocols-only
subject: story:permissive-peer-build
way: build-against-protocols-only
release: d977250c
outcome: held
warrant: experiment:permissive-peer-build
---
# Building a complete peer whose only rimsky dependency is the permissive protocols module

The audit built a complete third-party service — its own module, requiring exactly one rimsky module — for the host and cross-built it for a deployment's platform, then inspected its dependency graph. The one rimsky module it names is the protocols module, and every rimsky package it links sits under that module, so nothing from the rest of the product is pulled in by building a peer. The licence boundary the story rests on was counted rather than assumed: all 105 source files in the protocols module declare the permissive licence, while the module the peer does not depend on declares the copyleft one. A service author integrating with rimsky therefore does not place their own service under copyleft.

## Unverified remainder

The count covers the protocols module at this release. The way establishes the boundary for a peer implementing the executor protocol; it does not separately re-count the boundary for every protocol a peer could implement.
