# `pg-largeobject` blob backend

**Multi-process safe:** yes (state lives in the same Postgres
instance every rimsky process already talks to).

## What it is

Stores blobs in `pg_largeobject` via the libpq `lo_*` API (pgx's
`LargeObjects.Create / Open / Unlink`). Handles formatted as
`pglo:<oid>`. Each LO operation runs in its own pgx transaction.

## When to use

- Multi-process Postgres-backed deployments where you don't want to
  manage a shared filesystem volume.
- Payloads in the 100 KB – 1 GB range.

## Configuration

```yaml
persistence:
  blob:
    backend: pg-largeobject
    spill_threshold_bytes: 65536    # default; bytes above this spill
    pg_large_object: {}             # no fields required
    retention:
      orphan_sweep_interval: 1h
      retention_after_unreferenced: 24h
```

## Operational notes

- LOBs are tied to the same database the rimsky tables live in. If
  you split rimsky_* and the LOB storage across databases, handles
  break.
- Vacuum: standard Postgres vacuum applies; LOBs are reaped when
  `lo_unlink` runs, which the orphan-sweep does on
  `rimsky_blob_orphans` rows.
- Performance: chunked reads/writes through pgx — comparable to
  `filesystem` for sequential access; range reads use `lo_lseek` and
  are efficient.

## Conformance

```sh
go run ./cmd/rimsky-blob-backend-conformance \
  --backend pg-largeobject \
  --pg-conn-string "postgres://user:pass@host:5432/db"
```
