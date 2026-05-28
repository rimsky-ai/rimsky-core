# Divergences — 2026-05-27-release-skill

Audit of the working tree against `plans/2026-05-27-release-skill.md` and `specs/2026-05-27-release-skill-design.md`. The implementation is unusually faithful — all 14 tasks landed where the plan named them, with prose and structure matching the plan text. The divergences below are minor placement/structural notes rather than substantive shape changes.

## 1. Placement of the `LATEST_TAG` floating-tag explanatory sentence in the Makefile

- **What the plan said:** Task 2 step 4 — "Update the inline comment on the line above the `push-images` target (or the comment block above it, currently 'Push every rimsky image to $(REGISTRY) with SBOM + provenance attestations attached...') to mention the floating-tag parameterization. Append one sentence to the existing comment block: 'The floating second tag is `$(LATEST_TAG)`, defaulting to `latest`; `make dev-release` overrides it to `dev`.'"
- **What was implemented:** The sentence landed at `Makefile:177-178`, appended to the comment block that terminates at the `REGISTRY ?=` line (`Makefile:179`) — i.e. the block discussing the Hub namespace naming convention. The `push-images:` target itself is at `Makefile:197`, with a separate `BUILDX_PUSH` macro comment block immediately above it (`Makefile:193-195`). No floating-tag comment was attached to the `push-images:` adjacent block.
- **Inferred reason:** Plan ambiguity. The plan referenced "the comment block above it, currently 'Push every rimsky image…'" but no such comment block existed in the live Makefile (the actual block above `push-images:` is the `BUILDX_PUSH` macro definition). The implementer picked the most semantically adjacent block — the one introducing `REGISTRY`, which is the variable whose siblings now include `LATEST_TAG`. Cleaner placement given the actual file shape.

## 2. Position of `dev-release` target relative to `publish-protocols`

- **What the plan said:** Task 6 step 1 — "Locate a position in the `Makefile` between the `release:` target (Pass 1, Task 3) and the `publish-protocols:` target (the existing one). Add a new section header + target."
- **What was implemented:** `dev-release:` sits at `Makefile:263`, immediately after the new `release:` block at `Makefile:249` and before `publish-protocols:` at `Makefile:271`. That matches the plan's positional instruction. However, this places `dev-release` *before* `publish-protocols-dev` (at `Makefile:279`), which the plan's Task 4 (Add `publish-protocols-dev`) instructed to add "Immediately after `publish-protocols`'s body."
- **Inferred reason:** Plan-internal ordering tension. Tasks 4 and 6 each independently specified anchor points, but their results imply `publish-protocols` comes between `dev-release` (the caller) and `publish-protocols-dev` (the callee). The implementer followed both anchor instructions literally. The net order in the file is: `release` → `dev-release` → `publish-protocols` → `publish-protocols-dev`. Functionally fine; reads slightly oddly since the dev-release target invokes `publish-protocols-dev` defined 16 lines later.

## 3. Release-notes template inner fence in SKILL.md uses four backticks (no `markdown` language tag)

- **What the plan said:** Task 10 step 2 — the SKILL.md body block for the notes template begins with a bare triple-backtick fence: `` ``` `` (no language tag, no four-backtick outer fence).
- **What was implemented:** `.claude/skills/release/SKILL.md:113` opens the release-notes template with a four-backtick fence and closes it at line 151 with four backticks, matching the spec's outer-fence convention (`specs/2026-05-27-release-skill-design.md:178` — "outer fence uses four backticks so inner three-backtick code blocks nest correctly"). The plan's Task 10 literal text used only three backticks for the outer fence, which would have broken nesting around the inner `go get` / `npm install` code blocks.
- **Inferred reason:** Spec-intent override of a plan transcription error. The plan's outer fence count is inconsistent with the inner code blocks the same Task 10 block contains; the implementer matched the spec's explicit four-backtick rule instead.

## 4. Verification step run-count and `make build-all` gate execution

- **What the plan said:** Pass 1 verification expected `make lint && make license-lint` and Task 8 verification additionally expected `make build-all && make lint && make license-lint` to pass.
- **What was implemented:** Cannot be confirmed from the working tree alone — no log of verification runs is in the diff. The diff shows only the intended source changes; the build artifacts (e.g. binaries under `bin/`) are not present in the diff. This isn't a code divergence but is flagged because the plan's verification steps include runtime checks that leave no trace in the working tree.
- **Inferred reason:** Verification commands run outside the working-tree diff scope. Recorded for transparency only — `review-work` (the correctness step) is the appropriate place to re-run them.

## 5. No `.PHONY` ordering divergence; trailing position confirmed

- **What the plan said:** Task 7 — append `publish-protocols-dev` and `dev-release` to the existing `.PHONY` line so it ends with "... scan release buildx-builder publish-protocols-dev dev-release".
- **What was implemented:** `Makefile:1` ends with "...scan release buildx-builder publish-protocols-dev dev-release". Matches the plan literally.
- **Inferred reason:** No divergence; included only to confirm an explicit plan-prescribed ordering held.

---

Aside from the minor placement notes above, the implementation tracks the plan task-for-task. No substituted libraries, no extra helper files, no omitted plan steps, no concept-doc text that drifted from what the plan dictated. The SKILL.md prose, the dev-release.sh body, the RELEASING.md sections, and the module-layout.md mutation are all close-to-verbatim transcriptions of the plan's prescribed text.
