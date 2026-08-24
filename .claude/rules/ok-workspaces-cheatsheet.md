# ok-workspaces Cheatsheet

Materialized by ok-workspaces v19.3.0 — suite-owned; refreshed by
the front door's administration (`/ok`); do not hand-edit. Profile:
`.ok-workspaces/config.json` (stacks: go, docker;
runtime: docker-compose).

Three rules. Each one makes the next one safe — ship any subset and the
isolation story has a hole.

1. **One worktree per job.** Every unit of work gets its own checkout
   on its own branch: directory `../wt-<job>`, branch
   `wt/<job>`. Never share a working tree between concurrent
   jobs; never do job work on the main checkout. Use `/open <job>`
   and `/close <job>`.

2. **One runtime stack per worktree.** Every workspace runs its own compose
project, namespaced by workspace: `COMPOSE_PROJECT_NAME=rimsky-core-<job>`
(set it in the workspace's local env, never hardcoded in a compose file).
Container names, networks, and volumes all derive from the project name,
so two workspaces can run their stacks concurrently without collision.
Host-port mappings must be parameterized (env var with a per-workspace
value), never fixed numbers shared across workspaces.

3. **Per-run artifacts.** Every verification run mints one fresh tag,
   builds every artifact it verifies under that tag, and hands the tag
   to its tests through the one environment variable this project
   declares. Run `.ok-workspaces/bin/run-tag` to mint the tag: it prints
   `run-<12 hex>`, a new value on every invocation. Tests resolve
   artifacts by that tag alone and fail loudly when the variable is
   unset or no artifact carries the tag. Never `:latest`, and never
   any tag that outlives the run, in a verification path. A tag unique
   to the run keeps concurrent runs and concurrent workspaces from
   colliding; building and verifying inside one run makes staleness
   unrepresentable.
