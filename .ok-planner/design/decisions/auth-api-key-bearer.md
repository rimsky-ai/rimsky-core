---
decision: auth-api-key-bearer
---

# Authentication model

## Choice

Api-key as bearer token.

## Rationale

Simple, stateless, service-account-friendly.

## Alternatives

- Signed tokens (OAuth2 / JWT) — rejected: issuer, expiry, and refresh machinery for callers that are services and CLIs; grants are read from the database per request anyway, so claims-in-token buys nothing.
- Session cookies — rejected: the callers are service accounts and CLIs, not browsers.
