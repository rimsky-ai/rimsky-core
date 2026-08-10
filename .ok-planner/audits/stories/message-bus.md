---
audit: message-bus
artifact: story:message-bus
determination: supported
compliance: noncompliant
commit: PENDING
audited: 2026-08-10T07:25:00Z
---

# Sending with a dedup key, reading the history, and replaying without a duplicate

Supported. Against a zero-config all-in-one deployment, a send carrying no dedup
key was refused and a send carrying one was accepted; both replays under that
same key — one repeating the body, one changing it — returned the original
message identity, and the instance's history held one row for the key rather
than three. The history listed both distinct sends attributed to the operator,
the fetch-by-id route returned the row with its body and instance, an id never
minted was not found, and both bodies reached the downstream node. All 4 clauses
the story names were exercised on that run. The operator's second public read
path is weaker: the CLI retrieves one message by id correctly, but its history
verb returns only the newest row, so the history clause is reachable through one
of the two public ways an operator reads the bus.

## Compliance

The benefit clause promises that downstream nodes consume the bus "reliably", an
adjective only a human judgment can settle, where the story rules require an
observable statement; the compliant text is "so that downstream nodes consume
the bus and no replay slips through".
