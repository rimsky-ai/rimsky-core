# Rimsky quickstart

The first 60 seconds of using Rimsky. Brings up a working orchestrator with a stub claim-producer, a stub executor, and the read-only dashboard. SQLite-backed; runs entirely from this directory.

## Prerequisites

- Docker (24+) with Docker Compose v2.
- About 2 GB of free disk for the first build (Go + Node compilation). Subsequent runs are cached.

## 1. Bring it up

```sh
docker compose up
```

First build takes a few minutes (Rimsky's Go binaries + the dashboard's Node bundle). When you see `rimsky-1 | ...listening on :8080` and the dashboard log line, it's ready.

In another terminal, verify:

```sh
./rimsky health
# → ok
```

The dashboard is at <http://localhost:8090>. The control API is at <http://localhost:8080>.

## 2. Register and run a template

The included `example-template.yml` is a two-node cascade: `items.fetch → items.classify`. Both nodes use the bundled stub executor.

```sh
./rimsky template register example-template.yml
# → template_hash=sha256-... tags=

./rimsky template deploy sha256-...
# → deployed

./rimsky instance create sha256-...
# → instance_id=01H...
```

Watch in the dashboard or via the CLI:

```sh
./rimsky instance get 01H...
```

Both nodes settle into `fresh`; the frame transitions to `resolved`. The dashboard's instance graph view shows the dependency edge and the node-by-node states.

## 3. Tear down

```sh
docker compose down
```

This preserves the SQLite state (in the `rimsky-state` named volume) so the next `up` resumes where you left off. To wipe state too:

```sh
docker compose down -v
```

## What's running

| Service | Image | Purpose |
|---|---|---|
| `rimsky` | `rimsky/all` | scheduler + supervisor + control-api + migrate, all under one entrypoint; SQLite state in a named volume |
| `store-stub` | `rimsky/store-stub` | bundled stub claim-producer (in-memory; deterministic) |
| `executor-stub` | `rimsky/executor-stub` | bundled stub executor (every Execute returns Complete) |
| `dashboard` | `rimsky/dashboard` | read-only UI on :8090, dials the control-api |

The `rimsky.yml` here wires them together. To bring your own claim producer or executor, point the `claim_producers:` / `executors:` blocks at your service and rebuild the compose stack.

## Common variations

### Skip the dashboard

`docker-compose.minimal.yml` brings up just the orchestrator + stub services. Use it when you only need to script against the control-api:

```sh
docker compose -f docker-compose.minimal.yml up
```

The `./rimsky` wrapper auto-detects which compose file is up via the `RIMSKY_COMPOSE_FILE` env var:

```sh
export RIMSKY_COMPOSE_FILE=$PWD/docker-compose.minimal.yml
./rimsky health
```

### Override host ports if 8080 / 8090 are in use

Default port mappings are `8080:8080` (control-api) and `8090:8090` (dashboard). If you have other services on those ports (Hasura, another local API, etc.), override:

```sh
RIMSKY_HOST_PORT=18080 RIMSKY_DASHBOARD_HOST_PORT=18090 docker compose up
```

When you override `RIMSKY_HOST_PORT`, also tell the wrapper how to reach the published port:

```sh
RIMSKY_HOST_PORT=18080 ./rimsky health
```

(The wrapper's `docker compose exec` path doesn't care about the host port, but native `rimsky` invocations and any `curl http://localhost:8080/...` examples in this README do — substitute your override.)

### Inspect the SQLite state

State lives in a SQLite db inside the rimsky container. Inspect with:

```sh
docker compose exec rimsky sqlite3 /var/lib/rimsky/state.db
```

By default the state lives in a Docker named volume. Override with `RIMSKY_STATE_DIR=./state docker compose up` to bind-mount a host path.

### Skip the wrapper, install the CLI natively

The `./rimsky` wrapper invokes the CLI inside the rimsky container — convenient (zero install) but pays a `docker compose exec` overhead per command (~500ms-1s). For native-speed CLI:

```sh
go install github.com/fallguyconsulting/rimsky/cmd/rimsky@latest
export RIMSKY_CONTROL_API=http://localhost:8080
rimsky health
```

(Requires Go 1.25+. Pre-built binaries are not yet published.)

## What this doesn't include

- Real persistence. SQLite is dev-only; multi-host deployments need the postgres driver. See `deploy/docker-compose.yml` for the multi-process production-shape stack.
- Real executors. The stub returns canned `Complete` events keyed only on `node_type`. To run actual work, use `rimsky/executor-http-node` (calls an HTTP endpoint) or `rimsky/executor-claude-agent` (calls Anthropic's API), or write your own — see [`docs/protocols/executor.md`](../docs/protocols/executor.md).
- Authentication. Rimsky has no built-in auth — the v1 deployment model assumes network-perimeter isolation. Don't expose port 8080 to untrusted networks.

## Next steps

- Browse the public concept reference under [`docs/concepts/`](../docs/concepts/) — one file per Rimsky noun.
- Read [`docs/humans/concepts.md`](../docs/humans/concepts.md) for a narrative walk through the full vocabulary in learning order.
- Copy-pasteable examples for richer scenarios live under [`docs/agents/examples/`](../docs/agents/examples/).
- Implementing a custom claim producer, executor, or lifecycle subscriber: [`docs/protocols/`](../docs/protocols/).
