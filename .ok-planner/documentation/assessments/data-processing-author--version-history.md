---
assessment: data-processing-author--version-history
subject: story:data-processing-author
way: version-history
release: d977250c
outcome: held
warrant: experiment:data-processing-author
---
# Version history reaches readers through the author's own listing surface

The fan-out's claim appears as an asset, and reading that asset's versions — through `catalog:http-routes/GET /v1/instances/{id}/assets/{alias}/versions` and through `catalog:cli-verbs/rimsky asset versions` — calls the producer's own list-versions verb. What a reader gets back is therefore whatever the author's data model holds, surfaced through rimsky rather than replaced by it: the author owns the version history and rimsky owns the route to it.

## Unverified remainder

The run's producer records versions against its sub-claims rather than against the parent the asset route names, so the listing came back empty for that asset. That is this producer's data model, not something the route decides; the run does not demonstrate a populated history through the asset surface.
