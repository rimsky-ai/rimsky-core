# Parallel-worktree dev-port allocation — Design Sketch

**Date:** 2026-05-24
**Status:** Sketch (not a spec; not authorization to build)
**To be tackled after:** the repo-reorganization project lands (`spec:2026-05-24-repo-reorganization-design`).

## Idea

Make `docker compose up` on rimsky's reference deployment work cleanly across multiple `git worktree` checkouts at the same time. Today `deploy/docker-compose.yml` hardcodes host-port mappings (15+ entries: control-API `8080:8080`, Postgres `5544:5432`, claim-producer admin `9121:9121`, executor ports `9090/9091/9092/9081/9082/9083/9084/9090/9190/9184/8090`, and the test-fixture variants in `store-postgres.yml` etc.). Two stacks on the same host collide on every one of those ports. Container/network/volume names also collide because compose derives the project name from the working directory's basename, which is identical across worktrees of the same repo.

The proposal: parameterize every host-port binding with a `${VAR:-default}` interpolation, ship a small launch script that picks free ports + sets a unique `COMPOSE_PROJECT_NAME` per worktree, and wrap it in Makefile targets so the operator's interface stays `make dev-up` / `make dev-down`. Each worktree gets a gitignored `.env.local` carrying its assigned ports; restarts within a worktree reuse the same ports for URL stability. Single-worktree workflows continue to work unchanged because the `${VAR:-default}` interpolation falls back to today's hardcoded values when `.env.local` is absent.

## Shape

### File changes (sketch-level enumeration; not exhaustive)

```
deploy/docker-compose.yml           — parameterize every "ports:" host-side binding
deploy/store-postgres.yml           — same (test-fixture variant has its own ports)
deploy/store-filesystem.yml         — same
deploy/rimsky.yml                   — internal-port-only config; no change (services bind container-side 8080 etc.)
deploy/rimsky-all.yml               — same as docker-compose.yml if it carries port mappings
deploy/dev-ports.sh                 — NEW — picks free ports, sets COMPOSE_PROJECT_NAME, writes .env.local
Makefile                            — NEW targets: dev-up / dev-down / dev-ports / dev-logs / dev-status
.gitignore                          — add /.env.local
```

### Parameterization pattern

Existing line:
```yaml
ports:
  - "5544:5432"
```

Becomes:
```yaml
ports:
  - "${POSTGRES_HOST_PORT:-5544}:5432"
```

Container-side port (`:5432`) is unchanged — services inside the compose network keep talking to each other by service name + their internal port. Only the host-side mapping varies. Naming convention for the env var: `<SERVICE>_HOST_PORT` (e.g., `CONTROL_API_HOST_PORT`, `POSTGRES_HOST_PORT`, `HTTP_NODE_HOST_PORT`, etc.). One entry per host-exposed port. The current docker-compose.yml has ~15; expect the same count of env-var slots.

### Launch script (`deploy/dev-ports.sh`)

```bash
#!/usr/bin/env bash
set -euo pipefail

ENV_FILE=".env.local"
PROJECT_BASE="rimsky-$(basename "$(pwd)")"

# Pick a free TCP port: bind(:0) → kernel assigns from the ephemeral range → close.
# The window between close and docker's bind is milliseconds; collision is rare
# (28k+ ports in the ephemeral range). Retry-by-deleting-.env.local handles the
# rare race; not worth coding around inline.
pick_port() {
  python3 - <<'PY'
import socket
s = socket.socket()
s.bind(("", 0))
print(s.getsockname()[1])
s.close()
PY
}

# Reuse on re-run so URLs stay stable across `make dev-down && make dev-up` cycles
# inside a single worktree. Operator deletes .env.local manually if they want fresh ports.
if [[ -f "$ENV_FILE" ]]; then
  echo "Reusing existing ${ENV_FILE}:"
  cat "$ENV_FILE"
  exit 0
fi

cat > "$ENV_FILE" <<EOF
COMPOSE_PROJECT_NAME=${PROJECT_BASE}
CONTROL_API_HOST_PORT=$(pick_port)
POSTGRES_HOST_PORT=$(pick_port)
CLAIM_PRODUCER_ADMIN_HOST_PORT=$(pick_port)
HTTP_NODE_PRIMARY_HOST_PORT=$(pick_port)
HTTP_NODE_SECONDARY_HOST_PORT=$(pick_port)
# ... one row per service exposed to the host (full list during spec phase)
EOF

echo "Wrote ${ENV_FILE}:"
cat "$ENV_FILE"
echo
echo "Use:  docker compose --env-file ${ENV_FILE} -f deploy/docker-compose.yml up -d"
echo "Or:   make dev-up"
```

