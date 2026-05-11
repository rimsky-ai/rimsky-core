---
tension: control-api-version-prefix
category: unspecified
status: open
affects:
  - control-api
  - rimsky-cli
---

# Control-API uses bare paths (no `/v1/`); the post-v1 commitment is unspecified

## What is muddy

CLAUDE.md "Non-obvious gotchas": "rimsky-cli is a thin client; v1 does not version the control-api. Bare paths (no /v1/ prefix); rolling upgrades are operator-managed."

The bare-paths shape is deliberate v1-deferral. But:

- The post-v1 commitment is unspecified. Will v1 introduce `/v1/...`? Drop the bare paths? Support both?
- A CLI shipped from an older rimsky release may issue requests an older control-api rejects with 404 — known consequence with no client-side version negotiation.

## Why it matters

Anyone building tooling against the control-api (third-party CLIs, agent shims, dashboards) needs to know whether the URL shape is stable or transitional. Rolling upgrades work only if old + new are compatible; CLAUDE.md acknowledges this is operator-managed.

## Resolution candidates (do NOT pick)

- v1 commits to bare paths indefinitely (with breaking changes via deprecation cycles).
- v1 introduces `/v1/` and offers a transitional period.
- Add a `GET /version` endpoint and CLI-side version negotiation.

## Evidence

- `_discover/2026-05-10-rimsky-cli-thin-client.md` Description + Observations bullet 1.

