---
audit: timestamp-format
artifact: decision:timestamp-format
determination: supported
commit: b767a27d
audited: 2026-08-02T09:35:34Z
---

# Filesystem-safe ISO 8601 timestamp format

Supported. `FormatRunTimestamp` in
`cmd/rimsky/cli/compose/artifact.go` (the only place in the codebase that
formats a `time.Time` for embedding in a file or directory name — checked
across all `.Format(` call sites in the module, all others format DB
column values or fixed-nanosecond persistence timestamps, not path
components) implements exactly `YYYY-MM-DDTHH-MM-SSZ` via the Go layout
`"2006-01-02T15-04-05Z"`, applied to `t.UTC()`. It is the sole timestamp
source for both call sites that build run directories
(`compose/run.go`, `compose/template_run.go`), which append it as a
directory-name prefix. `TestFormatRunTimestamp_FilesystemSafe` asserts
both the absence of colons and a regex match on the exact hyphenated
pattern.
