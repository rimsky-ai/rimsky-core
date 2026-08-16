---
audit: permissive-peer-build
artifact: story:permissive-peer-build
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:25:00Z
---

# A third-party peer builds against the permissive module alone and exchanges verbs with a real stack

Supported. A complete third-party service — its own module, requiring exactly
one rimsky module — was built for the host and cross-built for the stack's
platform, and its dependency graph was inspected: the one rimsky module it names
is the protocols module, and every rimsky package it links is under that module.
The licence boundary the story rests on was counted rather than assumed: all 105
Go files in the protocols module declare the permissive licence, and the root
module the peer does not depend on declares the copyleft one. Against a running
stack that declared the peer as an ordinary gRPC executor, the protocol's verbs
were exchanged in both directions: the discovery probe's capabilities call
returned the peer's own declared error class, and two dispatches settled — one
node fresh carrying the peer's success delta on the record, one node failed
carrying the peer's own error class — with the peer's container log showing both
executions.
