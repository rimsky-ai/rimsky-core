# Minimal `rimsky.yml`

Save this as `rimsky.yml` and pass `RIMSKY_CONFIG=$PWD/rimsky.yml` to each rimsky binary, or place under `/etc/rimsky/rimsky.yml`. This is the smallest config that brings up a working stack: SQLite persistence (dev only), one named lock, one bundled stub claim producer, one bundled stub executor.

```yaml
persistence:
  driver: sqlite
  sqlite:
    path: /var/lib/rimsky/state.db

claim_producers:
  stub:
    endpoint: "grpc://store-stub:9100"
    protocols: [claim_producer]
    write_semantics_allowed: [sync]

named_locks:
  serial-lane: { limit: 1 }

executors:
  stub:
    transport: grpc
    endpoint: "executor-stub:9090"
    tls: off
    protocols: [executor]
```

## Verification

After `docker compose -f deploy/docker-compose.yml up -d`:

```sh
curl http://localhost:8080/health
```

Expected output: `{"status":"ok"}`.

## Notes

- SQLite is dev-only. Multi-replica / multi-host deployments must use the postgres driver.
- `write_semantics_allowed` must be a subset of what the producer advertises at startup. Misconfiguration fails startup with a `capability_envelope_mismatch` error.
- Real deployments swap SQLite for Postgres and add real producers/executors. The same blocks then look like:

```yaml
persistence:
  driver: postgres
  postgres:
    dsn: "postgres://rimsky:rimsky@postgres:5432/rimsky?sslmode=disable"

claim_producers:
  content:
    endpoint: "grpc://store-filesystem:9100"
    protocols: [claim_producer]
    write_semantics_allowed: [sync]
  topics-ring:
    endpoint: "grpc://store-postgres:9101"
    protocols: [claim_producer]
    write_semantics_allowed: [sync]

named_locks:
  "topics-ring:concurrent-claims": { limit: 5 }
  model-budget:                    { limit: 50 }

executors:
  claude-agent:
    transport: grpc
    endpoint: "claude-agent:9090"
    tls: off
    protocols: [executor]
  http-node:
    transport: grpc
    endpoint: "http-node:9091"
    tls: off
    protocols: [executor]
```
