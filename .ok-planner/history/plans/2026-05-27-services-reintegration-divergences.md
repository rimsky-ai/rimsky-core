# Divergences — 2026-05-27 services reintegration

Audit of the working tree against the plan's literal text. The implementation matches the plan throughout for the core mechanical work (copy, repoint imports, wire into `go.work`, update Makefile / `.golangci.yml` / `licensing.yml` / `.gitignore`, rewrite Dockerfiles, repoint harness images, update the design concept + CLAUDE.md + feature-index). The items below are the meaningful places where the implementer departed from the plan's literal text.

---

## 1. `tools/license-check/headers.go` — bug fixes the plan never named

**What the plan said:** Pass 4 / Task 11 lists `make license-stamp` as a step. Nothing in the plan touches the `tools/license-check/` source. The plan assumes the existing tool is correct.

**What was implemented:** `tools/license-check/headers.go` gained three behavior changes (`+47 / -8` lines):
1. `verifyHeaders` now treats a file carrying both Apache and AGPL markers as a violation (new short-circuit emitting `"contradictory license markers (both Apache and AGPL) — run \`make license-stamp\`"`).
2. `stampHeaders` treats a mixed-markers file as not-correct so it re-stamps and cleans the contradictory header.
3. `stripLeadingHeader` now tolerates a single blank line between header-marker runs, so a boilerplate block followed by a blank then a stale `SPDX-License-Identifier:` line is stripped in one pass (previously only the first marker run was stripped). `SPDX-License-Identifier` was also added to `licenseHeaderMarkers`.

The implementer wrote a code comment that explains this: "what a prior buggy strip pass would leave behind."

**Inferred reason:** Fix-forward under the "Fix Every Bug You Find" rule. The implementer discovered, while running `make license-stamp` per Task 11, that the existing stamp/strip code couldn't clean up the partial Apache strips left behind by the 2026-05-27 root-folder reorg. Two files in the prior reorg (`test/support/testpg/testpg.go` and `test/support/testpg/testpg_test.go`) carried an AGPL boilerplate block plus a stale `SPDX-License-Identifier: Apache-2.0` line; the old stripper bailed at the first non-marker line and left the SPDX line in place. The two file edits in this commit (`test/support/testpg/testpg.go` and `…/testpg_test.go`, each removing the stale `SPDX-License-Identifier: Apache-2.0` line — see item 2 below) are the result of running the fixed stamper. Calling out this fix-forward against an unrelated reorg side-effect is exactly what the project rule asks for.

This confirms orchestrator note #2.

---

## 2. Two unrelated files modified outside `lib/services/` — `test/support/testpg/testpg.go` and `…/testpg_test.go`

**What the plan said:** The plan's Files lists for Task 11 are `licensing.yml` plus "every Apache-headed Go file under `lib/services/` outside the claude-agent subtree (~87 files)" plus `lib/services/internal/ops/ops.go`. Nothing under `test/support/` is in scope.

**What was implemented:** Both files had their stale second-header line `// SPDX-License-Identifier: Apache-2.0` removed (the boilerplate "Copyright … Dual-licensed under AGPL-3.0-or-later …" block above it is unchanged).

**Inferred reason:** A direct consequence of the headers.go fixes in item 1. Once the stamper learned to detect mixed Apache/AGPL markers and to walk across a blank line between marker runs, `make license-stamp` correctly re-stamped these AGPL-classified files (longest-prefix match to `agpl: test/`), dropping the stale Apache SPDX line. Plan-intent compliant: the plan requires `make license-lint` to pass at the end of Task 11, which would not have passed with the fixed verifier finding contradictory headers in these two files.

---

## 3. `feature-index.md` — renamed the prior section and removed two now-empty subsections, beyond Task 21's literal "add a new section"

**What the plan said (Task 21):** "Add a new section `## Bundled services (lib/services/)` with a short intro … and a table with one row per service group …"

