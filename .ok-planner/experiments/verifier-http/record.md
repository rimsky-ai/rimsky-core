---
experiment: verifier-http
commit: PENDING
---

# The bundled HTTP-callout verifier against an external check service

## What it ran against

`run.py` starts a small HTTP check service on a free host port, then boots a
`rimsky-all-in-one` container from this tree's image at `RIMSKY_IMAGE_TAG` on
its zero-config SQLite defaults. The bundled `verifier-http` executor runs
in-process inside that container and reaches the check service at
`host.docker.internal`. The check service answers `200` on `/pass`, `422` with
`{"class": "schema_mismatch"}` on `/reject`, and `503` with
`{"class": "upstream_down"}` on anything else, and records every request body it
receives. `run.py` registers, deploys, and instantiates one template per leg,
reads the verifier node back off the node observability route, and removes both
the container and the check service.

## What was observed

Three legs, eight checks, none failing.

Against `/pass` the node settled fresh, its attributes recorded
`verifier_status: 200` and `verifier_pass: true`, and the check service received
the exact JSON object the template declared under `attributes.body`.

Against `/reject` the node settled with one failed run and no fresh run. The
terminal error class was `verifier/check_failed/schema_mismatch` — the
`verifier/check_failed` family with the upstream's own class appended — and the
error payload carried `actual_status: 422`, `expected_status: "2xx"`, and
`upstream_class: "schema_mismatch"`.

Against the `503` route the node likewise settled failed, with the terminal
error class `verifier/check_failed/upstream_down`.
