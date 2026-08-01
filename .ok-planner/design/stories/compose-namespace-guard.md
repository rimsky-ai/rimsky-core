---
story: compose-namespace-guard
status: as-is
---

# Server enforces reserved compose prefix

## Role

As an operator running rimsky behind multiple tools, I can trust that the compose namespace prefix on tag and instance-key namespace is reserved for the compose machinery alone — any other client attempting to create a compose-prefixed resource is refused at the server regardless of which client surface it comes from — so that compose-managed namespace stays disjoint from manually-authored artifacts.

## Capability

Server-side reservation of the compose namespace prefix: only the compose machinery (holding the appropriate capability) may create compose-prefixed tags or instance keys; all other clients are refused at the server.

## Business value

Compose-managed namespace stays disjoint from manually-authored artifacts; the guard is enforced once at the server rather than relied on at every client.

