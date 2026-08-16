---
issue: params-redact-covers-one-of-six-surfaces
kind: audit
category: conflicting
artifacts:
  - concept:template
  - concept:event-log
  - concept:control-api
status: verified
opened: 2026-08-16T09:35:02Z
---

# A template's params_redact suppresses a value on one read surface and leaks it on five

A template can name instance parameters as redacted; the intent a reader takes from the key is that the value never comes back in clear. The redactor is applied on the control API's instance read and list only, fail-closed. The auth trail captures every create request's body verbatim into an access-attempted row, which the event, audit and observability event reads return; and the observability instance views serialize the raw instance row. The same secret returns as redacted from one route and in clear from five, in one deployment. The corpus is silent on the key entirely. The ruling decides what redaction governs.

## Options

- Give the template concept an invariant naming every read surface the key governs — including the auth trail's captured body — and route them all through the redactor; cost: touches the deliberately verbatim audit capture.
- Record that the audit trail is verbatim by design and redaction is display-only on instance reads, and rename the key so it stops promising more; cost: an operator who wanted secrets kept out of the trail has no tool.
- Split the promise: instance-shaped reads redact; audit and event surfaces are governed by their permissions, stated plainly as equivalent to secret access; cost: three permissions become secret-bearing by declaration.

The ruling decides how far a redacted parameter's protection reaches.

## Ruling

> Recommended ruling (/verify-issues): Make redaction reach every surface that returns instance parameters — the instance reads it already covers, the observability instance views, and the auth trail's captured request body (redacted at capture, before the row is written) — and state that in the template concept.
>
> Rationale: a key named redact that leaves the value in the audit log is a trap the run measured, and the audit trail is the surface most likely to be widely readable; capturing a redacted body still records that the request happened, which is the trail's job. Flip case: if forensic fidelity of the raw request is a requirement (replaying exactly what a caller sent), the third option with the permissions named as secret-bearing is the honest posture.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
