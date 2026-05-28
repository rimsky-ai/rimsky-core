# Release Skill + Dev-Release Make Target Implementation Plan

**Spec:** .ok-planner/specs/2026-05-27-release-skill-design.md
**Goal:** Add the project-local `/release` skill, the `make dev-release` mechanical target, the extended `make release` chain with new pre-push gates, and the supporting documentation + concept-doc mutation.
**Architecture:** Two release paths share one extended `make release` chain (`lint → license-lint → test-all → core-images → service-images → scan → push-images` with parameterized `$(LATEST_TAG)`). Formal releases drive the chain through `/release` (agentic — SemVer judgment, release-notes drafting + review, single user gate). Dev releases drive the chain through `make dev-release` (mechanical — version derived from `git describe + date + SHA`, floating `:dev` Hub tag, npm `--tag dev`). One concept-doc mutation accompanies the new `releases/` top-level directory.
**Tech Stack:** GNU make, bash (`tools/dev-release.sh`), Claude Code skill prose (Markdown SKILL.md), Markdown documentation.

---

## Context the implementer must have

This plan implements a release-coordination toolchain. The implementer will be in a fresh Claude Code session with no memory of the brainstorm or this plan-writing turn. Read the spec at `.ok-planner/specs/2026-05-27-release-skill-design.md` end-to-end before starting Pass 1 — the spec is the source of truth for what each piece does. This plan describes **how to land each piece in the repo**.

### Live-tree facts the plan relies on

- The current `Makefile` at `Makefile` (repo root) already has: `release`, `core-images`, `service-images`, `push-images`, `scan`, `check-clean`, `publish-protocols`, `lint`, `license-lint`, `test-all`, `buildx-builder`, plus the `IMAGES`, `REGISTRY`, `VERSION`, `BUILDX_PUSH` variables. The plan extends this Makefile in place.
- `push-images` (currently at lines ~189–209 of `Makefile`) uses `docker buildx build --push --provenance=mode=max --sbom=true` via the `$(BUILDX_PUSH)` macro. Each image build line currently ends with `-t $(REGISTRY)/<name>:$(VERSION) -t $(REGISTRY)/<name>:latest .` — the literal `latest` is what gets parameterized in Pass 1.
- `release` is currently `release: core-images service-images scan push-images` (one line). It gets extended in Pass 1.
- `publish-protocols` (currently `publish-protocols: check-clean` with body `cd lib/protocols && npm publish`) is left unchanged; a sibling `publish-protocols-dev` is added.
- `check-clean` rejects publishing from a dirty tree by inspecting `$(VERSION)` for a `-dirty` suffix or the literal value `dev`. The dev-release flow passes `VERSION=$(DEV_VERSION)` to dependent targets so this guard does not trip on the uncommitted `package.json` bump.
- `lib/protocols/package.json` currently has `"version": "0.1.0"`. No `package-lock.json` exists.
- `CLAUDE.md` line 23 is the "Release flow" pointer to rewrite.
- `RELEASING.md` exists; sections of it document the manual `make release` chain, image set, Scout integration, and a DSOS application stub.
- `.ok-planner/design/concepts/module-layout.md` is the concept file to mutate. Its current "What it is" uses group-form names ("the cmd group", "the lib group"), not path-form (`cmd/`, `lib/`). The mutation follows the same convention — read the file before editing.
- `.claude/skills/` directory does **not** exist; only `.claude/rules/`, `.claude/settings.json`, and `.claude/scheduled_tasks.lock` live under `.claude/` today. Pass 2 creates the new directory.
- `releases/` directory does **not** exist. Pass 3 creates it.

### Files this plan creates

- `tools/dev-release.sh` — bash script driving the mechanical dev-release flow.
- `.claude/skills/release/SKILL.md` — the project-local `/release` skill prose.
- `releases/README.md` — small README pointing at `RELEASING.md`.

### Files this plan modifies

- `Makefile`
- `RELEASING.md`
- `CLAUDE.md`
- `.ok-planner/design/concepts/module-layout.md`

### What this plan does NOT do

- Does not actually cut a release. No `git tag`, no `npm publish`, no `docker push` runs as part of plan execution. The work produces the toolchain; using it to ship is the user's call afterward.
- Does not move `lib/protocols/package.json`'s version off `0.1.0`. The next real release (run via the new skill) will bump it; this plan only adds the machinery.
- Does not run the extended `make release` chain to verify it ships images. The chain's static structure is verified (`make -n release`) but actually running it requires Docker login + a Hub push, which is outside the autonomous test scope.

---

## Pass 1: Makefile + tools/dev-release.sh

**Goal:** Land the build-system mechanics — extended `make release` chain, parameterized floating tag, two new Make targets (`publish-protocols-dev`, `dev-release`), and the shell script that implements dev-release.
**Scope:** Tasks 1–8
**End state:** working
**Verification:** `make -n release | grep -E 'lint|license-lint|test-all' >/dev/null && make -n dev-release >/dev/null && make -n publish-protocols-dev >/dev/null && make -n push-images LATEST_TAG=dev | grep ':dev ' >/dev/null && bash -n tools/dev-release.sh && make lint && make license-lint`

### Task 1: Add `LATEST_TAG` variable

**Files:** `Makefile`

**Steps:**
1. Read the current `Makefile`. Locate the `REGISTRY ?= docker.io/rimskyai` line.
2. Immediately after the `REGISTRY ?=` line, add:
   ```
   # Floating tag pushed alongside :$(VERSION) on every image. Defaults to
   # `latest` for formal releases; `make dev-release` overrides to `dev` so
   # the dev channel never moves :latest.
   LATEST_TAG ?= latest
   ```
3. Run `grep -n 'LATEST_TAG ?= latest' Makefile` and confirm it returns one match.

**Verification:** `grep -n 'LATEST_TAG ?= latest' Makefile`

### Task 2: Parameterize `push-images` second tag

**Files:** `Makefile`

