---
audit: exposure-no-config
artifact: decision:exposure-no-config
determination: supported
commit: b767a27d
audited: 2026-08-02T09:35:29Z
---

# One-shot (compose run) mode has no config-surface toggle

Supported. `rimsky compose run` (`RunComposeRun` in `cmd/rimsky/cli/compose/run.go`, carrying this decision's tag) is the only entry point into the embedded/one-shot stack. The `rimsky.yml` schema parsed by `LoadRimskyConfigYAML` restricts `persistence.driver` to `postgres`/`sqlite` (`lib/foundation/persistence/open.go` errors "unknown driver" on anything else) and carries no `embedded`/`one-shot` value or any other field selecting the mode; a repo-wide search for "embedded" near the config/persistence packages found no such knob outside the migrator's unrelated embedded-filesystem error string.
