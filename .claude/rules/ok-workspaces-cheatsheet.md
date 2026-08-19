# ok-workspaces Cheatsheet

Materialized by ok-workspaces v18.8.0 — suite-owned; refreshed by
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

3. **Content-addressed artifacts.** Build outputs used for verification
   are tagged by source-tree hash: `tools/image-src-tag.sh` prints
   `src-<12 hex>` — a git tree-object hash of the project root's
   subtree, including uncommitted changes. The project root is the
   nearest ancestor carrying a suite estate marker, so an estate nested
   in a larger repository tags only its own subtree. Same tree → same
   tag, on every machine. Tests and harnesses resolve artifacts by that
   tag and fail loudly when it is missing. Never `:latest` or any
   mutable tag in a verification path — staleness must be
   unrepresentable, not avoided.
