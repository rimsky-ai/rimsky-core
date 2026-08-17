---
issue: handler-package-scope-for-services-without-inproc-path
kind: audit
category: unclear
artifacts:
  - decision:handler-package-in-service-directory
status: verified
opened: 2026-08-16T08:48:07Z
---

# The handler-package decision covers eleven services, but only six can follow it

A decision says every bundled service directory keeps its logic in an importable handler package beside a thin command, so the same code serves standalone and in-process. Its rationale and every rejected alternative assume the in-process path. Only executors and claim producers have one. The four sensors and the OpenLineage subscriber register nowhere in-process and keep everything in their command package. Read literally, five services violate the decision. Read by its rationale, they were never in scope. The ruling settles the scope.

## Options

- Narrow the Choice to services with both a standalone and an in-process surface, and state what a single-surface service owes instead; cost: five services' layout stays undecided by rule.
- Keep the universal and restructure the five to expose handler packages anyway, for test importability and reuse; cost: five refactors with no in-process consumer to justify them.
- Keep the universal and extend the in-process registry to sensors and subscribers; cost: a new consumption path for two service classes with no driving need.

The ruling decides what population the decision quantifies over.

## Ruling

> Recommended ruling (/verify-issues): Narrow the decision to the dual-mode services, the executors and claim producers, and say plainly that a service with no in-process path owes only the thin-command shape.
>
> Rationale: the decision's whole justification is dual-mode reuse. Where there is no second mode there is nothing to share, and the registry-entrypoint decision already fixes which services are in-process. Flip case: if sensors or subscribers gain an in-process registration when the all-in-one grows them, they enter the population and get the handler package then.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
