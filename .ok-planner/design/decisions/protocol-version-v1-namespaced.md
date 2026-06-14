---
decision: protocol-version-v1-namespaced
status: as-is
---

# Protocol versioning

## Choice

`rimsky.v1` proto package; **all control-API HTTP routes mounted under `/v1/`**, including the executor async-callback URL (`/v1/callback/{async_ack_id}`) and the observability sub-router (`/v1/observability/`). Every route (`/v1/templates`, `/v1/instances`, `/v1/tags`, `/v1/nodes`, `/v1/messages`, `/v1/events`, `/v1/audit`, `/v1/auth/*`, `/v1/lineage/*`, `/v1/admin/*`, `/v1/diagnostics/*`, `/v1/backfills/*`, `/v1/lock-holders/*`, `/v1/health`, `/v1/mcp`) is mounted under the `/v1/` prefix.

## Rationale

Consistent versioned contract surface across the whole control-API; aligns the URL layer with the already-versioned proto package (see `decision:pre-v1-break-freely`).

## Alternatives

Committing to bare paths indefinitely (leaves the two existing `/v1/` carve-outs as permanent exceptions); adding version-discovery + client-side negotiation (introduces a new mechanism not justified by current scope).
