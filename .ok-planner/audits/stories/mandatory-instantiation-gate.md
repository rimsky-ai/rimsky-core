---
audit: mandatory-instantiation-gate
artifact: story:mandatory-instantiation-gate
determination: supported
compliance: noncompliant
commit: PENDING
audited: 2026-08-10T05:20:00Z
---

# Instance create validates static attribute config against each service's schema

Supported. Against a zero-config all-in-one deployment, templates carrying their
attribute config only in the template-level per-executor defaults — the site
registration's composition check does not read — registered and deployed, and
instance create then refused each misconfigured one. The refusal named the node,
the attribute and the violated constraint: an empty array against a minimum-items
constraint, which is a value constraint rather than a shape mismatch, and a
number against a string type on a second referenced service. No instance row was
created in either case, and a template satisfying both services' schemas created
cleanly with both its nodes. The refusal detail rides the control-api response;
the CLI relays only its summary line and drops the per-attribute findings, so the
promise is reachable through one of the two public ways an operator creates an
instance.

## Compliance

The capability clause promises "a clear error", an adjective only a human
judgment can settle, where the story rules require the observable statement of
what the user gets; the compliant text is "refuses the create with an error
naming the misconfigured attribute".
