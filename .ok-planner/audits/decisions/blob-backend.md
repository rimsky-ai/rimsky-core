---
audit: blob-backend
artifact: decision:blob-backend
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:44:00Z
---

# The filesystem blob backend at the default spill threshold read against the CLI's generated self-host configuration

Supported. The configuration the CLI generates for a self-hosted run selects the filesystem blob backend and roots it at the blob directory inside that run's own artifact directory, leaving the spill threshold unset so the default applies; the same generator serves both self-hosting entry points. The persistence layer's spill rule keeps a value inline when it is at or below the threshold and spills only when it exceeds it, and the filesystem backend writes each spilled value as a plain file under its root, refusing any derived path that escapes that root. The three rejected alternatives all exist as real backends to have been rejected: an inline backend that produces no handles, a memory backend that configuration validation permits only in the single-process topology, and a Postgres large-object backend that is unavailable under the SQLite driver the self-hosted run uses. Tests cover the generated configuration's paths and the backends' round-trip and spill behavior.