**What was implemented:** The new section was added as specified. Beyond that:
- The existing section header `## Bundled service reference impls` was renamed to `## Bundled service test-infra carve-outs`, and its intro prose was rewritten ("Production-side bundled implementations are not part of this repo. Only test-infrastructure carve-outs …" → "Test-infrastructure carve-outs and in-rimsky testfixture wrappers that stay in the root module (separate from the production-side services under `lib/services/`).").
- The two subsections `### Sensors` and `### Lifecycle subscribers` (each containing only a "No … reference impls are part of this repo." sentence) were deleted.

**Inferred reason:** Cleaner shape; reading-the-plan-as-intent. After reintegration, the old section's framing ("Production-side bundled implementations are not part of this repo") is now factually wrong, and the two "No … reference impls" subsections are now contradicted by the new `lib/services/` table (which has rows for all four sensors and the openlineage subscriber). The implementer chose to repair the contradictions inline rather than leave the index self-inconsistent. The plan author would almost certainly have agreed had it been flagged, but the plan's literal task wording did not authorize the surrounding cleanup.

This confirms orchestrator note #3.

---

## 4. `.golangci.yml` `consumption-side-isolation` comment — added a rationale paragraph the plan didn't ask for

**What the plan said (Task 10 step 3):** Rewrite the comment above the rule to state that `lib/services/` is the home of the bundled services, that the module graph already prevents the bad imports, and that the rule is retained as defense-in-depth. (One paragraph.)

**What was implemented:** The new comment matches the plan's content, but the implementer added a second paragraph explaining *why* the rule now lists both `**/lib/services/**` and the root-anchored `stores/** sensors/** …` globs: "depending on how golangci-lint resolves paths when linting the lib/services module (module-relative vs. workspace-relative vs. absolute), one form or the other is the one that matches. Listing both keeps the rule effective in every case."

**Inferred reason:** Defensive documentation of the redundant-looking glob duplication. The plan's step 2 explicitly chose to list both forms ("regardless of how golangci-lint resolves paths"), but didn't ask for that rationale to be captured in the file. The implementer added the rationale to head off a future cleanup pass that might "simplify" the rule by dropping one of the two forms. Plan-intent override (capturing intent the plan documented in prose but did not require in the file).

---

## 5. `lib/services/go.mod` ended up with the foundation module also in the workspace-resolved chain

**What the plan said (Task 6):** "`go mod tidy` … sync the workspace checksum file … build and vet."

**What was implemented:** Tidy produced a `lib/services/go.mod` whose `require` block includes only `lib/protocols` from the in-repo set, but the indirect-deps block lists transitively-pulled packages from foundation's dependency set (testcontainers' transitive deps, opentelemetry, etc.). The single `replace` directive is the one the plan named (`lib/protocols => ../protocols`); there is no `replace` against foundation. The actual `require` block is consistent with the plan ("requires only `lib/protocols`").

**Inferred reason:** Not a divergence — this is what `go mod tidy` produces. Recorded only because a reviewer scanning `go.mod` for "is the module graph really only depending on lib/protocols?" might be confused by the long indirect-deps tail; the indirect deps are pulled by `testcontainers`/`pgx`/`grpc`/etc., not by foundation.

---

## 6. The orchestrator's noted intermediate state (Pass 2 placeholder image tag) is **not** present in the final diff

**What the plan said (Task 4):** No image-tag manipulation; Task 4 is purely a Go-import rewrite.

**What was implemented:** The final diff has `storeFilesystemImage = "rimsky-store-filesystem:latest"` (the canonical end-state from Task 15). The intermediate `ghcr.io/rimsky-ai/rimsky-core/store-filesystem:latest` placeholder the orchestrator mentioned is not visible in the working tree.

**Inferred reason:** Confirms orchestrator note #1. The placeholder was a within-run intermediate state that Pass 5 / Task 15 overwrote with the canonical tag. Not a real divergence in the recorded artifact.

---

Total meaningful divergences recorded: **4** (items 1, 2, 3, 4). Items 5 and 6 are recorded as non-divergences for the reviewer's benefit.
