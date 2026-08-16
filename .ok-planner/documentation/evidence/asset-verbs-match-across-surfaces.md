---
trap: asset-verbs-match-across-surfaces
release: d977250c
---
# Evidence set — every asset operation exists identically on all three surfaces — CLI, REST, and MCP — so `rimsky asset lineage` has a matching route and tool.

Source of the prior: sibling-symmetry — `rimsky asset {list,show,versions,delete,lineage}` against `asset_{list,get,versions,delete,materialization_history}` MCP tools and five asset routes

## What the audit ran and observed (assumption record)

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

## Experiment record (experiment:assumption-asset-verbs-match-across-surfaces)

# Do the asset operations match across CLI, REST and MCP?

## What it ran against

One `rimsky-all-in-one` container from the tree's own image tag, seeded with a
template, a deploy and an instance so every surface has a real instance id to
address. It runs each `rimsky asset` subcommand, probes each asset route,
initializes an MCP session and lists the tools, and builds the six-by-three
coverage table from what each surface answered.

## What was observed

Six asset operations exist across the three surfaces, and two of them are absent
from a surface:

    list                     CLI yes  REST yes  MCP yes
    get                      CLI yes  REST yes  MCP yes
    versions                 CLI yes  REST yes  MCP yes
    delete                   CLI yes  REST yes  MCP yes
    materialization-history  CLI no   REST yes  MCP yes
    lineage                  CLI yes  REST no   MCP no

`rimsky asset` names exactly five subcommands, `<list|show|versions|delete|
lineage>`, and `rimsky asset materialization-history` is refused with
`unknown subcommand "materialization-history"`. On REST, the five asset routes
are mounted — `assets`, `assets/{alias}`, `.../versions`,
`.../materialization-history` and `DELETE assets/{alias}` all reach a handler
(200, or a JSON `asset not found` for the alias that does not exist yet) — while
`assets/{alias}/lineage` answers chi's `404 page not found`, so it is not a
route. On MCP the asset tools are exactly `asset_delete`, `asset_get`,
`asset_list`, `asset_materialization_history`, `asset_versions`; there is no
`asset_lineage`, and the lineage tools that do exist are claim- and run-shaped
(`lineage_get`, `lineage_claim_ancestors`, `lineage_claim_descendants`,
`lineage_run_ancestors`, `lineage_run_descendants`, `lineage_prune`).

`rimsky asset lineage` is a client-side composition rather than a surface
operation: run against a missing alias it fails on
`GET /v1/instances/{id}/assets/{alias}` before it ever reaches a lineage call.

EXPERIMENT PASS (21 checks)

Runnables: `src:.ok-planner/experiments/assumption-asset-verbs-match-across-surfaces/` at the stamped commit.
