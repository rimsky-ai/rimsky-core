---
decision: asset-materialize-endpoint-retired
status: as-is
---

# No asset-materialize verb; re-materialization goes through messages

## Choice

The control surface exposes no asset-materialize verb — no control endpoint, no CLI subcommand, no MCP tool, no action-permission row. The asset list, detail, versions, materialization-history, and delete surfaces exist unchanged. Re-materialization is expressed through messages — the empty-message trigger for whole-instance re-run, or a typed message the template author designs for partial paths via author-declared subscription edges on the producer node(s).

## Rationale

A materialize-by-alias verb is sugar on ad-hoc invalidation of one specific node, which the principle rules out. Partial re-materialization with arbitrary upstream-staleness is a footgun: an asset produced by a full instance run can differ from one produced by a one-off invalidation of just its producer, because upstreams may carry stale values. Template authors who want partial re-runs own the safety question by choosing where the subscription edges land.

## Alternatives

- Auto-inject a runtime-managed asset-materialize typed message — rejected: re-introduces the same footgun under a different name.
- Keep the synthetic-envelope chokepoint just for this caller — rejected: preserves the architectural debt with no offsetting benefit.
