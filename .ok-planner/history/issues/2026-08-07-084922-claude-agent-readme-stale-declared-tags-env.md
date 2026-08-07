---
issue: claude-agent-readme-stale-declared-tags-env
kind: human
category: doc-drift
artifacts:
  - decision:env-var-registry
status: repaired
opened: 2026-08-07T08:49:22Z
github: https://github.com/rimsky-ai/rimsky-core/issues/75
---

# Does the claude-agent README still document RIMSKY_EXECUTOR_DECLARED_TAGS, removed at v0.14.0?

Yes, confirmed on the current tree: `lib/services/executors/claude-agent/README.md`
still listed `RIMSKY_EXECUTOR_DECLARED_TAGS` among the transport/runtime env
knobs. `grep`ing the whole tree for `DeclaredTags`/`DECLARED_TAGS` finds
only this one README line — no `Opts` field, no env read, no registry entry
reference it anywhere in code. The same README was also missing a var that
does exist: `opts.go:111` reads `RIMSKY_EXECUTOR_OBSERVABILITY_HTTP_BRIDGE_URL`,
which is present in `tools/env-registry/registry.md` but absent from the
README's list.

`decision:env-var-registry`'s Choice makes the generated registry (backed
by a fitness test over every live `RIMSKY_*` read site) the enforced source
of truth for what operator env vars exist. `tools/env-registry/registry.md`
lists exactly eight `RIMSKY_EXECUTOR_*` vars mapped to
`lib/services/executors/claude-agent/opts.go` — `CLAUDE_BINARY`, `HOST`,
`OBSERVABILITY_HTTP_BRIDGE_URL`, `PORT_GRPC`, `PORT_HTTP`, `SILENCE_MS`,
`STUB_MODE`, `TOOL_USE_TIMEOUT_MS` — no `DECLARED_TAGS`. The rules leave
exactly one compliant list; no commitment changed.

**Change:** `lib/services/executors/claude-agent/README.md`'s "Operator
configuration (env)" section — removed `RIMSKY_EXECUTOR_DECLARED_TAGS`,
added `RIMSKY_EXECUTOR_OBSERVABILITY_HTTP_BRIDGE_URL`, matching the
registry's eight-entry set exactly.

**Verified:** cross-checked the new list against
`tools/env-registry/registry.md`'s `RIMSKY_EXECUTOR_*` rows and against the
literal `os.Getenv`/`intFromEnv`/`envOr` call sites in
`lib/services/executors/claude-agent/opts.go` — one-for-one match. Docs-only
change; no code touched.
