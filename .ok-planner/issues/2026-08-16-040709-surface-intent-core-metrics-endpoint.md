---
issue: surface-intent-core-metrics-endpoint
kind: audit
category: unclear
artifacts:
  - concept:observability
status: verified
opened: 2026-08-16T04:07:09Z
---

# The surface intent does not say whether the core roles' metrics endpoint is public

Each core role can serve a Prometheus metrics endpoint on a port chosen by environment variables the intent already calls public. The endpoint itself falls under no named public HTTP class, so the extractor defaulted it internal. Public would commit the metric names and labels as a compatibility surface; internal leaves the metric set free to change. The ruling amends the intent.

## Options

- Public: an operator's dashboards can rely on metric names; cost: metric renames become breaking changes.
- Internal: metrics stay an operator convenience with no promise; cost: dashboards can break silently on upgrade.

The ruling decides whether the metric set is a promise.

## Ruling

> Recommended ruling (/verify-issues): Call the endpoint public and treat the metric names it serves as surface — the run's own story on named-lock metrics is measured through it, and an operator who graphs it is a consumer by the intent's general rule.
>
> Rationale: the environment variables that place the listener are already public, and a public knob to an internal surface is incoherent; naming metrics as surface is what lets the audit measure dashboards at all. Flip case: if the metric set is still being reshaped and the owner wants freedom to rename before v1, name it internal now and revisit at v1.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
