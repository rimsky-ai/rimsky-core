# filesystem claim-producer

The `filesystem` reference claim-producer offers concrete-paths-only
access to a shared volume. Claims pin a single file or directory by
its absolute path; the platform's byte-equal scope predicate ensures
that two claims naming the same path conflict.

## When to use it

- Workloads that need exclusive write access to specific files or
  trees on a shared volume (NFS, EBS, CSI volume, local disk on a
  single-host deploy).
- A simple primitive for graphs whose unit of work is "produce or
  modify this file."

The producer is **concrete-paths-only**: glob patterns, parent-dir
locks, and "lock the whole subtree" semantics are not supported.
Operators that need richer scoping should implement a custom producer.

## Configuration

```yaml
claim_producers:
  workspace_files:
    kind: filesystem
    endpoint: dns:fs-producer.svc.cluster.local:9001
    write_semantics_allowed: [exclusive]
    config:
      root: /var/lib/rimsky/workspace
```

A claim's address (returned by `Open`) is a JSON object with the
absolute path. The executor uses `cwd_from_store: <alias>` to chdir
into the held path before running.

## Scope canonicalization

Scopes are byte-compared. The producer normalizes paths to absolute
canonical form (`filepath.Clean` plus root-relative resolution)
before returning. Two claims whose path resolves to the same
canonical form will conflict; two claims on different paths will not
conflict even if the paths are siblings or share a parent.

## Build and test

```sh
go build ./stores/filesystem
go test ./stores/filesystem/...
```

## Operating

The producer is stateless on its own; the only persistent state is
the held-claim metadata in rimsky's `rimsky_claim_handles`. The
producer enforces TTL on its in-memory claim registry and exposes
the standard `Capabilities()` handshake for the rimsky-side client to
populate the per-producer metadata cache.
