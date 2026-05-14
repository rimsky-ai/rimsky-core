# postgres claim-producer

The `postgres` reference claim-producer exposes regional access semantics
and items-table queue semantics over the gRPC ClaimProducer protocol.
It runs as a standalone binary and is dialed from rimsky over the wire.

## When to use it

- A graph wants exclusive write access to a Postgres row, table, or
  schema for the duration of a node's run.
- Multiple supervisors need to coordinate writes to a shared Postgres
  resource through rimsky's contention safety.

For other producers, see `docs/stores/filesystem/` (concrete-paths) and
`docs/stores/stub/` (in-memory test fixture). Custom producers
implement `protocols/proto/v1/claim_producer.proto`.

## Configuration

Operator-side config in `rimsky.yml`:

```yaml
claim_producers:
  analytics_production:
    kind: postgres
    endpoint: dns:postgres-producer.svc.cluster.local:9000
    write_semantics_allowed: [exclusive]
    config:
      dsn: "postgres://rimsky:secret@db:5432/analytics"
      schema: production
      mode: items_table
```

The `mode` knob selects between two flavors:
- `regional` — claims wrap a SELECT FOR UPDATE on a specific row or
  range; the address points to the locked rows.
- `items_table` — claims emulate a classic items-table queue; commit
  marks the row processed, abandon returns it to the queue.

## Wire shape

The producer's `Capabilities()` advertises:

```
write_semantics: [exclusive]
modes: [regional, items_table]
```

`Open` returns an opaque `address` and `payload`; rimsky treats both
as inert (`@blessed-invariant 20`).

## Operating

Run the binary as a service. It connects to the operator's
Postgres via the configured DSN. Producer-internal state lives in a
small set of tables (`rimsky_pg_claim_state` and similar) that the
producer manages; rimsky's `rimsky_claim_handles` is the sole
authority for lock state on rimsky's side.

Per-claim TTL and orphan sweeps run inside the producer; the rimsky-
side orphan-claim reaper handles abandoned `rimsky_claim_handles` rows
in coordination with `Release` calls on the producer.
