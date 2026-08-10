---
audit: permissive-peer-build
artifact: story:permissive-peer-build
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T05:45:00Z
---

# A peer builds, links and runs without touching copyleft code

Supported. A complete third-party executor was written as its own Go module
requiring only the protocols module, and it built for the host and cross-built
for the stack's platform. Its module graph names exactly one rimsky module — the
protocols one — and every rimsky package it links is under that module; all 105
Go files in that module declare Apache-2.0, while the root module it does not
depend on declares AGPL, so the boundary the story rests on is the one the build
respects. Run against a real stack, it exchanged the executor protocol's verbs in
both directions: the discovery probe's capability call returned the peer's own
declared error class, and two dispatches settled — one node successful with the
peer's attribute writeback recorded, one node failed carrying the peer's own
error class.
