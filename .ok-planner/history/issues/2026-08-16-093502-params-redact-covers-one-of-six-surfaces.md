---
issue: params-redact-covers-one-of-six-surfaces
kind: audit
category: conflicting
artifacts:
  - concept:template
  - concept:event-log
  - concept:control-api
  - decision:secret-at-rest-posture
status: promoted
sprint: 2026-08-21-intake-drain-and-concept-repair.md
opened: 2026-08-16T09:35:02Z
---

# A template's params_redact hides a value on one read surface and returns it in clear on five

A template can name instance parameters as redacted, and the key's name tells a reader the value never comes back in clear. Rimsky applies the redactor on the control API's instance read and instance list only, fail-closed. The auth trail captures every create request's body verbatim into an access-attempted row, and the event, audit and observability event reads return that row. The observability instance views serialize the raw instance row. One deployment therefore returns the same secret redacted from one route and in clear from five. The corpus says nothing about the key. The ruling decides what redaction governs.

## Options

- Give the template concept an invariant that names every read surface the key governs, including the auth trail's captured body, and route every one of them through the redactor; cost: changes the audit capture that is verbatim by design.
- Record that the audit trail stays verbatim by design and that redaction only affects instance reads, then rename the key so it promises no more than that; cost: an operator who wants secrets kept out of the trail has no tool.
- Split the promise: instance-shaped reads redact, and each audit and event surface's own permission governs access there, stated plainly as equal to secret access; cost: three permissions become secret-bearing by declaration.
- Retire the key: delete `params_redact` and its redactor, and record that instance params are cleartext operational data; cost: a template author who wants a value hidden from instance reads loses the mask.

The ruling decides how far a redacted parameter's protection reaches.

## Ruling

> Retire the key. Delete `params_redact` — the spec field, the redactor, its call-site plumbing, and its tests — and record in the template concept that instance params are cleartext operational data: a secret belongs in a surface that carries an absolute guarantee, never in params. `decision:secret-at-rest-posture` is the standing posture — rimsky protects its own crown-jewel secrets, delegates the rest to operator controls, and the one guarantee it owns is absolute (never logged, never returned over any API surface). A one-surface display mask over cleartext rows is a weaker fourth tier that decision never named, and the key predates it. Ruled live by the owner, 2026-08-20.
