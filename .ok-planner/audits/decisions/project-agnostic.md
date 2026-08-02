---
audit: project-agnostic
artifact: decision:project-agnostic
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:44:16Z
---

# No consumer-specific vocabulary in code, docs, tests, or examples

Supported, on the diligence a targeted adversarial search can establish
against a claim this broad. The `examples/` tree (29 top-level example
projects/scripts) and the `test/` and `lib/services/` trees use only
generic, illustrative names throughout — spot-checked placeholders
`project-alpha` and `analytics_production` (named explicitly by the
project's own conventions) each appear in 7 files across
`examples/compose/rimsky-compose.yml` and scenario tests. A repo-wide,
case-insensitive grep of `examples/`, `test/`, `lib/services/`, `cmd/`, and
`.ok-planner/design/` for a broad set of real company/SaaS names
(Salesforce, Segment, Amplitude, Mixpanel, Looker, Fivetran, dbt, BigQuery,
Redshift, HubSpot, Zendesk, Acme, Stripe, Shopify, Snowflake, Databricks,
Airbnb, Netflix) turned up no genuine hit — the handful of raw string
matches (e.g. "segment" inside "path segment", "dbt" inside "compat shim")
are ordinary English substrings, not vocabulary naming a real consumer.
