# Plumbline comment-hygiene sweep — Implementation Plan

**Spec:** .ok-planner/specs/2026-06-13-plumbline-comment-hygiene-sweep-design.md
**Goal:** Drive rimsky's 6,810 known comment-hygiene violations to zero, activate the `comment_hygiene` check in `.plumbline.json`, and codify the resulting methodology in the design corpus.
**Architecture:** The work is a serial sequence of passes against Plumbline's lint output, organized by violation shape and then by module-root. Each pass enables `comment_hygiene` temporarily in a config copy (the committed `.plumbline.json` stays at `false` until the final pass) so per-pass verification can run cleanly without blocking intermediate commits. The final pass flips the committed config, lands the proof artifact, and writes the design-doc deltas.
**Tech Stack:** Go (the codebase), TypeScript (the `claude-agent` executor under `lib/services/executors/claude-agent/`), Plumbline lint (delivered via Claude Code plugin; the binary is a Node.js script at `$CLAUDE_PLUGIN_ROOT/bin/plumbline` once the plugin is installed, and is invoked as `node <path> <args>` consistently — both the per-pass verification helpers and the proof artifact in Pass 14 use this invocation form so the `PLUMBLINE_BIN` env var carries a single consistent semantic: the filesystem path to the JS script that `node` wraps).

---

## How each pass verifies (read once, applies to every pass below)

Every pass except Pass 14 runs against a **temporary-config** state — comment-hygiene enabled in a sed-edited copy of `.plumbline.json` — so the committed config can stay at `false` until the work is fully clean. The pattern, identical across passes:

```bash
# Save baseline
cp .plumbline.json /tmp/plumbline-cfg-bak.json
# Enable comment_hygiene temporarily
sed -i '' 's/"comment_hygiene": false/"comment_hygiene": true/' .plumbline.json
# Run the lint or its patterns subcommand
node "${PLUMBLINE_BIN:-$CLAUDE_PLUGIN_ROOT/bin/plumbline}" patterns .
# Restore
cp /tmp/plumbline-cfg-bak.json .plumbline.json
```

`patterns` output names each cluster (`untagged-prose`, `doc-residue`, `commented-out-code`, `divider`, `todo-marker`, `license-fragment`) with a count and sample file paths. The per-pass verification grep-filters the patterns output to confirm the pass's target cluster (or per-module subset) reads zero. The committed `.plumbline.json` MUST remain at `"comment_hygiene": false` at the end of every pass except Pass 14 — if a pass commits a flipped config by accident, the PostToolUse hook will block subsequent passes' edits.

After every pass, run `make build-all` and `make lint` to confirm no Go-level regression. `make test-all` runs once in Pass 14's verification (the full repo-wide suite is reserved for the final acceptance gate; intermediate passes use `go build ./...` and `make lint` for cheap signal).

---

## Pass 1: Mechanical-cluster sweep

**Goal:** Delete the ~174 sites across the four mechanical clusters — `divider`, `commented-out-code`, `todo-marker`, `license-fragment-mis-classified` — so subsequent passes operate on a tree containing only prose-judgment work.
**Scope:** Tasks 1–4
**Falsifier:** Plumbline's `patterns` subcommand (with `comment_hygiene` temporarily enabled) still reports a non-zero count under any of `divider`, `commented-out-code`, `todo-marker`, or `license-fragment` at pass-end.

### Task 1: Delete `divider` cluster sites (~59 sites)

**Files:** Files surfaced by `node "${PLUMBLINE_BIN:-$CLAUDE_PLUGIN_ROOT/bin/plumbline}" patterns .` filtered to cluster `divider` (with comment_hygiene temporarily enabled per the preamble). Notable concentrations: `cmd/rimsky/cli/client.go`, scattered across the tree.

**Steps:**
1. Enable comment-hygiene in a temp config copy (see "How each pass verifies" above) and run Plumbline's `patterns` subcommand. Collect the list of all files containing `divider` cluster hits.
2. For each site, delete the divider line (typically a `// ---------` or `// ===` line). When a divider is part of a paired `// --- caption ---` block, delete all three lines (top divider, caption, bottom divider).
3. Run Plumbline's `patterns` subcommand again with comment-hygiene enabled; confirm the `divider` cluster count reads zero.
4. Run `go build ./...` and confirm no compilation regression.

### Task 2: Delete `commented-out-code` cluster sites (~105 sites)

**Files:** Files surfaced by Plumbline's `patterns` cluster `commented-out-code`. Notable concentrations: `cmd/rimsky-host-agent-proxy/{claim_producer_handler_test.go, executor_handler_test.go}`.

**Steps:**
1. With comment-hygiene enabled temporarily, run Plumbline's `patterns` and collect the file list for `commented-out-code`.
2. For each site, read the commented-out block and delete it (the entire commented region — typically a span of `//` lines containing recognizable code syntax). Commented-out code is unconditional residue per `decision:comment-drift-sweep`.
3. Run Plumbline's `patterns` with comment-hygiene enabled; confirm the `commented-out-code` cluster count reads zero.
4. Run `go build ./...` and confirm no compilation regression.