A bash-only alternative to the python3 line: `python3 -c '...'` could be replaced with a `bash` script reading from `/proc/net/tcp` or shelling out to `ss -tln` and picking a port not in the list, but that's more code and less portable across macOS / Linux. Python3 is a hard dep on the dev box already (pip-installed tools, etc.); keep it.

### Makefile wrappers

```make
.PHONY: dev-ports dev-up dev-down dev-logs dev-status

dev-ports:
	@./deploy/dev-ports.sh

dev-up: dev-ports
	docker compose --env-file .env.local -f deploy/docker-compose.yml up -d
	@echo
	@echo "Stack up. URLs:"
	@grep -E '_HOST_PORT=' .env.local | sed 's/^/  /'

dev-down:
	docker compose --env-file .env.local -f deploy/docker-compose.yml down

dev-logs:
	docker compose --env-file .env.local -f deploy/docker-compose.yml logs -f

dev-status:
	docker compose --env-file .env.local -f deploy/docker-compose.yml ps
```

### Operator workflow

```bash
# Worktree A
git worktree add ../rimsky-debugger -b feat/instance-debugger main
cd ../rimsky-debugger
make dev-up
# → CONTROL_API_HOST_PORT=42137, POSTGRES_HOST_PORT=42138, COMPOSE_PROJECT_NAME=rimsky-rimsky-debugger
curl http://localhost:42137/health
# → ok

# Worktree B, in parallel
git worktree add ../rimsky-reorg -b feat/repo-reorg main
cd ../rimsky-reorg
make dev-up
# → CONTROL_API_HOST_PORT=51209, POSTGRES_HOST_PORT=51210, COMPOSE_PROJECT_NAME=rimsky-rimsky-reorg
curl http://localhost:51209/health
# → ok
```

Both stacks running, no port collision, distinct container / network / volume names. Each worktree's `.env.local` persists across `make dev-down / dev-up` cycles so the URLs stay stable.

### What stays unchanged

