---
audit: mandatory-instantiation-gate
artifact: story:mandatory-instantiation-gate
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:42:46Z
---

# Instance create refuses statically misconfigured attribute values

Supported. Driven through the public surface against a container of the
released all-in-one image, with each template's attribute config placed where
registration's own composition check cannot see it, so the create-time gate is
the only thing that can catch it. A value constraint imposed by the referenced
service's schema — a minimum item count on a collection defaulted empty — was
caught: create was refused, no instance existed afterwards, and the refusal
body named the offending node, the offending attribute and the violated
constraint rather than a bare shape mismatch. The universal over referenced
services was taken on a two-service template whose second service's attribute
carried a type violation; that was refused too, naming the node bound to the
second service, so the gate is not limited to the first of the services a
template references. A control template satisfying both services' schemas
created cleanly with both nodes.

## Referrals

- referral: the refusal reaches the operator as a clear error
  established: the control-api refusal names node, attribute and violated
    constraint in one body; the CLI relays only the summary line of that
    refusal and drops the detail, confirmed by driving both in the same run
  discipline: ux
