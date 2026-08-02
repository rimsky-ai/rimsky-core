---
audit: migrations-no-compat-shims
artifact: decision:migrations-no-compat-shims
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:39:53Z
---

# Pre-v1 schema rethinks drop and recreate rather than threading a compat shim

Supported. Read all 39 postgres migration files (the sqlite tree mirrors them plus one sqlite-only file) as the population and found five outright "retire" migrations (`019-retire-terminate-after-run.sql`, `020-retire-attribute-override-match-counter.sql`, `021-retire-frame-state-column.sql`, `024-retire-frame-timeout.sql`, `025-retire-park-reason-and-watchdog.sql`) that drop the superseded column/constraint directly — `021`'s sqlite sibling does the required table-rebuild dance (`CREATE …_new`, copy, `DROP`, `RENAME`) and drops the old column outright rather than keeping it alongside a new one. A repo-wide grep across both migration directories for `shim`, `deprecated`, `_legacy`, and backward-compat phrasing returned no hits.
