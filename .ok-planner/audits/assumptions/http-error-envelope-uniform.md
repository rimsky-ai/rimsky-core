---
assumption: http-error-envelope-uniform
commit: PENDING
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# every non-2xx response from the control API carries the same JSON error envelope with a stable machine-readable code field, so error handling is written once.

As client-library author, I would take it that every non-2xx response from the control API carries the same JSON error envelope with a stable machine-readable code field, so error handling is written once.

## Source

craft-convention — a versioned JSON REST surface of ~92 routes

## What a run would observe

provoke a 400, 401, 403, 404, and 409 across several route families and compare body shapes and field names.

## Measured

Ran `experiments/assumption-http-error-envelope-uniform` (30 checks, pass) against
one `rimsky-all-in-one` container at this tree, provoking 400, 401, 403, 404, 409
and 500 across the template, tag, instance, node, run, message, auth,
observability and MCP route families.

A client-library author would write one error decoder. Four different shapes come
back off `/v1`. The core CRUD families answer `{"error": "<human string>"}` — no
`code`, no `error_code`, no `type`, nothing machine-readable to switch on. The
whole `/v1/observability/...` family nests the error as an object,
`{"error": {"code": "not_found", "message": "..."}}`, so `error` is a string on
one half of the versioned surface and an object on the other. `POST /v1/mcp`
answers in a JSON-RPC envelope. An unmatched `/v1` path answers `404 page not
found` as `text/plain`, and a wrong method answers 405 with an empty body.

Even one route is not one shape: `GET /v1/observability/instances/{id}` answers
the flat string envelope on 401 and the nested object envelope on 404. Fields
also come and go by status inside the flat envelope — a 401 adds `denial_reason`,
a 403 does not, a 409 on undeploy adds `active_count`.

The one machine-readable `code` that exists anywhere is on the observability
family, which is the half of the surface the prior is least likely to be written
against first.
