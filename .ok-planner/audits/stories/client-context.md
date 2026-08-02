---
audit: client-context
artifact: story:client-context
determination: supported
commit: b767a27d
audited: 2026-08-02T09:38:14Z
---

# CLI multi-endpoint context management

Supported. `cmd/rimsky/cli/context.go` implements `ctx add` (register a named endpoint), `ctx use` (switch the current context by name), `ctx list` (inspect all registered contexts, table or redacted-JSON form, marking the current one), `ctx current` (show the active context), and `ctx rm` (remove a context, refusing to remove the current one); `cmd/rimsky/cli/endpoint.go` resolves the effective endpoint for ordinary commands by falling back through flag, env, manifest pin, and finally the current named context, matching the story's "switch between them ... run commands against several deployments without flag plumbing." All five subcommands (add, use, list, current, rm) have direct unit tests in `context_test.go` covering creation, duplicate rejection, switching, current-context refusal on removal, non-current removal, empty listing, and API-key redaction in both table and JSON output.
