---
audit: untagged-prose-by-module
artifact: decision:untagged-prose-by-module
determination: supported
commit: b767a27d
audited: 2026-08-02T09:44:16Z
---

# Untagged-prose comment-hygiene cleanup decomposed one pass per top-level module root

Supported. The archived sprint `.ok-planner/history/sprints/2026-06-13-plumbline-comment-hygiene-sweep-design.md`
records TD-untagged-prose-by-module's choice verbatim (one pass per
`cmd/`, `lib/foundation/`, `lib/graph/`, `lib/runtime/`, `lib/control/`,
`lib/services/`, `lib/protocols/`, `examples/`, `test/`, `tools/`, plus a
`.claude/`/`dockerfiles/` catch-all — the module-root axis `concept:module-layout`
also uses), and its completion report records the ~5,787-site sweep landing
in a single commit (`61e3b3b4`) whose diff spans every one of those module
roots. The claim is that the axis, not a fixed-size bucketing, was used —
matching `concept:module-layout`'s five-module / four-layer split, which the
project's own import-boundary depguard rules in `.golangci.yml` also key on.
Present-day evidence the work landed and stayed landed: running the vendored
`plumbline` binary against the repo root exits 0 (no untagged-prose
violations remain), and `test/plumbline/clean_test.go` asserts the
lint's checks stay active.
