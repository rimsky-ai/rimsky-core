---
decision: sqlite-modernc-pure-go
status: as-is
---

# SQLite driver is modernc.org/sqlite, pure Go

## Choice

SQLite persistence uses `modernc.org/sqlite`, a pure-Go driver requiring no CGO.

## Rationale

A CGO-free build keeps cross-compilation and static container images simple: one Go toolchain, no C dependency chain, on every platform the binaries ship to.

## Alternatives

- `mattn/go-sqlite3`, the CGO-based incumbent — rejected: CGO complicates cross-compilation and static builds for no capability rimsky needs.
