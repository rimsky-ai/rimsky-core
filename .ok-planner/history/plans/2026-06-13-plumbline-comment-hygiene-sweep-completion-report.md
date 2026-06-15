# Plumbline comment-hygiene sweep — completion report

**Spec:** `.ok-planner/specs/2026-06-13-plumbline-comment-hygiene-sweep-design.md`
**Plan:** `.ok-planner/plans/2026-06-13-plumbline-comment-hygiene-sweep.md`

## 1. Proof walkthrough

### STORY-clean-lint — codebase passes Plumbline's full enforcement with every check active

The rimsky maintainer can verify that the codebase passes Plumbline's full enforcement and that the project's Plumbline configuration shows every check active, so `decision:coding-style` accurately describes the active configuration.

- **Artifact path:** `test/plumbline/clean_test.go` (with `test/plumbline/doc.go` carrying the package GoDoc + `@story: clean-lint` annotation).
- **What it exhibits:** the test resolves the Plumbline lint binary path (preferring `PLUMBLINE_BIN`, falling back to `$CLAUDE_PLUGIN_ROOT/bin/plumbline`), walks up from the test-file location to locate the directory containing `.plumbline.json`, parses the config and asserts that `source_validity`, `blessed_invariant_test_coverage`, and `comment_hygiene` are all present and `true`, then shells out to `node <bin> .` from the repo root and asserts the lint subprocess exits with code 0. The exit-code-only contract avoids brittle substring matching on Plumbline's violation-line format. On non-zero exit, the test fails with the first ~2,000 bytes of combined stdout/stderr for diagnosis.
- **Invocation:** `go test ./test/plumbline/... -count=1`
- **Status:** EXHIBITS WORKING — invocation under the current tree returns `ok github.com/rimsky-ai/rimsky-core/test/plumbline 0.275s`. The committed `.plumbline.json` shows `"comment_hygiene": true`, `"source_validity": true`, `"blessed_invariant_test_coverage": true`, so both branches of the assertion (config-level and lint-level) succeed against the post-work tree.

## 2. Technical decisions kept

### TD-comment-hygiene-uniform-rule — tag-or-delete per site with the Plumbline tag vocabulary

Every comment-hygiene violation outside the doc-residue cluster is resolved by tag-or-delete using `@constraint:`, `@deliberate:`, `@agent-contract`, or the project-extended `@concept:` / `@story:` / `@decision:` tags.

- Cited at `.ok-planner/design/decisions/comment-hygiene-uniform-rule.md:8-10` (Choice) — body matches the spec's design-change directive verbatim, with the doc-residue override callout to `decision:doc-residue-reshape-pass`.
- Implementation surface: the 13,987 / 15,794 line churn across 969 files in `git diff --cached --stat` (Go files under `cmd/`, `lib/control/`, `lib/foundation/`, `lib/graph/`, `lib/runtime/`, `lib/services/`, `lib/protocols/`, `examples/`, `test/`, `tools/`, plus the TS subtree under `lib/services/executors/claude-agent/`) reflects the per-site sweep; the proof's clean lint exit confirms every site landed on either a tagged comment or a deletion.

### TD-mechanical-cluster-sweep — divider / commented-out-code / todo-marker / license-fragment-mis-clustered in one up-front pass

The four small mechanical clusters (~174 sites) are handled in their own pass with per-cluster defaults: divider → delete; commented-out-code → delete; todo-marker → delete; license-fragment → resolve per the uniform rule (shape-misclassified prose in `tools/license-check/headers_test.go`).

- Cited at `.ok-planner/design/decisions/mechanical-cluster-sweep.md:8-10` (Choice) — body matches the spec's design-change directive.
- The four `// TODO(host-agent-proxy v2):` markers in `lib/runtime/peer/{dial.go, publisher_client.go, validation_client.go, data_processing_client.go}` are gone from the post-work tree (the diff records the per-file modifications); the deferral record remains in `history:plans/2026-05-24-host-agent-and-proxy-divergences.md`.

### TD-doc-residue-reshape-pass — doc-residue sites get a dedicated reshape pass with GoDoc/JSDoc-position-first rule

The 849 doc-residue sites are processed with a reshape-first rule: GoDoc-position sites are rewritten so the first word names the declaration on the next non-comment line; non-doc-position sites fall through to the uniform rule. Same shape for JSDoc-above-export-decl in TS/JS.

- Cited at `.ok-planner/design/decisions/doc-residue-reshape-pass.md:8-10` (Choice) — body matches the spec verbatim.
- Implementation surface: visible across the changed Go files under `cmd/rimsky/cli/`, `lib/control/`, etc., where package-level decl comments were reshaped into GoDoc form. The proof's clean exit confirms the cluster reached zero.

### TD-untagged-prose-by-module — one pass per top-level module root

The ~5,787 untagged-prose sites decompose into one pass per top-level module root (`cmd/`, `lib/foundation/`, `lib/graph/`, `lib/runtime/`, `lib/control/`, `lib/services/`, `lib/protocols/`, `examples/`, `test/`, `tools/`, plus a `.claude/` / `dockerfiles/` catch-all).

- Cited at `.ok-planner/design/decisions/untagged-prose-by-module.md:8-10` (Choice) — body matches the spec verbatim, cross-referenced to `concept:module-layout`.
- Implementation surface: the 969-file diff spans every named module root, consistent with the per-module decomposition; the proof's clean exit confirms the aggregate untagged-prose cluster count reached zero.

### TD-config-flip — `.plumbline.json`'s `comment_hygiene` check flipped to `true` in the final pass

Check activation follows clean state, not preceding it.

- Cited at `.ok-planner/design/decisions/config-flip.md:8-10` (Choice) — body matches the spec verbatim.
- Implementation surface: `.plumbline.json` in the post-work tree carries `"comment_hygiene": true`, alongside the previously-active `source_validity` and `blessed_invariant_test_coverage`. The `decisions/coding-style.md` Choice section was rewritten in the same pass to read in current-state form (no "previously disabled" hedging), per the spec's mutation directive.

## 3. Technical decisions diverged

None. All five technical decisions in the spec's `## Manifest` were carried out in the shape the spec dictated; all seven design changes in the spec's `## Manifest` (one story create, one decision mutation, five decision creates) landed verbatim. No necessity-rule additions were required.

## Coverage check

- **Stories exhibited:** 1 / 1 (STORY-clean-lint).
- **Technical decisions:** 5 kept + 0 diverged = 5 / 5 total.
- **Design changes (manifest):** 7 / 7 landed —
  - `Story:create:clean-lint` → `.ok-planner/design/stories/clean-lint.md` present.
  - `Decision:mutate:coding-style` → `.ok-planner/design/decisions/coding-style.md` Choice replaced per spec.
  - `Decision:create:comment-hygiene-uniform-rule` → present.
  - `Decision:create:mechanical-cluster-sweep` → present.
  - `Decision:create:doc-residue-reshape-pass` → present.
  - `Decision:create:untagged-prose-by-module` → present.
  - `Decision:create:config-flip` → present.

No mismatch. No defects flagged.
