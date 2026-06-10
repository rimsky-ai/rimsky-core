---
decision: protocol-version-v1-namespaced
status: as-is
---

# Protocol versioning

## Choice

`rimsky.v1` proto package; **all control-API HTTP routes mounted under `/v1/`**, including the executor async-callback URL (`/v1/callback/{async_ack_id}`) and the observability sub-router (`/v1/observability/`). The bare-path routes that exist today (`/templates`, `/instances`, `/tags`, `/nodes`, `/messages`, `/events`, `/audit`, `/auth/*`, `/lineage/*`, `/admin/*`, `/diagnostics/*`, `/backfills/*`, `/lock-holders/*`, `/health`, `/mcp`) get swept to `/v1/...`.

## Rationale

Consistent versioned contract surface across the whole control-API; aligns the URL layer with the already-versioned proto package; existing `/v1/` carve-outs become the leading edge of one rule rather than exceptions. Pre-v1 freedom (per `decision:pre-v1-break-freely`) means no transition window; old bare paths are removed when the sweep lands. Resolves `tension:control-api-version-prefix`.

## Alternatives

Committing to bare paths indefinitely (leaves the two existing `/v1/` carve-outs as permanent exceptions); adding version-discovery + client-side negotiation (introduces a new mechanism not justified by current scope).

## Notes

2026-06-08 — Decision recorded via spec 2026-06-08-design-corpus-bootstrap.
