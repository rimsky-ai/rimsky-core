---
tension: observability-split-cascade-graph-and-discovery-cache
category: overloaded
status: resolved
spec: 2026-05-11-design-log-convergence
affects:
  - observability
  - cascade-graph
  - discovery-cache
  - executor
  - claim-producer
resolution:
  shape: split-into-three
  new-concepts:
    - concepts/cascade-graph.md (operator-dashboard backplane)
    - concepts/discovery-cache.md (per-peer Capabilities cache)
  slimmed: concepts/observability.md (now covers only peer protocols + handshake + userdata_schema)
  summary: |
    Split observability into three sharper concepts. cascade-graph
    owns the operator-dashboard HTTP routes; discovery-cache owns
    the in-memory per-peer Capabilities cache; observability owns
    the peer-facing optional protocols and the handshake that
    populates discovery-cache, plus userdata_schema validation.
---

# `observability` bundles four surfaces; promote `cascade-graph` and `discovery-cache` to their own concepts

## What is muddy

`concepts/observability.md` covers four structurally distinct surfaces under one slug:

1. **Optional peer observability protocols** (`ExecutorObservability`, `StoreObservability`) — the per-peer gRPC surface for `Capabilities` / `GetTrace` / `StreamTrace`. Peer-facing.
2. **Cascade-graph HTTP routes** mounted on control-api (`/observability/*`, `/events`, `/frames`, `/nodes/{instance}/{type}`, `/dispatches`, ...) — the operator-dashboard backplane. Surfaces rimsky's own runtime state. Operator-facing.
3. **Handshake + Discovery cache** (`modeling/observability/handshake.go`) — the startup-time parallel probe of each peer plus the in-memory `Discovery` cache it populates. The cache is then consulted at template registration for the `on_event` declared-events cross-check.
4. **`userdata_schema` validation site** — read from the observability handshake, applied at template registration and at dispatch post-merge/post-substitution.

The four surfaces are agglomerated under one noun but they answer different questions: who observes whom (peer protocols), who-reads-rimsky-state (cascade-graph), what-rimsky-cached-from-peers (discovery cache), and what-schema-gates-userdata (userdata_schema).

## Why it matters

- **`discovery-cache`** is the registration-time validation gate. An agent reasoning about "what does template registration check?" naturally hunts for a noun, finds nothing standalone, and has to discover it as a sub-field on the observability struct. It is the structural object that mediates between executor capabilities and rimsky-side validation gates — load-bearing at registration time, gates `on_event` handler declarations, mediates the unknown-event-as-no-op runtime behavior when a peer was unreachable at registration.
- **`cascade-graph`** is structurally different from the peer-protocol surface. One direction is "rimsky reads from peers"; the other is "operators read from rimsky." Bundling them obscures the directionality. The cascade-graph is the canonical operator-dashboard backplane; treating it as a sub-endpoint of `observability` understates its role.
- Catalog parsimony cuts both ways: 46 concepts is high, but it is high precisely because some concepts (like this one) bundle multiple structural surfaces that readers naturally treat as nouns.

This decision also disposes of `review-notes.md` "Suspected-but-unconfirmed concepts" / `discovery-cache` (a separate promotion candidate flagged by phase 2 review).

## Resolution candidates (do NOT pick)

- **Promote both** `concepts/cascade-graph.md` (operator-dashboard backplane: the HTTP route group, the routes it exposes, the read-only-of-rimsky-state framing, the `inTx`-per-handler invariant) and `concepts/discovery-cache.md` (in-memory per-peer `Capabilities` cache, populated by handshake, consulted at template registration for `on_event` cross-check, mediates unknown-event-as-no-op runtime fallback). Slim `observability` down to: (a) the optional peer protocols, (b) the handshake mechanism that feeds both new concepts, (c) the `userdata_schema` validation surface. Net +1 concept (3 → 4 covering the same material).
- **Promote `discovery-cache` only**; keep `cascade-graph` as a sub-endpoint inside `observability`. Net +1 concept.
- **Promote `cascade-graph` only**; keep `discovery-cache` as a sub-field inside `observability`. Net +1 concept.
- **Keep status quo** — one concept covering all four surfaces.

(Pre-decided shape: promote both.)

## Evidence

- `concepts/observability.md`.
- `_discover/observability-cascade-graph-endpoint.md`.
- `_discover/observability-handshake-discovery-cache.md`.
- `_discover/2026-05-10-observability-optional-protocols.md`.
- `review-notes.md` "Judgment calls" / `observability` bullet; "Suspected-but-unconfirmed concepts" / `discovery-cache` bullet.