**Steps:**
1. Locate the `push-images:` target body (currently 15 `$(BUILDX_PUSH) -f ... -t $(REGISTRY)/<name>:$(VERSION) -t $(REGISTRY)/<name>:latest .` lines). Each line has the literal `:latest` as the second tag.
2. For every one of the 15 image build invocations **inside the `push-images` target body**, replace `:latest .` at the end with `:$(LATEST_TAG) .`. The leading `-t $(REGISTRY)/<name>:$(VERSION)` stays unchanged; only the second `:latest` becomes `:$(LATEST_TAG)`. **Do NOT touch** the `:latest` tags in `core-images` and `service-images` (lines ~103–128) — those are local-tag builds; the floating-tag parameterization is purely about what gets pushed to Hub.
3. Confirm count: `sed -n '/^push-images:/,/^[a-zA-Z]/p' Makefile | grep -c ':$(LATEST_TAG) \.'` should return `15` (one per image build line inside `push-images`). `sed -n '/^push-images:/,/^[a-zA-Z]/p' Makefile | grep -c ':latest \.'` should return `0` (no literal `latest` second-tag left inside `push-images`).
4. Update the inline comment on the line above the `push-images` target (or the comment block above it, currently "Push every rimsky image to $(REGISTRY) with SBOM + provenance attestations attached...") to mention the floating-tag parameterization. Append one sentence to the existing comment block: "The floating second tag is `$(LATEST_TAG)`, defaulting to `latest`; `make dev-release` overrides it to `dev`."

**Verification:** `sed -n '/^push-images:/,/^[a-zA-Z]/p' Makefile | grep -c ':$(LATEST_TAG) \.'` returns `15` and `sed -n '/^push-images:/,/^[a-zA-Z]/p' Makefile | grep -c ':latest \.'` returns `0`.

### Task 3: Extend `release` target with pre-push gates

**Files:** `Makefile`

**Steps:**
1. Locate the existing comment block + `release` target. The current state is a short comment block above a single-line target: `release: core-images service-images scan push-images`. Read enough of the surrounding text to find the boundaries of the block (the comment block above `release` mentions "builds locally (so the harness can use the resulting tags), scans for critical/high CVEs, then pushes with attestations").
2. Replace the entire existing comment block + the `release:` line in **one** combined edit with the following replacement text. (One edit, not two — `release:` appears only once in the replacement.)
   ```
   # Full release chain — pre-push gates, build, scan, push.
   #
   # Order:
   #   lint           — golangci-lint across all four modules (cheap, host-side)
   #   license-lint   — go run ./tools/license-check (cheap, host-side)
   #   test-all       — full Go test suite, including testcontainer scenarios
   #                    (requires Docker daemon for the testcontainer tests)
   #   core-images    — build the 4 core images locally
   #   service-images — build the 11 bundled-service images locally
   #   scan           — docker scout cves against every locally-built image
   #   push-images    — buildx build + push with SBOM + provenance attestations
   #
   # Both `/release` (the skill, formal releases) and `make dev-release`
   # (mechanical dev channel) invoke this chain; dev-release overrides
   # LATEST_TAG=dev so the floating tag pushed alongside :$(VERSION) is :dev
   # instead of :latest. If scan finds vulnerabilities, the chain stops before
   # push.
   release: lint license-lint test-all core-images service-images scan push-images
   ```
3. Confirm exactly one `release:` line exists in the Makefile after the edit: `grep -c '^release:' Makefile` must return `1`. Confirm the new prereq list: `grep -E '^release: lint license-lint test-all core-images service-images scan push-images' Makefile` must match.

**Verification:** `grep -c '^release:' Makefile` returns `1` AND `grep -E '^release: lint license-lint test-all core-images service-images scan push-images' Makefile` matches.

### Task 4: Add `publish-protocols-dev` target

**Files:** `Makefile`

**Steps:**
1. Locate the `publish-protocols: check-clean` target (currently around the `publish-protocols` comment block near the bottom of the publishing section).
2. Immediately after `publish-protocols`'s body (the `cd lib/protocols && npm publish` line), add a blank line then:
   ```
   # Dev-channel sibling of publish-protocols. Lands the version under the
   # `dev` npm dist-tag instead of `latest`. Same clean-tree guard.
   # Invoked by `make dev-release` (which passes VERSION=$(DEV_VERSION) so
   # the *-dirty arm of check-clean does not trip on the uncommitted
   # package.json bump in the dev flow).
   publish-protocols-dev: check-clean
   	cd lib/protocols && npm publish --tag dev
   ```
3. Confirm: `grep -n '^publish-protocols-dev:' Makefile` returns one match.

**Verification:** `make -n publish-protocols-dev VERSION=v0.0.0-test 2>&1 | grep -q 'npm publish --tag dev'`

### Task 5: Write `tools/dev-release.sh`

**Files:** new `tools/dev-release.sh`

**Note on precedent:** This is the first shell script under `tools/`. The existing `tools/license-check/` is a Go binary with its own test suite. The spec at `spec:2026-05-27-release-skill-design`'s "Implementation home" section deliberately picks bash over Go for the dev-release work (it's orchestration: `git tag`, `docker buildx`, `npm publish`, `gh release create` — all external-command-driven, where bash is the natural shape). Acknowledge the precedent break but follow the spec.

