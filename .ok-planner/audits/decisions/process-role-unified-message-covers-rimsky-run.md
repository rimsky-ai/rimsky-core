---
audit: process-role-unified-message-covers-rimsky-run
artifact: decision:process-role-unified-message-covers-rimsky-run
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:33:37Z
---

# Blob-config error names three deployment setters, leaves conformance unnamed

Supported. `lib/foundation/persistence/blob_config.go`'s memory-backend error text (behind `@decision: process-role-unified-message-covers-rimsky-run`) names exactly the three deployment paths the decision specifies — "rimsky-entrypoint's no-command all-in-one path, ... rimsky compose run, and ... rimsky run in self-host mode" — matching the actual `RIMSKY_PROCESS_ROLE=unified` setters found by grep across the tree: `cmd/rimsky-entrypoint/main.go` (no-arg path), `cmd/rimsky/cli/compose/run.go` (`RunComposeRun`), and `cmd/rimsky/cli/compose/template_run.go` (`runTemplateSelfHost`). The fourth setter, `cmd/rimsky/conformance_blob_backend.go`'s `openBlobBackend("memory", ...)` path, does set the same env var but is absent from the error text, exactly as the decision specifies it should be left unnamed.