### Task 3: Delete `todo-marker` cluster sites (4 sites)

**Files:** `lib/runtime/peer/dial.go`, `lib/runtime/peer/publisher_client.go`, `lib/runtime/peer/validation_client.go`, `lib/runtime/peer/data_processing_client.go`.

The four sites carry the identical text:
```
// TODO(host-agent-proxy v2): install ServiceName interceptor here when this protocol gains late-bind support
```

These are spec-optional v1 deferral residue per `history:plans/2026-05-24-host-agent-and-proxy-divergences.md:324`. The deferral record lives in the divergences file; the source markers are forward-looking residue per `decision:coding-style` and Plumbline's "no forward-looking content" stance.

**Steps:**
1. Open each of the four files and delete the single TODO line. Do not modify any other comment or code.
2. Run Plumbline's `patterns` with comment-hygiene enabled; confirm the `todo-marker` cluster count reads zero.
3. Run `go build ./...` and confirm no compilation regression.

### Task 4: Resolve `license-fragment` cluster sites (6 sites)

**Files:** All in `tools/license-check/headers_test.go` (lines 219, 296, 300, 306, plus one or two more — verify via Plumbline's `patterns`).

These six sites are NOT license-text fixtures. They are prose comments narrating test assertions in code that happens to mention SPDX / Copyright nearby (e.g., `// No double-stacked header.` above an assertion that exactly-one-copyright-line holds; `// Body must survive.` above a body-preservation check). The lint's shape heuristic mis-classified them because of proximity to SPDX/Copyright keywords.

Resolve each per TD-comment-hygiene-uniform-rule (treat as untagged-prose):

**Steps:**
1. With comment-hygiene enabled temporarily, run Plumbline's `patterns` and collect the file list for `license-fragment`.
2. For each site, read the comment text and the next 5–10 lines of code. Apply the uniform rule: if the comment encodes a load-bearing why a future reader would otherwise lose (rare for these narration-style sites), tag with `@constraint:` / `@deliberate:` / `@agent-contract` as appropriate; otherwise delete as residue. Most or all of these will delete cleanly because the test assertion below the comment is self-documenting.
3. Run Plumbline's `patterns` with comment-hygiene enabled; confirm the `license-fragment` cluster count reads zero.
4. Run `go build ./...` and confirm no compilation regression.
5. Run `make lint` to confirm no Go-level regression.

---

## Pass 2: Doc-residue reshape

**Goal:** Process the ~849 `doc-residue` cluster sites under TD-doc-residue-reshape-pass — reshape into GoDoc/JSDoc form when the site is in a doc-position, fall through to tag-or-delete otherwise.
**Scope:** Task 5
**Falsifier:** Plumbline's `patterns` subcommand (with `comment_hygiene` temporarily enabled) still reports a non-zero count under `doc-residue` at pass-end.

### Task 5: Reshape doc-residue sites

**Files:** Files surfaced by Plumbline's `patterns` cluster `doc-residue`. The cluster spans most directories of the tree; reshape is the dominant action for package-level decl docs, fall-through to tag-or-delete for inside-function why-comments.

**Steps:**
1. With comment-hygiene enabled temporarily, run Plumbline's `patterns` and collect the full list of `doc-residue` sites.
2. For each site, read the comment and the next non-comment line (the declaration the comment sits above):
   - **GoDoc position** (Go file, next non-comment line begins with `func`, `type`, `const`, `var` — package-level, not inside a function): rewrite the comment so its first word names the declaration on the next non-comment line and the body describes what the symbol IS. The reshaped comment must satisfy Plumbline's GoDoc exemption (first word matches the declaration's name).
   - **JSDoc position** (TS/JS file, next non-comment line begins with `export function`, `export class`, `export const`, `export interface`, `export type`, `export enum`, `function`, `class`, `const`, `let`, `var` at package level): rewrite the comment as a JSDoc block (`/** ... */`) starting with a capitalized word, immediately preceding the declaration. The reshaped block must satisfy Plumbline's JSDoc exemption.
   - **Not in a doc-position** (inside-function `var x := ...`, a divider the cluster heuristic surfaced here, a comment above a non-declaration line): fall through to TD-comment-hygiene-uniform-rule — tag with the appropriate Plumbline tag (`@constraint:`, `@deliberate:`, `@agent-contract`, `@concept:`, `@story:`, `@decision:`) when the comment encodes a load-bearing why, or delete as residue.
3. After processing all sites, run Plumbline's `patterns` with comment-hygiene enabled; confirm the `doc-residue` cluster count reads zero.
4. Run `go build ./...` and `make lint` to confirm no regression. For TS/JS sites: also run `cd lib/services/executors/claude-agent && npm run build && npm test` to confirm the TypeScript executor still builds and its tests pass.

---

## Pass 3: Per-module untagged-prose — `cmd/`

**Goal:** Resolve all `untagged-prose` cluster sites whose file paths lie under `cmd/`, per TD-comment-hygiene-uniform-rule.
**Scope:** Task 6
**Falsifier:** Plumbline's `patterns` subcommand reports any `untagged-prose` site under `cmd/` at pass-end.