**Steps:**
1. Create the file `tools/dev-release.sh`. Make it executable (`chmod +x tools/dev-release.sh`).
2. The script implements the dev-release flow per spec section "`make dev-release` Make target" (specifically the "Flow" subsection). Write it as a defensive bash script with `set -euo pipefail`, no positional arguments. The script body:

   ```bash
   #!/usr/bin/env bash
   # Copyright © 2026 Fall Guy Consulting.
   # Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
   # license. See LICENSE.agpl and COPYRIGHT at the repo root.
   set -euo pipefail

   # tools/dev-release.sh — mechanical dev-channel release.
   #
   # Invoked by `make dev-release`. Derives a SemVer-2.0 pre-release version
   # of the form v<next-minor>.0-dev.<YYYYMMDD>+g<sha> from the latest stable
   # tag, then drives the same `make release` chain a formal release uses,
   # with LATEST_TAG=dev so the floating :dev Hub tag moves (not :latest).
   # Also bumps lib/protocols/package.json transiently for the npm publish,
   # then reverts.
   #
   # Preconditions: clean working tree, docker login, npm login, branch on
   # default. The script does not verify preconditions itself — the Make
   # targets it invokes (check-clean) catch the dirty-tree case via
   # VERSION=$(DEV_VERSION) override; broader preconditions are the
   # operator's concern (or are handled by the /release skill, not this
   # mechanical path).

   cd "$(git rev-parse --show-toplevel)"

   # --- 1. Derive DEV_VERSION ---
   LAST_STABLE="$(git describe --tags --match='v[0-9]*' --exclude='*-dev*' --abbrev=0 2>/dev/null || true)"
   if [ -z "${LAST_STABLE}" ]; then
       echo "no stable tag found (expected something like v0.X.Y); cut a stable release first" >&2
       exit 1
   fi
   # LAST_STABLE is vMAJOR.MINOR.PATCH; bump minor, zero patch for the dev base.
   STABLE_NO_V="${LAST_STABLE#v}"
   IFS='.' read -r MAJOR MINOR PATCH <<<"${STABLE_NO_V}"
   NEXT_MINOR_BASE="v${MAJOR}.$((MINOR + 1)).0"
   DATE="$(date -u +%Y%m%d)"
   SHA="$(git rev-parse --short=7 HEAD)"
   DEV_VERSION="${NEXT_MINOR_BASE}-dev.${DATE}+g${SHA}"
   DEV_VERSION_NOPREFIX="${DEV_VERSION#v}"

   echo "dev-release: deriving ${DEV_VERSION} (last stable: ${LAST_STABLE})"

   # --- 2. Tag locally ---
   git tag "${DEV_VERSION}"
   git tag "lib/protocols/${DEV_VERSION}"

   # --- 3. Transient package.json bump ---
   (cd lib/protocols && npm version --no-git-tag-version "${DEV_VERSION_NOPREFIX}")

   # --- 4. Build + scan + push with LATEST_TAG=dev ---
   # VERSION override stops check-clean from seeing the dirty suffix from step 3.
   LATEST_TAG=dev VERSION="${DEV_VERSION}" make release

   # --- 5. Push git tags ---
   git push origin "${DEV_VERSION}" "lib/protocols/${DEV_VERSION}"

   # --- 6. npm publish with dev dist-tag ---
   VERSION="${DEV_VERSION}" make publish-protocols-dev

   # --- 7. Revert transient package.json bump ---
   git checkout lib/protocols/package.json
   if [ -f lib/protocols/package-lock.json ]; then
       git checkout lib/protocols/package-lock.json
   fi

   # --- 8. (Optional) GitHub prerelease ---
   # Comment out the line below if GH prereleases are not desired.
   gh release create "${DEV_VERSION}" --prerelease --generate-notes

   echo "dev-release: shipped ${DEV_VERSION}"
   ```
3. Confirm the script has valid bash syntax: `bash -n tools/dev-release.sh`.
4. Confirm the script is executable: `test -x tools/dev-release.sh`.

**Verification:** `bash -n tools/dev-release.sh && test -x tools/dev-release.sh && echo OK`

### Task 6: Add `dev-release` Make target

**Files:** `Makefile`

**Steps:**
1. Locate a position in the `Makefile` between the `release:` target (Pass 1, Task 3) and the `publish-protocols:` target (the existing one). Add a new section header + target:
   ```
   # Mechanical pre-release / dev channel. Derives a SemVer-2.0 pre-release
   # version (v<next-minor>.0-dev.<YYYYMMDD>+g<sha>) from the latest stable
   # tag, then drives the same `make release` chain with LATEST_TAG=dev so the
   # floating tag pushed alongside :$(VERSION) is :dev, not :latest. Bumps
   # lib/protocols/package.json transiently for the npm publish.
   #
   # Implementation lives in tools/dev-release.sh (shell-heavy work that doesn't
   # belong inline in the Makefile). The target is the entry point operators
   # invoke (manually, via CI, via cron, etc.).
   #
   # The /release skill (formal releases) does NOT invoke this target — formal
   # releases run the same chain but with their own SemVer/notes/review logic.
   dev-release: check-clean
   	@./tools/dev-release.sh
   ```
2. Confirm: `grep -n '^dev-release:' Makefile` returns one match; the body invokes `./tools/dev-release.sh`.

**Verification:** `make -n dev-release | grep -q 'tools/dev-release.sh'`

### Task 7: Update `.PHONY`

**Files:** `Makefile`

