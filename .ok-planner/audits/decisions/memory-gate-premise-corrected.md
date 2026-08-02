---
audit: memory-gate-premise-corrected
artifact: decision:memory-gate-premise-corrected
determination: supported
commit: b767a27d
audited: 2026-08-02T09:35:29Z
---

# Memory blob backend startup-rejected outside single-process (unified) topology

Supported. `ValidateBlobConfig` in `lib/foundation/persistence/blob_config.go` rejects `Backend == "memory"` whenever `topology.Unified()` is false, and its error text names the single-process mode as the reason, spelling out that all three roles share one in-process map and naming the marker env var/value (`RIMSKY_PROCESS_ROLE=unified`) and its three legitimate setters (`rimsky-entrypoint`'s no-command path, `rimsky compose run`, `rimsky run` self-host). `TestValidateBlobConfig` in `blob_test.go` covers memory with split topology, memory with the zero-value (unset) topology, and memory with unified topology — 3 of its 10 cases — confirming both the split-rejected and unified-accepted paths mechanically. The asymmetry with SQLite is real in code: `SQLiteReplicaWarning` (`topology.go`) only warns, never errors, for `sqlite` outside unified.