- `deploy/rimsky.yml` (the supervisor's YAML config): the supervisor binds container-side `:8080` regardless of host-port mapping. Internal compose-network communication (executor → supervisor callback, control-API → claim-producer, etc.) goes through service names + internal ports, which never change. The `callback.advertise_host` setting (per `file:CLAUDE.md`) keeps pointing at the supervisor's compose service name; no edit needed.
- Single-worktree workflows: if no `.env.local` exists, the `${VAR:-default}` interpolation produces the same compose-up the operator gets today. `make dev-up` without parallel-worktree intent works identically.
- CI: runs one stack at a time, no `.env.local`, defaults apply. No CI change.
- testcontainers-go scenario tests: spin up their own Postgres on random ports already; independent of the deployed compose stack. No change.

### `rimsky` CLI access (optional follow-on)

Operators interacting with one of the two stacks need to know its control-API port. Three options:

- Source `.env.local` in the shell: `set -a; source .env.local; set +a; rimsky --server localhost:$CONTROL_API_HOST_PORT instances list`.
- Pass the port explicitly each time: same as above without the source.
- **Patch the CLI** to auto-read `.env.local` from the current directory when present. Cleanest UX: `cd ../rimsky-debugger && rimsky instances list` Just Works.

The CLI patch is small (10-20 lines in `code:control/cli/`) but not blocking — the source-the-env-file approach works today. List it as a polish item in the spec, not a v1 requirement.

## Open questions

- **Full enumeration of host-exposed ports.** The sketch enumerates 5 example slots; the actual count is ~15 across `deploy/docker-compose.yml`, `deploy/store-postgres.yml`, `deploy/store-filesystem.yml`, `deploy/rimsky-all.yml`. Spec phase walks every `ports:` entry and assigns an env-var name.
- **Should `dev-ports.sh` validate that `python3` is available** and fall back to a bash-only port-picker if not? Pre-v1 dev-tooling convention probably says "require python3 and document it"; not worth dual-stack maintenance.
- **`.env.local` vs `.env`.** Compose looks for `.env` by default. If `.env` is already used by another convention in the repo (check during spec phase), `.env.local` avoids collision. If not, `.env` might be simpler — but `.env` is the conventional name and could be confused with production env files. `.env.local` is the safer pick.
- **Should the script also pin `COMPOSE_PROJECT_NAME` to git branch instead of directory basename?** Branch is more meaningful (`rimsky-feat-instance-debugger` vs `rimsky-rimsky-debugger`). Tradeoff: branch lookup adds `git symbolic-ref HEAD` overhead and breaks in detached-HEAD state. Directory basename is dumber but bulletproof. Probably keep basename, document the choice.
- **Cleanup discipline.** When a worktree is removed (`git worktree remove`), its stack is left running. Should there be a `make dev-purge` that nukes the project's containers / volumes / networks before the worktree gets removed? Probably yes; trivial to add.
- **Conflict with the rimsky-services repo (post-reorg-P3).** rimsky-services has its own deploy fragments and its own compose-up workflow. If a developer iterates across rimsky + rimsky-services in parallel worktrees, both repos want the per-worktree port allocation pattern. The script and Makefile here should be straightforward to copy into rimsky-services; the pattern is repo-agnostic. Worth flagging in the rimsky-services CHANGELOG when this lands; not load-bearing for this sketch.
- **macOS vs Linux ephemeral-port range differences.** Linux defaults to 32768-60999; macOS to 49152-65535. Both have 28k+ ports. No functional difference for this use case. Note in the docstring but don't code around it.

## Risks / unknowns

- **Pick-then-bind race.** Between `pick_port()` returning a free port and `docker compose up` actually binding it, another process could grab the port. Realistic frequency on a dev machine: rare (the window is milliseconds; the ephemeral range is huge). When it happens: compose-up fails with "address already in use," operator deletes `.env.local` and re-runs. Acceptable. If it becomes annoying, the script could retry inline.
- **Operator confusion about which stack they're talking to.** Two stacks running simultaneously, different ports, different project names. If the operator forgets which terminal is which worktree, they might `curl` the wrong stack. Mitigation: `make dev-status` prints the project name + URLs; the `.env.local` lives in the worktree directory so `pwd` is the source of truth.
- **Existing docker-compose.yml may have non-obvious port assumptions.** Some services may use the host port internally (e.g., a callback URL hardcoded to `localhost:8080`). The spec phase needs to grep the deploy fragments and rimsky.yml for any such hardcoded host-port references and route them through env-var substitution too. If callback.advertise_host turns out to need parameterizing (it currently uses a service name, but worth verifying), the script writes that env var too.
- **Stale `.env.local` after a Makefile change.** If `make dev-up` is updated to expose a new port that wasn't in the original `.env.local`, that port falls back to its default and might collide across worktrees. Mitigation: when adding a port, bump a `DEV_PORTS_VERSION` in the script and re-pick all ports if the version changes. Slightly over-engineered for v1; document the manual workflow (delete `.env.local`, re-run) and add the version-bump dance only if it becomes painful.
- **Some operators run `docker compose up` directly without the Makefile wrapper.** The `--env-file .env.local` flag is then missing; the stack comes up with defaults. Symptom: they hit collisions despite the script having run. Mitigation: the Makefile is the supported entry point; document in README. A `direnv`/`.envrc` could auto-source `.env.local` if that's a path the repo wants to take, but that's a separate convention question.

## What this is not

- **Not a production deployment concern.** Production sets ports via its own configuration (Kubernetes Service objects, ingress controllers, etc.). This sketch is dev-machine-local only.
- **Not a Kubernetes concern.** k8s deployments allocate ports via Service objects + ingress; per-worktree-host-port doesn't apply.
- **Not a CI concern.** CI runs one stack at a time on a fresh worker; defaults work; no `.env.local` involved.
- **Not a testcontainers replacement.** Scenario tests under `test/scenarios/...` use testcontainers-go for their own Postgres + Docker setup, independent of the deployed compose stack. Port allocation there is already handled by testcontainers' own random-port mechanism.
- **Not a multi-host concern.** This sketch assumes one developer machine running multiple stacks. Multi-host coordination (e.g., a shared dev cluster) is its own design.
- **Not a substitute for `git worktree remove --force` discipline.** If an operator removes a worktree without taking down its stack, the orphaned containers persist. Cleanup is on the operator (or the optional `make dev-purge` target).
- **Not blocking on `concept:control-api` or any rimsky concept.** This is pure deploy-tooling; no concept-doc impact.
- **Not in scope for the debugger plan or the repo-reorg plan.** Lands separately, after reorg, as a small self-contained change.
