# Rimsky

## What it is

Rimsky is a project-agnostic reactive node-graph orchestration platform. Workflows are declarative graphs of nodes; when an upstream value changes, downstream dependents are marked `stale` via an `invalidate` cascade, and the scheduler recalculates them by dispatching their executors. Coordination across shared state is handled by claims (producer-mediated, scoped) and named locks (deployment-level scalars).

## Point your favorite coding agent at this surface

Rimsky's documentation is built for agent-mediated adoption. The recommended consumption path is through your coding agent. Useful entry points:

- **For agents that follow the convention**: [`agents/llms.txt`](../agents/llms.txt) — curated index pointing at every public-surface page.
- **For per-noun reference**: the canonical concept catalog at [`.ok-planner/design/concepts.md`](../../.ok-planner/design/concepts.md) — an auto-generated TOC over per-concept files with definitions, boundaries, and invariants, plus inline `@concept:` annotations pointing at enforcement sites in code.
- **For implementing a custom claim producer, executor, or lifecycle subscriber**: [`protocols/`](../protocols/).
- **For copy-pasteable starter examples**: [`agents/examples/`](../agents/examples/).

## Dashboard

The bundled `dashboards/` collection is a read-only reference UI for observing what Rimsky is doing. Launch it via the docker-compose stack (`docker compose -f deploy/docker-compose.yml --profile dashboard up`) or as a standalone container.
