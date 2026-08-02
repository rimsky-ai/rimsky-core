---
story: compose-namespace-guard
---

# Server enforces reserved compose prefix

## Story

As an operator running rimsky behind multiple tools, I can trust that the compose namespace prefix on tag and instance-key namespace is reserved for the compose machinery alone — any other client attempting to create a compose-prefixed resource is refused at the server regardless of which client surface it comes from — so that compose-managed namespace stays disjoint from manually-authored artifacts.