**Steps:**
1. Read the first non-empty line of `Makefile`, which is the `.PHONY: ...` declaration.
2. Append the new target names: `publish-protocols-dev`, `dev-release`. The new `.PHONY` line should end with `... scan release buildx-builder publish-protocols-dev dev-release`.
3. Confirm: `head -1 Makefile | grep -q publish-protocols-dev` and `head -1 Makefile | grep -q ' dev-release'` (the leading space matters so it doesn't false-match `dev-release-something`).

**Verification:** `head -1 Makefile | grep -q publish-protocols-dev && head -1 Makefile | grep -q ' dev-release'`

### Task 8: Verify the Makefile parses end-to-end

**Files:** none (verification only)

**Steps:**
1. Run `make -n release VERSION=v0.0.0-test` and confirm the output begins with the four gate steps in order: `golangci-lint` (or `lint`), `license-check` (or `license-lint`), `go test` (or `test-all`), then the build chain. Specifically: `make -n release VERSION=v0.0.0-test 2>&1 | head -20 | grep -E 'golangci-lint|license-check|go test'` should produce 3+ matches (one per gate).
2. Run `make -n dev-release` and confirm it invokes `./tools/dev-release.sh`.
3. Run `make -n push-images VERSION=v0.0.0-test LATEST_TAG=dev` and confirm the output contains `:dev` tags (`grep ':dev '` should match) and zero `:latest` tags (`grep -c ':latest '` should return `0`).
4. Run `make -n push-images VERSION=v0.0.0-test` (no LATEST_TAG override) and confirm `:latest` appears 15 times (`grep -c ':latest '` returns `15`).
5. Run `make build-all && make lint && make license-lint` to confirm nothing in the Makefile changes broke the host-side gates.

**Verification:** `make -n release VERSION=v0.0.0-test 2>&1 | head -20 | grep -cE 'golangci-lint|license-check|go test' | awk '$1 >= 3 {exit 0} {exit 1}' && make -n dev-release 2>&1 | grep -q tools/dev-release.sh && make build-all && make lint && make license-lint`

---

## Pass 2: `.claude/skills/release/SKILL.md`

**Goal:** Land the project-local `/release` skill prose.
**Scope:** Tasks 9–10
**End state:** working
**Verification:** `test -f .claude/skills/release/SKILL.md && head -1 .claude/skills/release/SKILL.md | grep -q '^---' && grep -q '^name: release' .claude/skills/release/SKILL.md`

### Task 9: Create the skill directory

**Files:** `.claude/skills/release/` (new directory)

**Steps:**
1. Run `mkdir -p .claude/skills/release/`.
2. Confirm: `test -d .claude/skills/release && echo OK`.

**Verification:** `test -d .claude/skills/release`

### Task 10: Write `.claude/skills/release/SKILL.md`

**Files:** new `.claude/skills/release/SKILL.md`

**Steps:**

1. Read the spec at `.ok-planner/specs/2026-05-27-release-skill-design.md` end-to-end. The `/release` skill section is the source of the SKILL.md content; everything in that section needs to land in the SKILL.md prose.

2. Write the file with this structure. The frontmatter and the prose body must be self-contained — the SKILL.md will be read by Claude when `/release` is invoked, with no other context.

   File body:

   ```markdown
   ---
   name: release
   description: "ONLY activated by explicit /release slash command. Cuts a formal rimsky release: SemVer judgment from diff inspection, release-notes drafting + review, then the automated outward push (Hub images with attestations, git tags, npm publish, GitHub Release). Single user-confirmation gate."
   ---

   # /release — Cut a formal rimsky release

   Orchestrates the agentic parts of cutting a formal rimsky release: looks
   at the diff since the last stable tag to decide a SemVer bump, drafts
   release notes against the diff, runs a notes-review loop, and presents
   a single consolidated confirmation gate to the operator. After the
   gate, the skill drives the mechanical pipeline (tag, build + scan +
   push, git push, npm publish, GitHub Release) without further user
   interaction.

   For dev/nightly releases, use `make dev-release` — the mechanical path
   does not need agentic judgment and bypasses this skill entirely.

   ## When invoked

   The user invokes `/release` from a Claude Code session in the
   `rimsky-core` repo root. Optional arguments:

   - `/release --minor` — operator-stated bump; skill audits against the
     diff and flags any mismatch at the gate.
   - `/release --patch` — operator-stated bump; same audit behavior.
   - `/release --dry-run` — runs all steps except the outward push.
     Reports what would happen.
   - `--major` is rejected pre-v1 with a clear message.
   - `--dev` is rejected — dev releases use `make dev-release`.

   ## Flow

   Walk these steps in order. Steps 1–5 are unsupervised internal work.
   Step 6 is the single user gate. Steps 7–8 are the post-gate automated
   pipeline.

   ### 1. Preflight

   Verify the environment can complete a release. Fail fast with the
   specific missing prereq:

   - Working tree is clean (no uncommitted changes; no staged changes).
     Run `git status --porcelain` and confirm empty output.
   - Current branch is `main`. Run `git rev-parse --abbrev-ref HEAD` and
     confirm.
   - `docker info` exits 0 (Docker daemon reachable).
   - `docker buildx version` exits 0 (buildx plugin available).
   - `docker scout --help` exits 0 (Scout plugin available).
   - Docker Hub auth: read `~/.docker/config.json` and verify an
     `auths."https://index.docker.io/v1/"` entry exists (or the equivalent
     keychain helper is configured). This is bootstrap-safe: it doesn't
     depend on any image existing in the Hub namespace.
   - `npm whoami` returns a username with publish rights to the
     `@rimsky-ai` scope.
   - `gh auth status` returns logged in.

   Any failure aborts the run with the specific missing prereq and a
   remediation hint (e.g. "run `docker login docker.io`").

   ### 2. Diff inspection and SemVer decision

   Read the diff and commit log between the last stable tag and HEAD:

   - Last stable tag:
     ```
     git describe --tags --match='v[0-9]*' --exclude='*-dev*' --abbrev=0
     ```
   - Diff scope: `git diff <last-stable>..HEAD` and
     `git log <last-stable>..HEAD --oneline`.

   Classify the diff against the high-signal surfaces. Any match triggers
   a minor bump; absence triggers a patch bump.

   - **Wire protocol** — any change under `lib/protocols/proto/v1/*.proto`.
   - **Persistence schema** — any new migration file under
     `lib/foundation/persistence/postgres/migrations/` or
     `lib/foundation/persistence/sqlite/migrations/`.
   - **Operator config — flags and defaults** — modifications to
     `cmd/*/main.go` flag declarations (grep `flag.String`, `flag.Bool`,
     etc.); modifications to the YAML config shape (best-effort grep for
     changes in files referencing rimsky-yml struct types).
   - **Public API** — added/removed/renamed/signature-changed exported Go
     symbols in `lib/protocols/` and `lib/foundation/`. Detect via
     `git diff` for `+func ` or `-func ` lines whose function name starts
     with a capital letter, and equivalently for types/vars.
   - **Environment** — added or removed `RIMSKY_*` env var references in
     code.

   If the operator passed `--minor` or `--patch`, use that as the proposed
   bump but still perform the diff analysis. Record any mismatch as a
   question for the final gate.

   Write a one-paragraph rationale capturing what the analysis found
   (which surfaces moved, why each triggered the bump it did).

   The rule is best-effort by design; misses and false positives both get
   surfaced as questions at the gate, and the operator's response wins.

   ### 3. Bump artifacts

   Edit `lib/protocols/package.json` in the working tree (uncommitted) to
   set `version` to the new `X.Y.Z` (sans the `v` prefix). If a
   `lib/protocols/package-lock.json` exists, refresh it with
   `cd lib/protocols && npm install --package-lock-only --no-audit --no-fund`.
   If no lockfile exists today, skip.

   ### 4. Draft release notes

   Write `releases/vX.Y.Z.md` against the template structure documented in
   `RELEASING.md`. The structure:

   ```
   # rimsky vX.Y.Z

   <one-paragraph release summary>

   ## Breaking changes

   - <surface-by-surface enumeration>

   ## What's new

   - <user-facing features, additions, new behaviors>

   ## Fixes

   - <bug fixes worth surfacing to consumers>

   ## Internal

   - <refactors, build changes, test additions; brief>

   ## Image set

   `docker.io/rimskyai/rimsky:vX.Y.Z` and 14 sibling images, all at
   `:vX.Y.Z` and `:latest`. See `RELEASING.md` for the full list.

   ## Go module

   ```
   go get github.com/rimsky-ai/rimsky-core@vX.Y.Z
   go get github.com/rimsky-ai/rimsky-core/lib/protocols@vX.Y.Z
   ```

   ## npm

   ```
   npm install @rimsky-ai/protocols@X.Y.Z
   ```
   ```

   Section rules:

   - Empty sections (often Breaking changes on a patch release) are
     omitted.
   - Every entry references a real diff hunk. Do not fabricate entries.

   ### 5. Notes review loop (B+C hybrid)

   Run an internal review-iterate loop before involving the operator:

   - Dispatch a reviewer subagent with the draft and the diff. Reviewer
     critiques against a rubric:
     - Every entry has a corresponding diff hunk (no fabrications).
     - Every surface flagged by the diff inspector as breaking appears in
       the Breaking changes section (no omissions).
     - Version bump is consistent with Breaking changes content
       (non-empty implies minor; minor with empty Breaking changes is
       suspicious unless What's new is non-empty).
     - No invented features.
   - Iterate the draft based on reviewer findings.
   - Self-review pass: cross-check the final draft against the diff
     inspector's bump rationale; reconcile any contradictions.
   - At loop exit, identify any genuine judgment questions worth
     surfacing to the operator (e.g. "this proto change renames a field;
     flag as breaking?"). Only genuine ambiguity gets surfaced; routine
     review findings get applied internally.

   ### 6. Single user gate

   Present one consolidated view to the operator:

   ```
   Proposed release: vX.Y.Z
   Bump rationale: <one-paragraph from step 2>

   Release notes (releases/vX.Y.Z.md):
   <full notes body>

   Outward actions on confirmation:
   - Commit: "release vX.Y.Z" (includes package.json bump + releases/vX.Y.Z.md)
   - Tags: vX.Y.Z, lib/protocols/vX.Y.Z (local; pushed after build)
   - Build + gates: lint, license-lint, test-all, core-images, service-images, scan
   - Hub push: 15 images at :vX.Y.Z + :latest (via make release's push-images)
   - Git push: vX.Y.Z lib/protocols/vX.Y.Z to origin
   - npm publish: @rimsky-ai/protocols@X.Y.Z to @latest (via make publish-protocols)
   - GitHub release: vX.Y.Z with releases/vX.Y.Z.md as notes

   Questions for you:
   <any flagged questions; empty if none>

   Reply: go | revise <what> | abort
   ```

   On `go` → proceed to step 7. On `revise <something>` → iterate from
   the relevant step. On `abort` → revert the `package.json` edit and
   exit cleanly (no tags, no commits, no Hub push).

   ### 7. Automated pipeline (post-gate)

   1. Stage and commit the release:
      ```
      git add lib/protocols/package.json releases/vX.Y.Z.md
      git commit -m "release vX.Y.Z

      <release notes body>

      Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
      ```
   2. Tag locally:
      ```
      git tag vX.Y.Z
      git tag lib/protocols/vX.Y.Z
      ```
   3. Invoke `make release`. The formal path does NOT set `LATEST_TAG`;
      it uses the default `latest`. Failure handling:
      - Pre-build gate failure (lint / license-lint / test-all) → abort.
        The release commit is staged; leave for the operator to address.
      - Build failure → abort with the build output. Same disposition.
      - Scan failure → enter CVE remediation (step 7a).
      - Push failure → abort. Hub state may be partial; surface clearly.
   4. Push git tags:
      ```
      git push origin vX.Y.Z lib/protocols/vX.Y.Z
      ```
   5. npm publish:
      ```
      make publish-protocols
      ```
      (lands on `@latest`).
   6. GitHub Release:
      ```
      gh release create vX.Y.Z --notes-file releases/vX.Y.Z.md
      ```

   ### 7a. CVE remediation (when scan fails)

   1. For each failing image, run `docker scout recommendations <img>:<version>`.
   2. Classify each recommendation:
      - **Patch-level base bump** (e.g. `node:20-alpine3.19` →
        `node:20-alpine3.20`): mechanical, apply automatically. Edit the
        relevant `FROM` line in the affected Dockerfile.
      - **Anything else** (major version jumps, multi-line
        recommendations, non-mechanical changes): bail to operator with
        the analysis.
   3. If all failing images had mechanical patch-level recommendations
      applied: re-run the full chain from build forward (`make
      core-images service-images scan push-images`). On clean rescan,
      push-images completes and the post-gate pipeline (step 7.4 onward)
      continues. If still failing, bail with new analysis.
   4. If any failing image had a non-mechanical recommendation: bail
      without applying any change; surface the full recommendation set.

   On bail: the partial state (release commit staged, tags local) is
   left for the operator. They can fix forward and re-run (the skill
   detects the existing commit + tags and continues), or manually
   roll back.

   ### 8. Final report

   ```
   Released vX.Y.Z

   Hub: 15 images at docker.io/rimskyai/{rimsky, rimsky-all-in-one, ...}:vX.Y.Z (and :latest)
   Git tags: vX.Y.Z, lib/protocols/vX.Y.Z (pushed to origin)
   npm: @rimsky-ai/protocols@X.Y.Z (on @latest)
   GitHub Release: https://github.com/rimsky-ai/rimsky-core/releases/tag/vX.Y.Z
   ```

   ## Edge cases

   - **No prior stable tag.** Reject with a message asking the operator
     to cut v0.1.0 manually first. The skill is not for first-version
     cuts.
   - **`HEAD == last-stable-tag`.** No new commits. Reject: nothing to
     release.
   - **Operator on non-default branch.** Reject: releases are cut from
     `main` only.
   - **Uncommitted changes in working tree.** Reject in preflight.
   - **Concurrent invocations.** Out of scope. The skill assumes a single
     operator.
   - **`--dry-run`.** Runs steps 1–6 with the gate as "this is what would
     happen, run for real?"; skips step 7.
   ```

3. After writing, confirm the file is well-formed Markdown: `test -f .claude/skills/release/SKILL.md`. Visually confirm the frontmatter is at the top (lines 1–4 should be `---` / `name: release` / `description: ...` / `---`).

**Verification:** `test -f .claude/skills/release/SKILL.md && head -4 .claude/skills/release/SKILL.md | head -1 | grep -q '^---' && grep -q '^name: release' .claude/skills/release/SKILL.md && grep -q '## Flow' .claude/skills/release/SKILL.md`

---

## Pass 3: Documentation + design-doc mutation

**Goal:** Land the `releases/` directory + README, the `RELEASING.md` updates, the `CLAUDE.md` rewrite, and the `concept:module-layout` mutation.
**Scope:** Tasks 11–14
**End state:** working
**Verification:** `test -d releases && test -f releases/README.md && grep -q '/release' RELEASING.md && grep -q '/release' CLAUDE.md && grep -q '2026-05-27-release-skill-design' .ok-planner/design/concepts/module-layout.md`

### Task 11: Create `releases/` directory + README

**Files:** new `releases/`, new `releases/README.md`

**Steps:**
1. Run `mkdir -p releases/`.
2. Write `releases/README.md`:
   ```markdown
   # Release notes

   This directory holds one Markdown file per release tag,
   `releases/vX.Y.Z.md`. Each file is written by the `/release` skill
   when a formal release is cut and is also attached to the matching
   GitHub Release.

   See `../RELEASING.md` for the release process — what `/release`
   does, what `make dev-release` does, what gets tagged where, and the
   release-notes template the skill fills in.

   Dev/nightly releases (`v0.X.0-dev.YYYYMMDD+gSHA` tags) do not produce
   files here; they ship without notes by design.
   ```
3. Confirm: `test -f releases/README.md && head -1 releases/README.md | grep -q 'Release notes'`.

**Verification:** `test -d releases && test -f releases/README.md && head -1 releases/README.md | grep -q 'Release notes'`

### Task 12: Update `RELEASING.md`

**Files:** `RELEASING.md`

**Steps:**
1. Read the existing `RELEASING.md` end-to-end. The current document has these sections: `# Releasing rimsky`, `## Release flow` (covering manual `make release`), `## Image set`, `## Docker Scout integration`, `## Supply chain attestations`, `## Hub-grade gotcha: "No AGPL v3 licenses" green check`, `## Docker-Sponsored Open Source (DSOS)`, `## Pre-v1 caveats`.

2. The `## Release flow` section currently describes the manual `make release` chain. Replace its body (everything between `## Release flow` and the next `## ` heading) with:

   ```markdown
   ## Release flow

   Rimsky cuts releases via one of two paths.

   ### Formal releases (`/release` skill)

   For human-cut releases that carry SemVer judgment and notes, invoke
   `/release` from a Claude Code session in the repo root. The skill
   walks:

   1. Preflight (verify clean tree, branch on `main`, docker/npm/gh
      logins active, tooling available).
   2. Diff inspection — reads the diff since the last stable tag,
      classifies against high-signal surfaces (proto files, persistence
      migrations, exported Go symbols, CLI flags, env vars), proposes
      a SemVer bump.
   3. Bumps `lib/protocols/package.json` in the working tree.
   4. Drafts `releases/vX.Y.Z.md` against the template (below).
   5. Notes review loop — reviewer subagent + skill self-review against
      a rubric (every entry maps to a diff hunk, every breaking surface
      appears in the Breaking changes section, bump matches content).
   6. Single user gate — presents bump rationale, full notes, action
      manifest, and any flagged judgment questions. Operator replies
      `go` / `revise <what>` / `abort`.
   7. On `go`: stages the release commit (`package.json` +
      `releases/vX.Y.Z.md`), creates both git tags, invokes `make
      release` (which runs the extended `lint → license-lint →
      test-all → core-images → service-images → scan → push-images`
      chain), pushes git tags, runs `make publish-protocols`, creates
      the GitHub Release via `gh release create`.

   If scan finds CVEs, the skill attempts mechanical patch-level base-
   image remediation (per `docker scout recommendations`); anything
   bigger bails to the operator.

   See `.claude/skills/release/SKILL.md` for the full skill prose.

   ### Dev / nightly releases (`make dev-release`)

   For pre-release / community-testing builds, run `make dev-release`.
   The target derives a SemVer-2.0 pre-release version of the form
   `v<next-minor>.0-dev.<YYYYMMDD>+g<sha>` from the latest stable tag,
   then runs the same `make release` chain with `LATEST_TAG=dev`. Result:

   - Hub images get `:vX.Y.Z-dev.YYYYMMDD+gSHA` plus the floating `:dev`
     tag. `:latest` is never moved.
   - Both git tags (`vX.Y.Z-dev.YYYYMMDD+gSHA` and
     `lib/protocols/vX.Y.Z-dev.YYYYMMDD+gSHA`) are pushed.
   - `@rimsky-ai/protocols` is published under the `dev` npm dist-tag.
   - GitHub creates a prerelease (with auto-generated notes).

   No release-notes file is written; dev consumers track the `:dev` Hub
   tag, the `@rimsky-ai/protocols@dev` npm dist-tag, or pin to a specific
   version.

   Consumers opt in:

   - Docker: `docker pull docker.io/rimskyai/rimsky-all-in-one:dev`
   - npm: `npm install @rimsky-ai/protocols@dev`
   - Go: `go get github.com/rimsky-ai/rimsky-core/lib/protocols@v0.X.0-dev.YYYYMMDD+gSHA`

   The dev path is mechanical — no SemVer judgment, no notes, no review.
   Trigger it manually, from cron, from a CI hook on push to `main`, or
   anywhere a clean tree is available.

   ### Shared chain

   Both paths invoke `make release`, which runs:

   ```
   lint → license-lint → test-all → core-images → service-images → scan → push-images
   ```

   - `lint` and `license-lint` are cheap host-side gates.
   - `test-all` runs the full Go test suite across all four modules,
     including testcontainer-using tests (requires Docker daemon).
   - `core-images` and `service-images` build the 4 + 11 images locally.
   - `scan` runs `docker scout cves --only-severity critical,high
     --exit-code` against every locally-built image. Blocks on
     unaddressed critical or high CVEs.
   - `push-images` uses `docker buildx build --push
     --provenance=mode=max --sbom=true` so SBOM + provenance
     attestations attach to each manifest. Pushes `:$(VERSION)` plus
     `:$(LATEST_TAG)` — the latter defaults to `latest`, overridden to
     `dev` for dev releases.

   Refuses to run from a dirty tree (via `check-clean`).
   ```

3. After the existing `## Image set` section, locate where to insert the release-notes template. Add a new section before `## Docker Scout integration`:

   ```markdown
   ## Release-notes template

   The `/release` skill writes one Markdown file per formal release to
   `releases/vX.Y.Z.md`, filled in from the diff analysis. Skeleton:

   ````markdown
   # rimsky vX.Y.Z

   <one-paragraph release summary>

   ## Breaking changes

   - <surface-by-surface enumeration; omit section if empty>

   ## What's new

   - <user-facing features, additions, new behaviors>

   ## Fixes

   - <bug fixes worth surfacing to consumers>

   ## Internal

   - <refactors, build changes, test additions; brief>

   ## Image set

   `docker.io/rimskyai/rimsky:vX.Y.Z` and 14 sibling images, all at
   `:vX.Y.Z` and `:latest`. See [`RELEASING.md`](../RELEASING.md) for
   the full list.

   ## Go module

   ```
   go get github.com/rimsky-ai/rimsky-core@vX.Y.Z
   go get github.com/rimsky-ai/rimsky-core/lib/protocols@vX.Y.Z
   ```

   ## npm

   ```
   npm install @rimsky-ai/protocols@X.Y.Z
   ```
   ````

   Section rules: empty sections are omitted; every entry references a
   real diff hunk. The skill's review loop catches fabrications and
   omissions before the operator sees the draft.
   ```
   (The outer fence uses four backticks so the inner three-backtick code blocks render correctly.)

4. Confirm the updates landed: `grep -q '## Release-notes template' RELEASING.md && grep -q 'make dev-release' RELEASING.md && grep -q '/release' RELEASING.md`.

**Verification:** `grep -q '## Release-notes template' RELEASING.md && grep -q 'make dev-release' RELEASING.md && grep -q '/release' RELEASING.md`

### Task 13: Rewrite `CLAUDE.md`'s "Release flow" pointer

**Files:** `CLAUDE.md`

**Steps:**
1. Locate the "Release flow" line in `CLAUDE.md` (currently line ~23, beginning with `**Release flow** — see \`RELEASING.md\`. The canonical entry point is \`make release\`, ...`). Read the full paragraph to identify its boundaries (it's one long single-paragraph block ending with "Refuses to run from a dirty tree.").

2. Replace that entire paragraph with:

   ```
   **Release flow** — see `RELEASING.md`. For formal releases, the `/release` skill (project-local at `.claude/skills/release/`) drives SemVer judgment, release-notes drafting + review, and the outward push chain through a single confirmation gate. For pre-release / dev builds, `make dev-release` runs the same build + scan + push chain mechanically — version derived as `v<next-minor>.0-dev.<date>+g<sha>`, no notes, floating `:dev` Hub tag, npm `--tag dev`. Both paths share the extended `make release` chain (`lint → license-lint → test-all → core-images → service-images → scan → push-images`). The chain's `push-images` step uses `docker buildx build --push --provenance=mode=max --sbom=true` so SBOM + provenance attestations land on Hub. Refuses to run from a dirty tree.
   ```

3. Confirm: `grep -q '/release' CLAUDE.md && grep -q 'dev-release' CLAUDE.md && grep -q 'lint → license-lint → test-all' CLAUDE.md`.

**Verification:** `grep -q '/release' CLAUDE.md && grep -q 'make dev-release' CLAUDE.md && grep -q 'lint . license-lint . test-all' CLAUDE.md`

### Task 14: Mutate `.ok-planner/design/concepts/module-layout.md`

**Files:** `.ok-planner/design/concepts/module-layout.md`

**Steps:**

1. Read the existing concept file end-to-end. The "What it is" section currently begins: "The Go workspace ties four modules into one build. The repo root holds four idiomatic top-level code directories: binaries (the cmd group), shippable library code (the lib group), out-of-tree tests plus their machinery (the test group), and dev tooling (the tools group)." The "Boundaries" section's "Owns" clause currently lists "the four-way top-level directory grouping (binaries / library code / out-of-tree tests + machinery / dev tooling)". The "Notes" section is the append-only audit trail.

2. **Edit "What it is"** (the first prose paragraph following the `## What it is` heading): replace the existing sentence "The repo root holds four idiomatic top-level code directories: binaries (the cmd group), shippable library code (the lib group), out-of-tree tests plus their machinery (the test group), and dev tooling (the tools group)." with:

   "The repo root holds four idiomatic top-level **code** directories: binaries (the cmd group), shippable library code (the lib group), out-of-tree tests plus their machinery (the test group), and dev tooling (the tools group). Non-code top-level entries — image build inputs and per-tag release notes — coexist alongside the four code groups for artifact-storage purposes; they are not part of the four-way grouping the concept owns."

   (The change adds the explicit "code" qualifier and the follow-on sentence acknowledging non-code top-level entries.)

3. **Edit "Boundaries"** (the "Owns" clause). The current text reads:
   "Owns: the per-module manifests, the workspace definition, the layer-purity lint rules, the four-layer ordering inside the root module's library code, the four-way top-level directory grouping (binaries / library code / out-of-tree tests + machinery / dev tooling), the four-module workspace (protocols, foundation, services, root), and the bundled-services module home under the lib group."

   Replace it with:
   "Owns: the per-module manifests, the workspace definition, the layer-purity lint rules, the four-layer ordering inside the root module's library code, the four-way top-level code grouping (binaries / library code / out-of-tree tests + machinery / dev tooling), the four-module workspace (protocols, foundation, services, root), and the bundled-services module home under the lib group."

   (The single change is "four-way top-level directory grouping" → "four-way top-level code grouping".)

   In the same "Boundaries" paragraph, the existing inline "Does NOT own:" list reads: "Does NOT own: package-internal layout (that's per-feature), proto wire content (owned by the protocols module)."

   Extend it to read: "Does NOT own: package-internal layout (that's per-feature), proto wire content (owned by the protocols module), artifact-storage top-level entries (image build inputs, per-tag release notes; they exist alongside the four code groups but are out of scope for the concept's invariants)."

   (The change appends a new comma-separated entry to the existing inline list. This is an extension of the existing list, not a new sub-section.)

4. **Append a Notes entry** at the end of the `## Notes` section (currently ending with the 2026-05-27 services-reintegration entry). Add as a new bullet at the bottom:

   "- 2026-05-27 (spec: 2026-05-27-release-skill-design): made the four-way grouping explicit as a code-only invariant. Non-code top-level entries — image build inputs (pre-existing) and per-tag release notes (introduced by this spec) — coexist alongside the four code groups but are out of the concept's scope."

5. Confirm the mutation landed: `grep -q 'four idiomatic top-level \*\*code\*\* directories' .ok-planner/design/concepts/module-layout.md && grep -q 'four-way top-level code grouping' .ok-planner/design/concepts/module-layout.md && grep -q 'artifact-storage top-level entries' .ok-planner/design/concepts/module-layout.md && grep -q '2026-05-27-release-skill-design' .ok-planner/design/concepts/module-layout.md`.

6. Confirm the mutation is self-containment-compliant (no file paths introduced into the body):
   ```
   diff <(git show HEAD:.ok-planner/design/concepts/module-layout.md 2>/dev/null) .ok-planner/design/concepts/module-layout.md | grep -E '^\+' | grep -vE '^\+\+\+' | grep -E '/\w+\.\w+|@source|@diverged|pkg:|code:' || echo "no path citations introduced"
   ```
   The expected output is `no path citations introduced`. (The grep looks for path-like patterns the new diff adds; the rule excludes spec-slug references like `spec:2026-05-27-...` which are allowed.)

**Verification:** `grep -q 'four idiomatic top-level \*\*code\*\* directories' .ok-planner/design/concepts/module-layout.md && grep -q 'four-way top-level code grouping' .ok-planner/design/concepts/module-layout.md && grep -q 'artifact-storage top-level entries' .ok-planner/design/concepts/module-layout.md && grep -q '2026-05-27-release-skill-design' .ok-planner/design/concepts/module-layout.md`

---

## Manual checks after completion

These cannot be automated as part of the plan run; the user runs them after `/execute-plan` completes and the implementation has been reviewed.

1. **Exercise `/release` end-to-end on the next real release.** The skill flow can only be fully verified by cutting a real release. The next time a release is cut, invoke `/release` and walk it through. Surface anything that doesn't behave per the spec for follow-up.

2. **Exercise `make dev-release` once manually.** Run `make dev-release` against a clean working tree (and a clean Docker login + npm login + gh login). Confirm:
   - A `vX.Y+1.0-dev.YYYYMMDD+gSHA` git tag is created and pushed.
   - The 15 Hub images each get `:vX.Y+1.0-dev.YYYYMMDD+gSHA` AND `:dev` tags; `:latest` is NOT moved.
   - `@rimsky-ai/protocols` is published under the `dev` dist-tag (`npm view @rimsky-ai/protocols dist-tags`).
   - The GH Release is created with `--prerelease`.
   - `lib/protocols/package.json` is reverted to its pre-run version.

3. **Confirm `concepts.md` TOC line for `module-layout`** is still accurate after the concept mutation. The TOC line currently says "The Go workspace ties four modules into one build, with the root module itself carrying a four-layer split." That remains correct after the mutation (the four-module workspace is unchanged); no edit needed unless future work changes the count.

4. **Confirm GitHub Releases page** doesn't have stale draft releases from any test runs of the skill; clean up if so.
