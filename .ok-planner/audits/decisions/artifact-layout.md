---
audit: artifact-layout
artifact: decision:artifact-layout
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:44:00Z
---

# The per-run artifact directory, its pointer entry, and third-party readability read against the CLI's self-host paths

Supported. Both self-hosting entry points — the ephemeral single-template run and the compose one-shot — create a per-run directory under a stable per-root parent named by a filesystem-safe UTC timestamp joined to the run name, collision-suffixed when the same second and name recur, and each run directory holds the run's SQLite state database beside a blob directory created at the same moment. Both paths then install the pointer entry at the parent level as a relative symlink swapped atomically onto the newest run, with staging entries swept and a refusal to overwrite a non-symlink in that slot. Third-party readability holds by construction: the state store is an ordinary SQLite file and spilled values are written as plain files under the blob directory through a temp-write-then-rename, so standard tooling for those two formats opens either without a rimsky-specific reader. Tests in the compose package cover ancestor discovery, collision handling, concurrent claims, blob-directory creation, timestamp safety, and the pointer entry's install, overwrite, concurrent-first-install, and never-broken-for-readers behavior.
