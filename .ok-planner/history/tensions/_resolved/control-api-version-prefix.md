---
tension: control-api-version-prefix
category: unspecified
status: resolved
affects:
  - control-api
  - rimsky
---

# Control-API uses bare paths (no `/v1/`); the post-v1 commitment is unspecified

## Resolution

`decision:protocol-version-v1-namespaced` sweeps every control-API HTTP route under `/v1/`, aligning the URL layer with the already-versioned proto package. Pre-v1 freedom means no transition window; bare paths are removed when the sweep lands. Code work implied by this resolution: every route registration in the control layer's control-api package moves under a `/v1/` mount; the MCP route catalog and every test that hits a bare path updates in lockstep; the rimsky CLI client issues requests against the `/v1/` paths. Resolved by `spec:2026-06-08-design-corpus-bootstrap`.

## What is muddy

CLAUDE.md "Non-obvious gotchas": "rimsky-cli is a thin client; v1 does not version the control-api. Bare paths (no /v1/ prefix); rolling upgrades are operator-managed."

The bare-paths shape is deliberate v1-deferral. But:

- The post-v1 commitment is unspecified. Will v1 introduce `/v1/...`? Drop the bare paths? Support both?
- A CLI shipped from an older rimsky release may issue requests an older control-api rejects with 404 — known consequence with no client-side version negotiation.

## Why it matters

Anyone building tooling against the control-api (third-party CLIs, agent shims, dashboards) needs to know whether the URL shape is stable or transitional. Rolling upgrades work only if old + new are compatible, and the burden of guaranteeing that compatibility is on the operator.

## Resolution candidates (do NOT pick)

- v1 commits to the unversioned URL shape indefinitely, handling breaking changes through deprecation cycles rather than a path version (see `concept:control-api`).
- v1 introduces an explicit version prefix on the control-API paths and offers a transitional period during which both shapes are accepted.
- Add a version-discovery endpoint plus client-side version negotiation, so a client built against an older release can detect and adapt to the server's contract (see `concept:rimsky`).

## Evidence

- `_discover/2026-05-10-rimsky-cli-thin-client.md` Description + Observations bullet 1.

