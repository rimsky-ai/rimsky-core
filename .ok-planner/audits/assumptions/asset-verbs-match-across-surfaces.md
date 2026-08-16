---
assumption: asset-verbs-match-across-surfaces
commit: PENDING
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# every asset operation exists identically on all three surfaces — CLI, REST, and MCP — so `rimsky asset lineage` has a matching route and tool.

As operator managing assets, I would take it that every asset operation exists identically on all three surfaces — CLI, REST, and MCP — so `rimsky asset lineage` has a matching route and tool.

## Source

sibling-symmetry — `rimsky asset {list,show,versions,delete,lineage}` against `asset_{list,get,versions,delete,materialization_history}` MCP tools and five asset routes

## What a run would observe

map the five asset CLI verbs onto the asset routes and MCP tools and name the ones with no counterpart on some surface.

## Measured

Experiment `assumption-asset-verbs-match-across-surfaces`, run at this tree
against one `rimsky-all-in-one` container seeded with a template, a deploy and an
instance. Six asset operations exist across the three surfaces and two are
missing from a surface. `rimsky asset lineage` is CLI-only: there is no
`assets/{alias}/lineage` route (chi answers `404 page not found`, unlike the five
mounted asset routes, which reach a handler) and no `asset_lineage` MCP tool —
the asset tools are exactly `asset_delete`, `asset_get`, `asset_list`,
`asset_materialization_history`, `asset_versions`, and the lineage tools that do
exist are claim- and run-shaped. The verb is a client-side composition: run
against a missing alias it fails on `GET /v1/instances/{id}/assets/{alias}`
before reaching any lineage call. The asymmetry runs the other way too:
materialization history has a route and an MCP tool but no CLI verb —
`rimsky asset` names five subcommands, `<list|show|versions|delete|lineage>`, and
`rimsky asset materialization-history` is refused with
`unknown subcommand "materialization-history"`. Four operations (list, get,
versions, delete) do appear on all three.
