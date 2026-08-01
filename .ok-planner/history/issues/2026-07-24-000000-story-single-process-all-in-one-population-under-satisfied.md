---
issue: story-single-process-all-in-one-population-under-satisfied
kind: audit
category: proof
artifacts:
  - story:single-process-all-in-one
status: repaired
opened: 2026-07-24T00:00:00Z
---

# A "missing" test turned out to exist — the whole issue reduces to one missing tag

Question: does `story:single-process-all-in-one` have real test coverage for the "one process serves all three roles, and a large stored payload one role writes is readable by another" claim, or is that coverage actually missing?

Re-verification confirmed the coverage exists: `lib/services/test/scenarios/single_process_allinone_test.go::TestSingleProcessAllInOne_MemoryBlobAcrossRoles` asserts exactly one `rimsky-entrypoint` process and zero spawned role-child processes (`assertSingleRimskyProcess`), then writes a payload past the configured spill threshold and reads it back through the observability API before and after an orphan-blob sweep — the process-unification and cross-role-blob-read claims, verified live by reading the test body. It simply carried no `@story:` annotation, so the coverage check's grep-based navigation couldn't see it — `@story: single-process-all-in-one` was present on a second test (`bundled_inproc_dispatch_test.go`, the zero-external-services claim) but absent here. Per `{{ANNOTATION-INTEGRITY-RULE}}` / `{{MECHANICAL-VS-JUDGMENT-RULE}}`, a missing annotation on an already-adequate test is the canonical mechanical repair — it changes no commitment, only how existing coverage is discovered.

What changed: added `// @story: single-process-all-in-one` immediately above `TestSingleProcessAllInOne_MemoryBlobAcrossRoles` in `lib/services/test/scenarios/single_process_allinone_test.go`. No `design/` file needed editing — the story's existing text already matched the test's coverage.

How verified: `cd lib/services && go build ./...` clean; the citation-resolution lint (Plumbline's PostToolUse hook) ran on the edit and passed, confirming the slug resolves to the live `design/stories/single-process-all-in-one.md` artifact.
