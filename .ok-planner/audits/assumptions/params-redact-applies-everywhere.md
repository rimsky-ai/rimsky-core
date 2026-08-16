---
assumption: params-redact-applies-everywhere
commit: PENDING
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# `params_redact` suppresses the named params everywhere they could surface — instance GET responses, event-log payloads, audit rows, and process logs.

As operator handling secrets in params, I would take it that `params_redact` suppresses the named params everywhere they could surface — instance GET responses, event-log payloads, audit rows, and process logs.

## Source

name-promise — a template key literally named `params_redact`

## What a run would observe

mark a param redacted, create an instance with it, then read the instance, its frames, `/v1/events`, and `/v1/audit` looking for the value.

## Measured

`.ok-planner/experiments/assumption-params-redact-applies-everywhere` — built
for this run — registered a template naming a `secret` param in
`params_redact`, created an instance carrying a distinctive literal, drove a
node reset, then read 13 public surfaces with the same admin key and grepped
each body for the literal, plus the deployment's process logs.

The redaction covers one surface. `GET /v1/instances/{id}` returns `"secret":
"[REDACTED]"`; the listing, the frame reads, the node reads, the
instance-filtered event query, and the process logs are clean.

Five surfaces return the value in full: `GET /v1/events`, `GET /v1/audit`,
`GET /v1/observability/events`, `GET /v1/observability/instances/{id}`, and
`GET /v1/observability/instances`. In the first three the carrier is one row —
an `auth.access_attempted` event whose `payload.request_params` holds the
verbatim create request, secret and all. In the observability instance views
it is the instance's raw `params` object, sitting unredacted beside the
redacted copy the ordinary instance read returns from the same deployment to
the same key.

Two of the three surfaces the prior names — the event log and the audit log —
are therefore the two that keep the secret, and the mechanism is the audit
trail itself: recording the request that carried the secret is what defeats
the flag that was set to suppress it. An operator who reads `params_redact`
as "this value will not appear in the platform's records" has it backwards for
every reader holding `event:read`, `audit:read`, or `observability:read`.
1 check, 0 pass, 1 fail.
