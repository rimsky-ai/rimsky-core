---
audit: persistence-driver
artifact: decision:persistence-driver
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:26:42Z
---

# Whether the ephemeral run uses the SQLite adapter against a per-run state-database file, with no in-memory variant

Supported. The synthetic configuration the CLI writes for a run names the SQLite driver and points it at a state-database file inside the run directory the artifact-root walk creates, with the blob store as a sibling directory in the same run directory; a round-trip test loads the written configuration back and asserts both the driver string and the exact file path. The "no in-memory variant" half is the stronger check, and it holds two ways: the driver switch admits exactly two names, postgres and sqlite, and nothing in any of the four modules registers or opens a third; and the SQLite configuration validator requires an absolute filesystem path, which rejects the in-memory DSN forms outright — a rejection the validation table test covers alongside the other malformed configurations. Searched the whole tree for in-memory SQLite DSN spellings and found none.
