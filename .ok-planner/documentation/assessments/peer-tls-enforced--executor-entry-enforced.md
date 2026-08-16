---
assessment: peer-tls-enforced--executor-entry-enforced
subject: story:peer-tls-enforced
way: executor-entry-enforced
release: d977250c
outcome: held
warrant: experiment:peer-tls-enforced
---
# Requiring transport security on an executor entry refuses a peer that cannot present it

The audit pointed two executor entries at one plaintext peer process, the entries differing only in the transport setting. The entry with it off was reported reachable at that setting through `catalog:http-routes/GET /v1/observability/executors/{name}`; the entry with it required was reported unreachable, and the reported failure named both the peer and the setting rather than a bare connection error. The refusal reaches the work, not just the status surface: a node dispatched at the off entry settled fresh with a success terminal, while a node dispatched at the required entry — the same peer, the same process — settled failed with a dial-failure terminal. The operator therefore finds out at once when a peer cannot meet the requirement.

## Unverified remainder

Two entries against one plaintext peer were driven. The way does not establish what a peer presenting an invalid or expired credential produces, as distinct from one presenting none.