### Task 6: Sweep `cmd/`

**Files:** All `.go` files under `cmd/` containing `untagged-prose` cluster hits (surfaced by `patterns` with comment-hygiene enabled, filtered by path prefix `cmd/`).

**Steps:**
1. With comment-hygiene enabled temporarily, run Plumbline's `patterns` and collect the list of `cmd/`-prefixed sites in the `untagged-prose` cluster.
2. For each site, read 5–10 lines of surrounding context and apply TD-comment-hygiene-uniform-rule:
   - Tag with `@constraint:` if the comment names an imperative requirement (a "must," a load-bearing rule the code is enforcing).
   - Tag with `@deliberate:` if the comment explains intentional surprise (a sequencing choice, a race-avoiding ordering, an invariant guard a refactor would otherwise miss).
   - Tag with `@agent-contract` if the comment describes guarantees a caller relies on plus the surface's NOT-handled cases.
   - Tag with `@concept:` / `@story:` / `@decision:` (project-extended tags) if the comment cites the design corpus by slug.
   - Delete otherwise (generation residue — narration, restated-from-code, "handles the case where X" lines that mirror the code below).
3. Run Plumbline's `patterns` filtered to `cmd/`; confirm zero `untagged-prose` sites remain under the module.
4. Run `go build ./cmd/...` and `make lint` to confirm no regression.

---

## Pass 4: Per-module untagged-prose — `lib/foundation/`

**Goal:** Resolve all `untagged-prose` cluster sites whose file paths lie under `lib/foundation/`, per TD-comment-hygiene-uniform-rule.
**Scope:** Task 7
**Falsifier:** Plumbline's `patterns` subcommand reports any `untagged-prose` site under `lib/foundation/` at pass-end.

### Task 7: Sweep `lib/foundation/`

**Files:** All `.go` files under `lib/foundation/` containing `untagged-prose` cluster hits.

**Steps:**
1. With comment-hygiene enabled temporarily, run Plumbline's `patterns` and collect the list of `lib/foundation/`-prefixed sites in the `untagged-prose` cluster.
2. For each site, apply TD-comment-hygiene-uniform-rule (same tag-or-delete logic as Task 6).
3. Run Plumbline's `patterns` filtered to `lib/foundation/`; confirm zero `untagged-prose` sites remain under the module.
4. Run `cd lib/foundation && go build ./... && golangci-lint run` to confirm no module-level regression.

---

## Pass 5: Per-module untagged-prose — `lib/graph/`

**Goal:** Resolve all `untagged-prose` cluster sites whose file paths lie under `lib/graph/`, per TD-comment-hygiene-uniform-rule.
**Scope:** Task 8
**Falsifier:** Plumbline's `patterns` subcommand reports any `untagged-prose` site under `lib/graph/` at pass-end.

### Task 8: Sweep `lib/graph/`

**Files:** All `.go` files under `lib/graph/` containing `untagged-prose` cluster hits.

**Steps:**
1. With comment-hygiene enabled temporarily, run Plumbline's `patterns` and collect the list of `lib/graph/`-prefixed sites in the `untagged-prose` cluster.
2. For each site, apply TD-comment-hygiene-uniform-rule (same tag-or-delete logic as Task 6).
3. Run Plumbline's `patterns` filtered to `lib/graph/`; confirm zero `untagged-prose` sites remain.
4. Run `go build ./lib/graph/...` and `make lint` to confirm no regression.

---

## Pass 6: Per-module untagged-prose — `lib/runtime/`

**Goal:** Resolve all `untagged-prose` cluster sites whose file paths lie under `lib/runtime/`, per TD-comment-hygiene-uniform-rule.
**Scope:** Task 9
**Falsifier:** Plumbline's `patterns` subcommand reports any `untagged-prose` site under `lib/runtime/` at pass-end.

### Task 9: Sweep `lib/runtime/`

**Files:** All `.go` files under `lib/runtime/` containing `untagged-prose` cluster hits. This is expected to be one of the larger modules.

**Steps:**
1. With comment-hygiene enabled temporarily, run Plumbline's `patterns` and collect the list of `lib/runtime/`-prefixed sites in the `untagged-prose` cluster.
2. For each site, apply TD-comment-hygiene-uniform-rule (same tag-or-delete logic as Task 6). Be alert for `@deliberate:` candidates around race-sensitive paths (the queue, scheduler, supervisor surfaces) — these are the canonical examples of load-bearing why-comments worth tagging rather than deleting.
3. Run Plumbline's `patterns` filtered to `lib/runtime/`; confirm zero `untagged-prose` sites remain.
4. Run `go build ./lib/runtime/...` and `make lint` to confirm no regression.

---

## Pass 7: Per-module untagged-prose — `lib/control/`

**Goal:** Resolve all `untagged-prose` cluster sites whose file paths lie under `lib/control/`, per TD-comment-hygiene-uniform-rule.
**Scope:** Task 10
**Falsifier:** Plumbline's `patterns` subcommand reports any `untagged-prose` site under `lib/control/` at pass-end.

