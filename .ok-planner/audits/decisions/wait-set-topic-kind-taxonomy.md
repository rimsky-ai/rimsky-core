---
audit: wait-set-topic-kind-taxonomy
artifact: decision:wait-set-topic-kind-taxonomy
determination: supported
commit: b767a27d
audited: 2026-08-02T09:38:14Z
---

# Wait-set `topic_kind` is a 4-value taxonomy: three canonical kinds plus `state` fallback

Supported. Both storage backends' `rimsky_wait_set` table (`lib/foundation/persistence/postgres/migrations/{001,016}-*.sql`, `.../sqlite/migrations/{001,016}-*.sql`) carries a CHECK constraint admitting exactly `'state'`, `'attribute'`, `'transient'`, `'terminal'` — 4 values, checked across both of the 2 backends' current-state migrations. The mapping function `lib/runtime/runner_terminal.go::waitSetTopicKindFor` derives the discriminator from the canonical signal taxonomy's top-level kind (`terminal`, `transient`, `attribute`) and falls back to `state` for anything else, matching the decision's rationale exactly; `runner_terminal_test.go` unit-tests this mapping and explicitly asserts that a `message/...` pattern does NOT produce a `message` topic_kind ("`message` topic_kind retired"), directly proving the rejected 5-value alternative was not taken up.
