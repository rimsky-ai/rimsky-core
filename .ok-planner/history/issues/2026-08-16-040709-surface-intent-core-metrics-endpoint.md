---
issue: surface-intent-core-metrics-endpoint
kind: audit
category: unclear
artifacts:
  - concept:observability
status: promoted
sprint: 2026-08-21-intake-drain-and-concept-repair.md
opened: 2026-08-16T04:07:09Z
---

# The surface intent does not say whether the core roles' metrics endpoint is public

Each core role can serve a Prometheus metrics endpoint. Environment variables the intent already calls public choose the port. The endpoint falls under no named public HTTP class, so the extractor defaulted it internal. Public would commit the metric names and labels as a compatibility surface. Internal leaves the metric set free to change. The ruling amends the intent.

## Options

- Public: an operator's dashboards can rely on metric names; cost: metric renames become breaking changes.
- Internal: metrics stay an operator convenience with no promise; cost: dashboards can break silently on upgrade.

The ruling decides whether the metric set is a promise.

## Ruling

> Recommended ruling (/verify-issues): Call the endpoint public and treat the metric names it serves as surface. The run measures its own named-lock metrics story through the endpoint, and the intent's general rule makes an operator who graphs it a consumer.
>
> Rationale: the intent already calls the environment variables that place the listener public, and a public knob onto an internal surface is incoherent. Naming metrics as surface is what lets the audit measure dashboards at all. Flip case: if the owner is still reshaping the metric set and wants freedom to rename before v1, name the endpoint internal now and revisit at v1.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
