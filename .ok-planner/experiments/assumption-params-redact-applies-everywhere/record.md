---
experiment: assumption-params-redact-applies-everywhere
commit: PENDING
---

# Where a redacted param still surfaces

## What it ran against

One `rimsky-all-in-one` container from this tree's image set, an admin key, a
template whose `params_schema` declares a `secret` param and whose
`params_redact` names it, and an instance created with a distinctive literal
in that param. After driving a node reset so frames and events exist, the run
reads 13 public surfaces with that same admin key and greps each response body
for the literal, then greps the deployment's own process logs. The population
is the surfaces the prior names — the instance read, the event log, the audit
log — plus the frame and node reads, the listing, and the observability views
the same key reaches.

## What was observed

The redaction covers the instance read and stops there. `GET
/v1/instances/{id}` returns `"secret": "[REDACTED]"`, and the listing, the
frame reads, the node reads, the instance-filtered event query, and the
process logs are all clean.

Five surfaces return the literal in full: `GET /v1/events`, `GET /v1/audit`,
`GET /v1/observability/events`, `GET /v1/observability/instances/{id}`, and
`GET /v1/observability/instances`. The carrier in the first three is the same
row — an `auth.access_attempted` event whose `payload.request_params` holds
the verbatim create request, `{"params": {"secret": "<literal>", …}}` — and in
the observability instance views it is the instance's raw `params` object,
unredacted beside the redacted copy the ordinary read returns. One key with
`event:read`, `audit:read`, or `observability:read` recovers what
`params_redact` removed from `instance:read`. 1 check, 0 pass, 1 fail.