### Task 10: Sweep `lib/control/`

**Files:** All `.go` files under `lib/control/` containing `untagged-prose` cluster hits.

**Steps:**
1. With comment-hygiene enabled temporarily, run Plumbline's `patterns` and collect the list of `lib/control/`-prefixed sites in the `untagged-prose` cluster.
2. For each site, apply TD-comment-hygiene-uniform-rule (same tag-or-delete logic as Task 6).
3. Run Plumbline's `patterns` filtered to `lib/control/`; confirm zero `untagged-prose` sites remain.
4. Run `go build ./lib/control/...` and `make lint` to confirm no regression.

---

## Pass 8: Per-module untagged-prose — `lib/services/`

**Goal:** Resolve all `untagged-prose` cluster sites whose file paths lie under `lib/services/` (Go bundled services and the TypeScript `claude-agent` executor subtree), per TD-comment-hygiene-uniform-rule.
**Scope:** Task 11
**Falsifier:** Plumbline's `patterns` subcommand reports any `untagged-prose` site under `lib/services/` at pass-end.

### Task 11: Sweep `lib/services/`

**Files:** All `.go` and `.ts` / `.tsx` / `.js` / `.jsx` files under `lib/services/` containing `untagged-prose` cluster hits. The TS subtree lives under `lib/services/executors/claude-agent/src/`; the build dir `lib/services/executors/claude-agent/dist/` and `node_modules/` are excluded by `.plumbline.json`'s ignore list and are not in scope.

**Steps:**
1. With comment-hygiene enabled temporarily, run Plumbline's `patterns` and collect the list of `lib/services/`-prefixed sites in the `untagged-prose` cluster.
2. For each Go site, apply TD-comment-hygiene-uniform-rule (same tag-or-delete logic as Task 6).
3. For each TS/JS site, apply the same uniform rule — tags work identically in TS comments (`// @constraint:`, `// @deliberate:`, `// @agent-contract`, `// @concept:`, `// @story:`, `// @decision:`). The JSDoc exemption already covers `/** ... */` blocks above declarations.
4. Run Plumbline's `patterns` filtered to `lib/services/`; confirm zero `untagged-prose` sites remain.
5. Run `cd lib/services && go build ./... && golangci-lint run` for the Go module.
6. Run `cd lib/services/executors/claude-agent && npm run build && npm test` to confirm the TypeScript executor still builds and its tests pass.

---

## Pass 9: Per-module untagged-prose — `lib/protocols/`

**Goal:** Resolve all `untagged-prose` cluster sites whose file paths lie under `lib/protocols/` (excluding the already-ignored `lib/protocols/proto/v1/gen/`), per TD-comment-hygiene-uniform-rule.
**Scope:** Task 12
**Falsifier:** Plumbline's `patterns` subcommand reports any `untagged-prose` site under `lib/protocols/` (excluding `proto/v1/gen/`) at pass-end.

### Task 12: Sweep `lib/protocols/`

**Files:** All `.go` files under `lib/protocols/` containing `untagged-prose` cluster hits. The generated directory `lib/protocols/proto/v1/gen/` is already in `.plumbline.json`'s `ignore` list and out of scope.

**Steps:**
1. With comment-hygiene enabled temporarily, run Plumbline's `patterns` and collect the list of `lib/protocols/`-prefixed sites in the `untagged-prose` cluster.
2. For each site, apply TD-comment-hygiene-uniform-rule (same tag-or-delete logic as Task 6). The proto-package handwritten files (everything outside `gen/`) carry interface boundaries — many comments may be `@agent-contract` candidates describing what the protocol promises and explicitly does NOT handle.
3. Run Plumbline's `patterns` filtered to `lib/protocols/`; confirm zero `untagged-prose` sites remain.
4. Run `cd lib/protocols && go build ./... && golangci-lint run` for the Go module.

---

## Pass 10: Per-module untagged-prose — `examples/`

**Goal:** Resolve all `untagged-prose` cluster sites whose file paths lie under `examples/`, per TD-comment-hygiene-uniform-rule.
**Scope:** Task 13
**Falsifier:** Plumbline's `patterns` subcommand reports any `untagged-prose` site under `examples/` at pass-end.

### Task 13: Sweep `examples/`

**Files:** All `.go` files under `examples/` containing `untagged-prose` cluster hits. `examples/` is its own go.work module containing 33+ Go files (reference implementations of all six protocols plus `atomic-staging-fs-producer`).

**Steps:**
1. With comment-hygiene enabled temporarily, run Plumbline's `patterns` and collect the list of `examples/`-prefixed sites in the `untagged-prose` cluster.
2. For each site, apply TD-comment-hygiene-uniform-rule. The examples are reference implementations — many why-comments may be load-bearing pedagogical content. Lean toward tagging (`@agent-contract` is common for example surfaces; `@deliberate:` for showcase choices) rather than deletion when the comment is meaningfully instructive.
3. Run Plumbline's `patterns` filtered to `examples/`; confirm zero `untagged-prose` sites remain.
4. Run `cd examples && go build ./...` to confirm the examples module still builds.

