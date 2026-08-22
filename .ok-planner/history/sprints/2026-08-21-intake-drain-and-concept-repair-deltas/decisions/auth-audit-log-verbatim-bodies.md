---
decision: auth-audit-log-verbatim-bodies
---

# Auth audit rows store request bodies verbatim

## Choice

An auth audit-log row stores the request body verbatim. Rimsky redacts nothing and truncates nothing.

## Rationale

A control-plane request body carries no secret. The one sensitive value in an auth-relevant exchange is the api key, and it travels in the auth header, which the audit row never stores (see `concept:api-key`, `concept:control-api`). Verbatim bodies let an operator answer forensic questions about what a caller asked for. Storing the bytes without inspecting them keeps the row inside the inertness discipline (see `concept:inertness`).

## Alternatives

- Redact each body down to an allowlist of fields — rejected: it drops the parameters a forensic query needs, and it makes rimsky read bytes the inertness discipline keeps opaque.
- Store no body at all — rejected: the row then records that a call happened without recording what it asked for.
