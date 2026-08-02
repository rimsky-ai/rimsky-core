---
audit: release-semver-from-diff
artifact: decision:release-semver-from-diff
determination: supported
commit: b767a27d
audited: 2026-08-02T09:40:22Z
---

# The formal-release skill's SemVer step is grounded in five named consumer-visible surfaces

Supported. `.claude/skills/release/SKILL.md` step 2 ("Diff inspection and SemVer decision") reads the diff and commit log since the last stable tag and classifies it against exactly the surfaces the decision names: wire protocol (`lib/protocols/proto/v1/*.proto`), persistence schema (new migration files under either backend's `migrations/` directory), operator config flags and defaults, public API (added/removed/renamed exported Go symbols), and environment (`RIMSKY_*` env-var references) — any match triggers a minor bump, absence triggers patch. This is the sole SemVer-decision path in the repo (dev releases, per `decision:release-dev-mechanical`, use a separate mechanical always-next-minor rule with no diff inspection); the skill's own text also states the rule is "best-effort by design" with mismatches surfaced as questions at the single operator gate rather than silently trusted.
