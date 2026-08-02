---
audit: sqlite-modernc-pure-go
artifact: decision:sqlite-modernc-pure-go
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:39:53Z
---

# SQLite persistence uses modernc.org/sqlite, pure Go, no CGO

Supported. `go.mod` and `lib/foundation/go.mod` pin `modernc.org/sqlite v1.50.1`, and `lib/foundation/persistence/sqlite/database.go` registers it with `database/sql` via a blank import (`_ "modernc.org/sqlite"`) and opens with `sql.Open("sqlite", …)`. Checked `go.mod`/`go.sum` in both modules for the CGO-based incumbent `mattn/go-sqlite3` — absent from both, direct and indirect.