---

## Pass 11: Per-module untagged-prose — `test/`

**Goal:** Resolve all `untagged-prose` cluster sites whose file paths lie under `test/`, per TD-comment-hygiene-uniform-rule.
**Scope:** Task 14
**Falsifier:** Plumbline's `patterns` subcommand reports any `untagged-prose` site under `test/` at pass-end.

### Task 14: Sweep `test/`

**Files:** All `.go` files under `test/` containing `untagged-prose` cluster hits.

**Steps:**
1. With comment-hygiene enabled temporarily, run Plumbline's `patterns` and collect the list of `test/`-prefixed sites in the `untagged-prose` cluster.
2. For each site, apply TD-comment-hygiene-uniform-rule. Test why-comments often deserve `@deliberate:` (explaining the test's setup or a non-obvious arrangement); test narration ("set up the server," "send the request") usually deletes.
3. Run Plumbline's `patterns` filtered to `test/`; confirm zero `untagged-prose` sites remain.
4. Run `go build ./test/...` to confirm test files still compile.

---

## Pass 12: Per-module untagged-prose — `tools/`

**Goal:** Resolve all `untagged-prose` cluster sites whose file paths lie under `tools/`, per TD-comment-hygiene-uniform-rule.
**Scope:** Task 15
**Falsifier:** Plumbline's `patterns` subcommand reports any `untagged-prose` site under `tools/` at pass-end.

### Task 15: Sweep `tools/`

**Files:** All `.go` files under `tools/` containing `untagged-prose` cluster hits.

**Steps:**
1. With comment-hygiene enabled temporarily, run Plumbline's `patterns` and collect the list of `tools/`-prefixed sites in the `untagged-prose` cluster.
2. For each site, apply TD-comment-hygiene-uniform-rule.
3. Run Plumbline's `patterns` filtered to `tools/`; confirm zero `untagged-prose` sites remain.
4. Run `go build ./tools/...` to confirm no regression.

---

## Pass 13: Per-module untagged-prose — top-level catch-all (`.claude/`, `dockerfiles/`)

**Goal:** Resolve all `untagged-prose` cluster sites whose file paths lie under `.claude/`, `dockerfiles/`, or any other top-level location not already swept, per TD-comment-hygiene-uniform-rule.
**Scope:** Task 16
**Falsifier:** Plumbline's `patterns` subcommand reports any remaining `untagged-prose` site anywhere in the tree at pass-end.

### Task 16: Sweep top-level catch-all

**Files:** Files under `.claude/`, `dockerfiles/`, and any other top-level location containing `untagged-prose` cluster hits.

**Steps:**
1. With comment-hygiene enabled temporarily, run Plumbline's `patterns` and collect any remaining `untagged-prose` sites whose file paths do not begin with `cmd/`, `lib/`, `examples/`, `test/`, or `tools/` (i.e., everything previously-swept passes did not touch).
2. For each site, apply TD-comment-hygiene-uniform-rule.
3. Run Plumbline's `patterns` (no path filter) with comment-hygiene enabled. Confirm the aggregate `untagged-prose` cluster count reads zero.
4. Run `make build-all && make lint` to confirm no Go-level regression.

---

## Pass 14: Acceptance — config flip, proof artifact, design-doc deltas (acceptance pass — STORY-clean-lint)

**Goal:** Deliver STORY-clean-lint end-to-end: flip `.plumbline.json`'s `comment_hygiene` check to `true`, author the executable proof artifact, and land all design-doc deltas (one story create, one decision mutation, five decision creates) per the spec's `## Design changes` section.
**Scope:** Tasks 17–25
**Falsifier:** A reader runs Plumbline's lint against the post-pass tree and either sees any violation reported, finds any of the three checks set to `false` in `.plumbline.json`, finds the proof artifact missing or stubbed (no real binary invocation, canned exit code), or finds any design-doc delta missing from the corpus.

### Task 17: Pre-flip verify, then flip `.plumbline.json` to enable comment_hygiene

**Files:** `.plumbline.json`

**Load-bearing sequencing:** the flip must happen AFTER a confirmed-clean state across the whole tree. Once flipped, the PostToolUse hook enforces all three checks on every subsequent Edit/Write; a flip onto a tree with any residual violation would block Task 18's proof-artifact writes and force a difficult mid-pass diagnostic loop. Verify first, then flip.

**Steps:**
1. Run the temp-config full-tree verification: copy `.plumbline.json` to a backup; sed-enable `comment_hygiene` in the working copy; invoke `node "${PLUMBLINE_BIN:-$CLAUDE_PLUGIN_ROOT/bin/plumbline}" .` from the repo root; capture the exit code; restore the backup. The exit code MUST be 0 (clean). If non-zero, halt this pass and re-run Plumbline's `patterns` subcommand to identify the residual cluster — the residual fix belongs in whichever earlier pass owns the file paths surfaced, not in Pass 14.
2. With clean verified, open `.plumbline.json`. Change `"comment_hygiene": false` to `"comment_hygiene": true` in the `checks` block. Leave the other two checks (`source_validity`, `blessed_invariant_test_coverage`) at `true` and the `tags_extend` / `ignore` blocks unchanged.
3. Read the file back and confirm the `checks` block has all three at `true`. The PostToolUse hook is now enforcing all three checks against every subsequent edit in the pass.

### Task 18: Author the executable proof artifact `test/plumbline/clean_test.go`

**Files:** `test/plumbline/clean_test.go` (new), `test/plumbline/doc.go` (new — Go package doc)

**Story:** STORY-clean-lint
**Proof form (from spec):** executable — a script that runs Plumbline's lint against the post-work tree, asserts the lint reports clean, and asserts the project's Plumbline configuration has every check active.

**Load-bearing property:** the proof artifact MUST invoke the real Plumbline lint binary as a subprocess against the rimsky tree, not an in-process construction or canned exit code. A test that asserts a stubbed return defeats the entire pass — STORY-clean-lint exists precisely to exhibit the real lint reporting clean.

**Steps:**
1. Create `test/plumbline/doc.go` with the license header and package doc comment in GoDoc form. Match the convention in `test/smoke/all/smoke_test.go` (license header, blank line, GoDoc package comment whose first word is `Package` followed by the package name). The `@story:` design-citation annotation belongs inside the same GoDoc block, on its own line after a blank `//` separator:

   ```go
   // Copyright © 2026 Fall Guy Consulting.
   // Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
   // license. See LICENSE.agpl and COPYRIGHT at the repo root.

   // Package plumbline holds the executable proof for STORY-clean-lint —
   // the codebase passes Plumbline's full enforcement with every check
   // active.
   //
   // @story: clean-lint
   package plumbline
   ```

2. Create `test/plumbline/clean_test.go` with a test function `TestPlumblineClean` that:
   - **Locates the Plumbline binary.** Read `PLUMBLINE_BIN` from env; if unset, fall back to `$CLAUDE_PLUGIN_ROOT/bin/plumbline`. The env var carries a filesystem path to the JS script that Node wraps — same semantic as the per-pass verification helpers at the top of this plan. If neither path resolves to an existing file, `t.Skip` with a clear message naming both `PLUMBLINE_BIN` and `CLAUDE_PLUGIN_ROOT`.
   - **Locates the repo root.** Walk up from the test file location (use `runtime.Caller` for the test file's path) until reaching a directory containing `.plumbline.json`. That directory is the repo root.
   - **Asserts the config has every check active.** Read `.plumbline.json` from the repo root, parse the `checks` block, assert all three checks (`source_validity`, `blessed_invariant_test_coverage`, `comment_hygiene`) are present and set to `true`. On mismatch, fail with a message naming which check is inactive.
   - **Invokes the real Plumbline binary.** Use `exec.Command("node", binPath, ".")` with the repo root as the working directory. Wrap the binary path in `node` — the binary is a Node.js script (matching the per-pass verification pattern). Capture combined stdout + stderr.
   - **Asserts the exit code is 0.** Plumbline's contract is exit 0 = clean, exit 2 = violations, exit 1 = internal error. The exit code alone is the gating signal — do NOT scan the output for substrings (the violation-line format is not part of Plumbline's stable contract and could regress silently if matched against). On non-zero exit, fail and include the captured combined output (truncated to the first ~2,000 bytes if very long) so the failure is diagnostic without needing to re-run.

3. The required design-citation annotation (`@story: clean-lint`) is already inside the GoDoc package comment block in `doc.go` (step 1). The test file `clean_test.go` does not need its own — the annotation is package-scoped via the doc-comment convention.

4. Run `go test ./test/plumbline/...` with `PLUMBLINE_BIN` set to the local plugin install path (or with the plugin enabled in the active Claude Code session so `CLAUDE_PLUGIN_ROOT` resolves). Confirm the test passes — exit 0, with all three checks reported active in the config and the wrapped `node <bin> .` invocation reporting clean.

### Task 19: Create `design/stories/clean-lint.md`

**Files:** `.ok-planner/design/stories/clean-lint.md` (new)

**Steps:**
1. Create the file with frontmatter `story: clean-lint`, `status: as-is`, and a body that exactly matches the canonical story-file shape from sibling stories in the same directory (see `.ok-planner/design/stories/all-upstream-gating.md` for an example of the section layout). Sections: `## Role`, `## Capability`, `## Business value`, `## Acceptance`, `## Falsifier`, `## Proof`.
2. Populate the sections from the spec's STORY-clean-lint block:
   - **Role**: As a rimsky maintainer
   - **Capability**: I can verify that the codebase passes Plumbline's full enforcement with every check active
   - **Business value**: so that `decision:coding-style` accurately describes the active configuration
   - **Acceptance**: the maintainer runs Plumbline's lint against the post-work tree → the lint reports the codebase clean, and the project's Plumbline configuration shows every check active
   - **Falsifier**: the maintainer runs Plumbline's lint and either sees any violation reported, or finds any check inactive in the project's Plumbline configuration
   - **Proof**: executable — a script that runs Plumbline's lint against the post-work tree, asserts the lint reports clean, and asserts the project's Plumbline configuration has every check active
3. The body MUST be self-contained per `ok-planner:discover-design`'s self-containment rule — no file paths, no `code:`/`pkg:`/`file:`/`history:` citations, no external doc refs. Only methodology vocabulary and slug-form artifact citations.

### Task 20: Mutate `design/decisions/coding-style.md` Choice section

**Files:** `.ok-planner/design/decisions/coding-style.md` (mutate)

**Steps:**
1. Open `.ok-planner/design/decisions/coding-style.md`. Locate the `## Choice` section.
2. Replace the entire current `## Choice` body (both paragraphs — the introduction paragraph AND the "currently disabled because GoDoc" paragraph) with the spec-dictated new Choice text (current-state, path-free):

   > Rimsky's coding methodology is Plumbline, consumed as a Claude Code plugin. The plugin materializes the methodology's per-session cheatsheet into the repo where every contributor and agent reads it; the cheatsheet is committed so contributors without the plugin still see the rules. The lint runs all three checks — `source_validity`, `blessed_invariant_test_coverage`, and `comment_hygiene` — with the comment-hygiene check honoring GoDoc-style and JSDoc-style block exemptions for Go and TS/JS respectively, so canonical doc shapes pass without per-comment tagging. A PostToolUse lint blocks edits that introduce new violations across any check; CI invokes the same lint against the full tree. Project-specific tag-vocabulary extensions configure the plugin to recognize the design-citation tags this project uses (`@concept:`, `@story:`, `@decision:`).

3. Leave the `## Rationale` and `## Alternatives` sections unchanged. Leave the frontmatter (`decision: coding-style`, `status: as-is`) unchanged.
4. Do NOT add a `## Notes` or `## History` section; do NOT write backward-looking phrasing ("previously," "was disabled") into the body. The new body reads as the artifact's current state.

### Task 21: Create `design/decisions/comment-hygiene-uniform-rule.md`

**Files:** `.ok-planner/design/decisions/comment-hygiene-uniform-rule.md` (new)

**Steps:**
1. Create the file with frontmatter `decision: comment-hygiene-uniform-rule`, `status: as-is`, and a title `# Comment-hygiene uniform tag-or-delete rule`.
2. Populate `## Choice` with the spec-dictated body (current-state, path-free):

   > Comment-hygiene violations are resolved by tag-or-delete on a per-site basis. A comment is tagged with `@constraint:`, `@deliberate:`, `@agent-contract`, or one of the project-extended design-citation tags (`@concept:`, `@story:`, `@decision:`) when it encodes a load-bearing why an agent or contributor would otherwise lose. A comment is deleted as residue otherwise. The doc-residue cluster overrides this rule with a reshape-first evaluation per `decision:doc-residue-reshape-pass`.

3. Populate `## Rationale`:

   > Plumbline's thesis is that load-bearing prose must be mechanically distinguishable from generation residue, and the structured-tag vocabulary is the project's existing surface for that distinction. Uniform per-site application keeps the rule simple and the lint enforceable; the cluster taxonomy the lint surfaces is for sampling and prioritization, not for parallel rules.

4. Populate `## Alternatives`:

   > per-cluster bespoke rules (different action vocabulary for divider vs commented-out-code vs prose) — rejected because the cluster heuristic is for grouping by shape, not for licensing different categorical actions; the per-site decision is the same tag-or-delete in every case.

5. Self-containment check: the body contains no file paths, no `code:`/`pkg:`/`file:`/`history:` citations. Only slug-form references (`decision:doc-residue-reshape-pass`) and methodology vocabulary.

### Task 22: Create `design/decisions/mechanical-cluster-sweep.md`

**Files:** `.ok-planner/design/decisions/mechanical-cluster-sweep.md` (new)

**Steps:**
1. Create the file with frontmatter `decision: mechanical-cluster-sweep`, `status: as-is`, and a title `# Mechanical-cluster comment sweep as a dedicated pass`.
2. Populate `## Choice` with the spec-dictated body:

   > The mechanical comment-hygiene clusters — divider, commented-out-code, todo-marker, and license-fragment-mis-classified — are swept in a dedicated pass distinct from the per-site prose-judgment passes. Per-cluster defaults: divider → delete; commented-out-code → delete; todo-marker → delete; license-fragment → resolve per `decision:comment-hygiene-uniform-rule` (the cluster is shape-misclassified — its sites are prose comments rather than license-text fixtures).

3. Populate `## Rationale`:

   > Mechanical work and per-site prose judgment want different validator reviews. Sweeping the mechanical clusters in their own pass leaves every subsequent pass purely about judgment, and the sweep's own validator review collapses to a single shape check.

4. Populate `## Alternatives`:

   > folding the mechanical sites into the per-module prose-judgment passes — rejected because mixing mechanical deletes and per-site judgment in one pass forces the validator to switch review modes mid-pass.

5. Self-containment check: no paths, no code/file/history citations.

### Task 23: Create `design/decisions/doc-residue-reshape-pass.md`

**Files:** `.ok-planner/design/decisions/doc-residue-reshape-pass.md` (new)

**Steps:**
1. Create the file with frontmatter `decision: doc-residue-reshape-pass`, `status: as-is`, and a title `# Doc-residue cluster reshape pass`.
2. Populate `## Choice` with the spec-dictated body:

   > The doc-residue comment-hygiene cluster gets a dedicated pass with a reshape-first per-site rule. When the comment sits directly above a package-level declaration (Go: `func` / `type` / `const` / `var`; TS/JS: `export function` / `export class` / `export const` / etc.), the comment is reshaped so its first word names the declaration on the next non-comment line and the body describes what the symbol IS, satisfying Plumbline's GoDoc / JSDoc exemption. When the comment is not in a doc-position (above an inside-function declaration, a divider that the cluster heuristic surfaced here, or otherwise), the comment is resolved per `decision:comment-hygiene-uniform-rule`.

3. Populate `## Rationale`:

   > The doc-residue cluster is bimodal: roughly half its sites are package-level declaration docs where GoDoc / JSDoc is the conventional shape agents reading the code expect, and the other half are inside-function why-comments where the doc convention doesn't apply. A dedicated pass with reshape evaluated first nudges the package-level half toward the conventional shape without forcing the executor to invent that priority during a tag-or-delete-framed pass.

4. Populate `## Alternatives`:

   > treating doc-residue uniformly under `decision:comment-hygiene-uniform-rule` — rejected because the GoDoc-position half of the cluster genuinely benefits from the conventional shape, and a tag-or-delete framing under-serves that.

5. Self-containment check: no paths, no code/file/history citations; slug-form references only.

### Task 24: Create `design/decisions/untagged-prose-by-module.md`

**Files:** `.ok-planner/design/decisions/untagged-prose-by-module.md` (new)

**Steps:**
1. Create the file with frontmatter `decision: untagged-prose-by-module`, `status: as-is`, and a title `# Untagged-prose sweep decomposes by module root`.
2. Populate `## Choice` with the spec-dictated body:

   > Untagged-prose comment-hygiene violations decompose into one sweep per top-level module root, using the splitting axis described by `concept:module-layout`. Pass count equals module-root count; pass sizing is uneven by design. Within each pass, every site is resolved per `decision:comment-hygiene-uniform-rule`.

3. Populate `## Rationale`:

   > A single sweep over thousands of judgment-only sites is not validator-reviewable. The module-layout axis is the project's existing coherent review boundary — it's the axis the import-boundary rules and multi-module split already use — so per-module passes match how reviewers (human and agent) already navigate the tree.

4. Populate `## Alternatives`:

   > bucketing the violations into fixed-size passes — rejected because module-coherent review surfaces beat uniform-size buckets when the work is per-site judgment.

5. Self-containment check: no paths, no code/file/history citations.

### Task 25: Create `design/decisions/config-flip.md` AND run final verification

**Files:** `.ok-planner/design/decisions/config-flip.md` (new), then full-tree verification across the whole plan's work.

**Steps:**
1. Create `.ok-planner/design/decisions/config-flip.md` with frontmatter `decision: config-flip`, `status: as-is`, and title `# Plumbline check activation follows clean state`.
2. Populate `## Choice` with the spec-dictated body:

   > Activation of an inactive Plumbline check follows clean state, not preceding it. The configuration change that flips a check from inactive to active is committed only after the codebase is already clean against that check.

3. Populate `## Rationale`:

   > The lint blocks edits that introduce new violations of any active check. Activating a check whose backlog is non-zero would block the very edits that would resolve the existing violations. Activating after the backlog reaches zero converges to full enforcement at the moment the codebase actually meets it.

4. Populate `## Alternatives`:

   > activating the check before the sweep — rejected because the edit-blocking PostToolUse lint would prevent the sweep itself.

5. Self-containment check: no paths, no code/file/history citations.

6. Run the full-tree verification suite:
   - `make build-all` — confirms all five Go modules build.
   - `make test-all` — runs the full repo test suite (includes the new `test/plumbline/clean_test.go`).
   - `make lint` — runs `golangci-lint run` across the five modules.
   - Run Plumbline's lint directly: `node "${PLUMBLINE_BIN:-$CLAUDE_PLUGIN_ROOT/bin/plumbline}" .` from the repo root, with the committed `.plumbline.json` (now showing all three checks `true`). Confirm exit code 0 and no `plumbline/` violation lines in the output.
7. Run `cd lib/services/executors/claude-agent && npm run build && npm test` to confirm the TypeScript executor's full check suite still passes.

---

## Manual checks after completion

(none)

The plan's verification is fully automated through `make build-all`, `make test-all`, `make lint`, the TypeScript executor's `npm test`, and the executable proof in `test/plumbline/clean_test.go`. There are no manual / visual / human-judgment checks required for this work.
