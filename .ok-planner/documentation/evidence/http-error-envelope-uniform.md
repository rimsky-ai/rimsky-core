---
trap: http-error-envelope-uniform
release: d977250c
---
# Evidence set — every non-2xx response from the control API carries the same JSON error envelope with a stable machine-readable code field, so error handling is written once.

Source of the prior: craft-convention — a versioned JSON REST surface of ~92 routes

## What the audit ran and observed (assumption record)

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

## Experiment record (experiment:assumption-http-error-envelope-uniform)

# One error envelope across the control API?

## What it ran against

One `rimsky-all-in-one` container from the tree's own image tag, driven entirely
through the control API over HTTP and the `rimsky auth` CLI verbs for the key
bootstrap. It provokes 400, 401, 403, 404, 409 and 500 across the template, tag,
instance, node, run, message, auth, observability and MCP route families and
compares the body shapes and field names.

## What was observed

The core CRUD families answer with one flat envelope: a single `error` key whose
value is a human-readable string. Seven 404s and two 400s carried exactly that
shape. A 409 keeps `error` a string and may add its own field beside it — the
undeploy conflict adds `active_count`. No core error body carries `code`,
`error_code` or `type`: there is no machine-readable discriminator to switch on,
only prose.

The envelope's fields are not stable across statuses on one route. A 401 carries
`error: "unauthorized"` plus `denial_reason` (`no_token` or `invalid_token`); a
403 carries `{"error": "permission denied"}` and nothing else.

Three further envelopes share the same versioned surface. The whole
`/v1/observability/...` family nests the error as an object —
`{"error": {"code": ..., "message": ...}}` — on its 400s, 404s and 500s, so
`error` is a string on one half of `/v1` and an object on the other, and the
machine-readable code the prior expects exists only here. `POST /v1/mcp` answers
in a JSON-RPC envelope (`jsonrpc`, `id`, `error.code`, `error.message`). An
unmatched `/v1` path answers `404 page not found` as `text/plain`, and a wrong
method answers 405 with an empty body — neither is JSON at all.

Sharpest of all: `GET /v1/observability/instances/{id}` answers the flat string
envelope when the token is missing and the nested object envelope when the
instance is not found. One route, two envelopes, chosen by status.

EXPERIMENT PASS (30 checks)

Runnables: `src:.ok-planner/experiments/assumption-http-error-envelope-uniform/` at the stamped commit.
