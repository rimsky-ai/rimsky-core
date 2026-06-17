---
decision: asset-materialize-endpoint-retired
status: as-is
---

# Asset-materialize endpoint retired

## Choice

The asset-materialize control endpoint, its CLI subcommand, its MCP tool schema, and its action-permission row all retire. The asset list, detail, versions, materialization-history, and delete surfaces stay unchanged. Re-materialization is expressed through messages — the empty-message trigger for whole-instance re-run, or a typed message the template author designs for partial paths via author-declared subscription edges on the producer node(s).

## Rationale

The materialize-by-alias verb was sugar on ad-hoc invalidation of one specific node, which the principle rules out. Partial re-materialization with arbitrary upstream-staleness is a footgun: an asset produced by a full instance run can differ from one produced by a one-off invalidation of just its producer, because upstreams may carry stale values. Template authors who want partial re-runs own the safety question by choosing where the subscription edges land.

## Alternatives considered

Auto-inject a runtime-managed asset-materialize typed message — re-introduces the same footgun under a different name; keep the synthetic-envelope chokepoint just for this caller — preserves the architectural debt with no offsetting benefit.
