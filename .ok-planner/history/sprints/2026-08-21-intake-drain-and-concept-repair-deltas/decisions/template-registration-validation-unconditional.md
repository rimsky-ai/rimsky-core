---
decision: template-registration-validation-unconditional
---

# Template registration validates every reference, with no relaxation

## Choice

Template registration validates every executor, store, and named-lock reference a template declares, and requires each declared executor's expected-attributes schema to be visible and satisfied. No operator setting relaxes any leg. A reference that cannot be validated fails registration. The spec itself declares the one exception: a template's late-bind list names the services registration does not check, and that list is part of the canonical spec bytes (see `concept:template`, `concept:service-address-book`).

## Rationale

Registration is the moment the author is present and can fix a name. A template that registers with an unresolvable reference fails later at dispatch, in front of an operator who did not write it, with a run already in flight. Refusing at registration turns every such failure into an authoring error. A relaxation setting would make one template valid in one deployment and broken in another, so an author could no longer read a template and know whether it registers. Register-before-provision already has a surface: the late-bind list, which names the exception in the spec rather than in an operator's configuration.

## Alternatives

- An operator setting that downgrades an unresolved reference to a warning — rejected: template validity becomes a per-deployment property, and the failure resurfaces at dispatch against a live run.
- Register first and validate at deploy — rejected: it moves the error away from the author and leaves a registered template nothing can run.
