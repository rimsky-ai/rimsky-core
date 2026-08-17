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

# A template's params_redact hides a value on one read surface and returns it in clear on five

A template can name instance parameters as redacted, and the key's name tells a reader the value never comes back in clear. Rimsky applies the redactor on the control API's instance read and instance list only, fail-closed. The auth trail captures every create request's body verbatim into an access-attempted row, and the event, audit and observability event reads return that row. The observability instance views serialize the raw instance row. One deployment therefore returns the same secret redacted from one route and in clear from five. The corpus says nothing about the key. The ruling decides what redaction governs.

## Options

- Give the template concept an invariant that names every read surface the key governs, including the auth trail's captured body, and route every one of them through the redactor; cost: changes the audit capture that is verbatim by design.
- Record that the audit trail stays verbatim by design and that redaction only affects instance reads, then rename the key so it promises no more than that; cost: an operator who wants secrets kept out of the trail has no tool.
- Split the promise: instance-shaped reads redact, and each audit and event surface's own permission governs access there, stated plainly as equal to secret access; cost: three permissions become secret-bearing by declaration.

The ruling decides how far a redacted parameter's protection reaches.

## Ruling

> Recommended ruling (/verify-issues): Make redaction reach every surface that returns instance parameters: the instance reads it covers, the observability instance views, and the auth trail's captured request body, redacted at capture before the row is written. State that in the template concept.
>
> Rationale: a key named redact that leaves the value in the audit log is a trap the run measured. The audit trail is the surface most likely to be widely readable. A redacted body still records that the request happened, which is the trail's job. Flip case: if replaying exactly what a caller sent is a requirement, the third option is the honest posture, with the permissions named as secret-bearing.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
