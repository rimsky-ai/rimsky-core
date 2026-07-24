# filesystem claim producer

Bundled claim producer that persists claim scope and payloads under a
configurable filesystem root. Ships as image
`rimsky-claim-producer-filesystem`.

## Asset surface not reachable through this producer

The **asset surface** — per `concept:asset`, a committed claim against a
**data-processing-capable** producer with a **durable** lifetime, exposing
asset-presentation queries and cross-subgraph co-holds of the committed
value — is **not reachable** via this bundled producer. This producer does
not advertise `data_processing` in its capabilities handshake, so claims
against it, however declared, are never presented on the asset surface.

Templates that need asset behaviors — asset-presentation queries, or
cross-subgraph co-holds of durable committed claims presented as assets —
require an **operator-provided** claim producer that advertises
`data_processing`.

A durable claim declared against this bundled producer is still legal —
it persists past its subgraph — it just is not asset-presentable.
