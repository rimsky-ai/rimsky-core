# Release skill + dev-release Make target

## Goal

Coordinate multi-artifact rimsky releases so the artifact set always ships in lockstep, mechanical orchestration is repeatable, and the two pieces of work that benefit from agentic judgment — SemVer bump decision and release-notes drafting — get that judgment without the operator having to remember coordination gaps.

A rimsky release spans five artifacts that today land via separate manual steps:

- The parent rimsky-core git tag `vX.Y.Z`.
- The `lib/protocols/vX.Y.Z` Go-submodule git tag.
- The `@rimsky-ai/protocols` npm package version (`lib/protocols/package.json`).
- Fifteen Docker Hub image tags under `docker.io/rimskyai/` (the four core images plus eleven bundled-service images, each at `:vX.Y.Z` and `:latest`).
- A GitHub Release with notes attached to the parent tag.

The recent v0.2.1 release surfaced the coordination cost: the `lib/protocols/v0.2.1` Go-submodule tag was missed until after the parent and images had shipped. The npm package version was also frozen at `0.1.0` and not bumped in lockstep with the parent. The skill exists to remove those gaps.

A pre-release / "dev" channel that ships frequent unstable builds for community testing is also in scope, but **does not need a skill** — it is fully mechanical (version derived from `git describe + date + SHA`, no SemVer judgment, no notes). It ships as a Makefile target instead.

## Deliverables

Two deliverables that share the existing `make release` build/scan/push chain:

1. **`/release` skill** — project-local skill at `.claude/skills/release/`. Agentic. Drives formal releases. Single user-confirmation gate; otherwise unsupervised.
2. **`make dev-release` target** — mechanical Makefile target. Drives the pre-release / dev channel. No skill, no agentic judgment.

A small set of related changes accompany the two deliverables:

3. **Extended `make release` chain** — adds `lint`, `license-lint`, `test-all` as host-side static + integration gates before the existing `core-images → service-images → scan → push-images` chain. Both the skill and `make dev-release` benefit from the same gate set.
4. **New `publish-protocols-dev` Make target** — wraps `cd lib/protocols && npm publish --tag dev`, gated by `check-clean`. Mirrors the existing `publish-protocols` target's shape. Used by `make dev-release`.
5. **`releases/` directory** — new in-repo home for per-tag release-notes markdown files (`releases/vX.Y.Z.md`). New top-level directory; see `## Design changes` below for the `concept:module-layout` adjustment that accompanies it.
6. **`RELEASING.md` and `CLAUDE.md` updates** — `RELEASING.md` extends to document the skill flow, the dev channel, and the release-notes template; `CLAUDE.md`'s "Release flow" pointer is rewritten to cover both deliverables and the extended chain.

The spec scope is "the publishing toolchain for rimsky-core's current artifact set." Generalization to other projects is explicitly deferred — develop here, generalize later (per the user's framing during brainstorm). No abstraction layer for portability is part of this spec.

## Architecture

```
              ┌──────────────────────────────────────────────┐
              │ Operator                                     │
              │                                              │
              │  /release          make dev-release          │
              │  (Claude Code      (cron / GH Action /       │
              │   session)          manual / git hook)       │
              └──────┬─────────────────────┬─────────────────┘
                     │                     │
                     ▼                     ▼
              ┌──────────────┐      ┌──────────────────┐
              │ /release     │      │ dev-release      │
              │ skill        │      │ Make target      │
              │              │      │                  │
              │  agentic:    │      │  mechanical:     │
              │   semver     │      │   version derive │
              │   notes      │      │   no notes       │
              │   review     │      │   tag            │
              │   gate       │      │   floating :dev  │
              │              │      │   npm --tag dev  │
              └──────┬───────┘      └────────┬─────────┘
                     │                       │
                     └───────────┬───────────┘
                                 ▼
                       ┌──────────────────────┐
                       │ make release         │
                       │ (extended chain)     │
                       │                      │
                       │  lint                │
                       │  license-lint        │
                       │  test-all            │
                       │  core-images         │
                       │  service-images      │
                       │  scan                │
                       │  push-images         │
                       └──────────────────────┘
```

Both formal and dev paths converge on the same `make release` target; the differences are in version selection (agentic vs mechanical), notes (full notes vs none), and outward-push details (`:latest` vs `:dev`, npm `@latest` vs `@dev`, GH Release vs prerelease).

The skill orchestrates around `make release`; it does not replace the Makefile's mechanical layer. The Makefile remains the canonical source of release mechanics; the skill is the canonical source of release judgment.

## Version coupling

Strict lockstep across all five artifacts. Every release bumps:

- Parent: `vX.Y.Z`.
- Go submodule: `lib/protocols/vX.Y.Z` (same `X.Y.Z`).
- npm: `@rimsky-ai/protocols@X.Y.Z` (same `X.Y.Z`, sans the `v` prefix per npm convention).
- Docker Hub: 15 images, each tagged `:vX.Y.Z` plus `:latest`.
- GitHub Release: `vX.Y.Z` tag, notes attached.

Pre-v1 SemVer rules apply (per `.claude/rules/rules.md` "Pre-v1 — break freely"):

- Major version stays at `0` until v1 ships.
- Minor bumps for breaking changes OR feature additions.
- Patch bumps for bugfixes that don't change any public surface.
- No major bump until v1.

The dev/nightly channel uses pre-release SemVer identifiers (`v0.3.0-dev.YYYYMMDD.gSHA`) which sort below the corresponding stable per SemVer 2.0; consumers tracking stable channels do not pick them up automatically. See "Dev release format" below. (The SHA is folded into the pre-release segment, dot-joined after the date, rather than carried as SemVer-2.0 build metadata after `+`. Build metadata after `+` is forbidden in Docker image tag grammar, silently stripped by `npm version`, and rejected by Go's `go get` canonical-version rule; the dot-joined form sidesteps all three.)

## `/release` skill

### File structure

The repo has no `.claude/skills/` directory today (only `.claude/rules/`, `.claude/settings.json`, and the scheduled-tasks lockfile live under `.claude/`). This skill establishes the `.claude/skills/` convention; future project-local skills can live alongside it without further bootstrap.

```
.claude/skills/release/                   # new directory
  SKILL.md                                # the skill prose; defines /release behavior
  scripts/                                # optional helpers (TBD during implementation —
                                          # the diff inspector may live here as a callable
                                          # or inline in SKILL.md as agent prose)
```

Slash command: `/release`. Optional arguments:

- `/release --minor` — operator-stated bump; skill audits against the diff and flags any mismatch at the final gate.
- `/release --patch` — operator-stated bump; same audit behavior.
- `/release --dry-run` — runs all steps except the outward push; reports what would happen.
- (No `--major` until v1 ships; the skill rejects it pre-v1 with a clear message.)
- (No `--dev` — dev releases use the Make target.)

### Flow

The skill walks these steps in order. Steps 1–5 are unsupervised internal work. Step 6 is the single user gate. Steps 7–8 are the post-gate automated pipeline.

#### 1. Preflight

Verify the environment can complete a release. Fail fast with the specific missing prereq:

- Working tree is clean (no uncommitted changes; no staged changes). Same shape as the existing `check-clean` target but reports its check directly so the skill controls the message.
- Current branch is the default branch (`main`). Reject releases from other branches.
- `docker login` is active to `docker.io/rimskyai`. There is no canonical "am I logged in to a specific registry" command; the skill probes by reading `~/.docker/config.json` for a credential entry under `https://index.docker.io/v1/` (or the equivalent OS keychain helper Docker is configured to use). If a published-image probe is preferred, the fallback is `docker manifest inspect docker.io/rimskyai/<some-image>:<some-tag>` — but this only works once the namespace has at least one image, which it does post-v0.2.1; the skill must handle the bootstrap case where no Hub image exists yet (early development / pre-v0.1.0) by falling back to the config-file probe.
- `npm whoami` returns a username with publish rights to the `@rimsky-ai` scope.
- `gh auth status` returns logged in to the GitHub remote.
- `docker scout` plugin is installed (`docker scout --help` exits 0).
- `docker buildx` is available (`docker buildx version` exits 0).

Any failure aborts the run with the specific missing prereq and remediation hint (e.g. "run `docker login docker.io`").

#### 2. Diff inspection and SemVer decision

Read the diff and commit log between the last stable tag and `HEAD`:

- Last stable tag: `git describe --tags --match='v[0-9]*' --exclude='*-dev*' --abbrev=0`.
- Diff scope: `git diff <last-stable>..HEAD` and `git log <last-stable>..HEAD --oneline`.

Classify the diff against the high-signal surfaces:

| Surface | Pattern matches | Bump trigger |
| --- | --- | --- |
| Wire protocol | `lib/protocols/proto/v1/*.proto` | Any change → minor (wire-contract breaking) |
| Persistence schema | `lib/foundation/persistence/postgres/migrations/*.sql`, `lib/foundation/persistence/sqlite/migrations/*.sql` | Any new migration file → minor (schema change) |
| Operator config | `cmd/rimsky*/` (flags, defaults), the `rimsky.yml` shape | Changed flag set or default → minor |
| Public API | Exported Go symbols in `lib/protocols/` and `lib/foundation/` | Added/removed/renamed/signature-changed export → minor |
| Environment | `RIMSKY_*` env var references in code | New required env var or changed default → minor |
| Anything else | (the catch-all) | Patch |

Skill computes the bump:

- Any surface above → minor.
- None of the above → patch.

If the operator passed `/release --minor` or `--patch`, the skill uses that as the proposed bump but performs the same diff analysis. If the analysis disagrees with the operator's stated bump, the skill records the mismatch as a question to surface at the final gate.

The skill writes a one-paragraph rationale capturing what it found (e.g. "Two proto files changed (`executor.proto`, `events.proto`); migrations added; one new exported Go symbol in `lib/foundation`. Suggested bump: minor.").

#### 3. Bump artifacts

In the working tree (uncommitted):

- Edit `lib/protocols/package.json` to set `version` to the new `X.Y.Z` (sans `v` prefix).
- Run `cd lib/protocols && npm install --package-lock-only --no-audit --no-fund` if needed to refresh `lib/protocols/package-lock.json`. If the lockfile doesn't change, no-op. (If the protocols package has no lockfile in this repo, skip.)

#### 4. Draft release notes

Write `releases/vX.Y.Z.md` from the diff + commit log + the bump rationale.

Template structure (rendered as Markdown; outer fence uses four backticks so inner three-backtick code blocks nest correctly):

````markdown
# rimsky vX.Y.Z

<one-paragraph release summary — what this release is about>

## Breaking changes

- <surface-by-surface enumeration; one bullet per breaking change>

## What's new

- <user-facing features, additions, new behaviors>

## Fixes

- <bug fixes worth surfacing to consumers>

## Internal

- <refactors, build changes, test additions; brief>

## Image set

`docker.io/rimskyai/rimsky:vX.Y.Z` and 14 sibling images, all at `:vX.Y.Z` and `:latest`. See [`RELEASING.md`](../RELEASING.md) for the full list.

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

Section rules:

- Empty sections omitted (a patch release usually has no Breaking changes, often no What's new).
- Each entry references a real diff hunk. Skill does not fabricate entries.
- Section ordering matches the template above so readers can scan a stack of release files consistently.

#### 5. Notes review loop (B+C hybrid)

The skill runs an internal review-iterate loop before involving the operator:

- Dispatch a reviewer subagent with the draft and the diff. Reviewer critiques against a rubric:
  - Every entry has a corresponding diff hunk (no fabrications).
  - Every surface flagged by the diff inspector as breaking appears in the Breaking changes section (no omissions).
  - Version bump is consistent with the Breaking changes content (a non-empty Breaking changes implies minor; a minor bump with empty Breaking changes is suspicious unless What's new is non-empty).
  - No `path:`/`file:` citations of code in the notes body (release notes are operator-facing prose, not the design-docs grammar — but they should also not invent paths the reader can't verify).
  - No invented features.
- Skill iterates the draft based on reviewer findings.
- Skill also performs a self-review pass: cross-check the final draft against the diff inspector's bump rationale; reconcile any contradictions.
- At loop exit, the skill identifies any genuine judgment questions worth surfacing to the operator. Examples:
  - "This proto change renames a field — depending on the consumer migration story, this might be backward-compatible or breaking. Flag as breaking?"
  - "The diff includes 47 commits; the notes I drafted cover the 5 most impactful. Want me to enumerate the rest under Internal?"
  - "Operator passed `--patch`, but the diff includes a new proto file (`new_protocol.proto`). The diff analysis suggests minor. Override the operator's stated bump?"

Only genuine ambiguity gets surfaced. Routine review findings get applied internally without surfacing.

#### 6. Single user gate

The skill presents one consolidated view to the operator:

```
Proposed release: v0.3.0
Bump rationale: <one-paragraph from step 2>

Release notes (releases/v0.3.0.md):
<full notes body>

Outward actions on confirmation:
- Commit: "release v0.3.0" (includes package.json bump + releases/v0.3.0.md)
- Tags: v0.3.0, lib/protocols/v0.3.0 (local; pushed after build)
- Build + gates: lint, license-lint, test-all, core-images, service-images, scan
- Hub push: 15 images at :v0.3.0 + :latest (via make push-images)
- Git push: v0.3.0 lib/protocols/v0.3.0 to origin
- npm publish: @rimsky-ai/protocols@0.3.0 to @latest
- GitHub release: v0.3.0 with releases/v0.3.0.md as notes

Questions for you:
<any flagged questions; empty if none>

Reply: go | revise <what> | abort
```

Operator response:

- `go` → proceed to step 7.
- `revise <something>` → skill iterates from the relevant step. Examples: `revise bump to patch` → skill recomputes notes for the new bump; `revise the Fixes section to mention the harness change` → skill rewrites that section; `answer: yes, flag the proto rename as breaking` → skill applies and re-presents.
- `abort` → skill reverts the `package.json` edit and exits cleanly. No tags, no commits, no Hub push, no remote state changed.

If the operator provides no answer (skill is invoked in a non-interactive context that doesn't allow user input), abort with a clear message — the gate is mandatory.

#### 7. Automated pipeline (post-gate, no user interaction)

On `go`:

1. Stage and commit the release:
   ```
   git add lib/protocols/package.json releases/vX.Y.Z.md
   git commit -m "release vX.Y.Z\n\n<release notes body>\n\nCo-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
   ```
2. Tag locally:
   ```
   git tag vX.Y.Z
   git tag lib/protocols/vX.Y.Z
   ```
3. Invoke `make release` (the extended chain). The formal path does NOT set the `LATEST_TAG` variable; it uses the default `latest`, so `:latest` floats to the new stable. (Dev releases override this; see the dev-release flow.) Failure handling per surface:
   - `lint`/`license-lint`/`test-all` failure → abort. The release commit is already staged; the skill leaves it for the operator to fix-forward or `git reset` (operator's call). Skill does NOT auto-revert the commit since the operator may want to address findings and re-run.
   - `core-images`/`service-images` failure → abort with the build output. Same disposition.
   - `scan` failure → enter CVE remediation flow (step 7a below).
   - `push-images` failure → abort. Hub state may be partial (some images pushed, some not); skill surfaces this clearly.
4. Push git tags:
   ```
   git push origin vX.Y.Z lib/protocols/vX.Y.Z
   ```
5. npm publish:
   ```
   make publish-protocols
   ```
   The existing `publish-protocols` Make target wraps `cd lib/protocols && npm publish` (no `--tag` argument; lands on `@latest`). Routing through the target keeps the Makefile as the single source of release mechanics.
6. GitHub Release:
   ```
   gh release create vX.Y.Z --notes-file releases/vX.Y.Z.md
   ```
   (no `--prerelease` flag).

#### 7a. CVE remediation (when scan fails)

The skill enters this flow when step 7's `make release` chain fails at `scan`. The behavior follows the hybrid policy:

1. For each failing image, run `docker scout recommendations <img>:<version>` to get base-image upgrade suggestions.
2. Classify each recommendation:
   - **Patch-level base bump** (e.g. `node:20-alpine3.19` → `node:20-alpine3.20`, `golang:1.25-alpine` → `golang:1.25.3-alpine`): mechanical, apply automatically. The skill edits the relevant `FROM` line in the Dockerfile.
   - **Anything else** (major version jumps, multi-line recommendation, "consider switching to distroless"): bail to operator with the analysis.
3. If all failing images had mechanical patch-level recommendations applied: amend the release commit to absorb the Dockerfile edits and move both tags onto the amended commit so the rerun sees a clean tree. The `push-images: check-clean` guard in the `Makefile` rejects any `VERSION` ending in `-dirty`, so leaving the Dockerfile edits uncommitted would dead-end the chain at the publish guard. Concretely: `git add <dockerfile>... && git commit --amend --no-edit && git tag -f vX.Y.Z && git tag -f lib/protocols/vX.Y.Z`. Then re-run the full chain from build forward (`make core-images service-images scan push-images`). If the new scan passes, push-images completes and the rest of the post-gate pipeline (step 7.4 onward) continues. If the rescan still fails: bail with the new analysis.
4. If any failing image had a non-mechanical recommendation: bail without applying any change; surface the full recommendation set to the operator.

On bail from step 7a: the partial state (release commit staged, tags local) is left for the operator. The operator can either fix forward and re-run (in which case the skill picks up at step 7 with the same version, since tags are idempotent on re-add), or abort manually with `git reset --hard HEAD~1 && git tag -d vX.Y.Z lib/protocols/vX.Y.Z`.

#### 8. Final report

On successful completion (step 7.6 returns 0):

```
Released vX.Y.Z

Hub: 15 images at docker.io/rimskyai/{rimsky, rimsky-all-in-one, ...}:vX.Y.Z (and :latest)
Git tags: vX.Y.Z, lib/protocols/vX.Y.Z (pushed to origin)
npm: @rimsky-ai/protocols@X.Y.Z (on @latest)
GitHub Release: https://github.com/rimsky-ai/rimsky-core/releases/tag/vX.Y.Z
```

### Diff inspection rules (detailed)

The skill applies these rules in step 2. Each rule pattern is a `git diff --name-only` filter; if any file matches and was modified/added/deleted, the rule fires.

1. **Wire protocol breaking** — `lib/protocols/proto/v1/*.proto`. Triggers minor.
2. **Persistence schema** — `lib/foundation/persistence/postgres/migrations/*.sql`, `lib/foundation/persistence/sqlite/migrations/*.sql`. Triggers minor.
3. **Operator config — flags and defaults** — modifications to `cmd/*/main.go` flag declarations (skill greps for `flag.String`, `flag.Bool`, etc.); modifications to the YAML config shape (the `concept:rimsky-yml` source-of-truth lives inside `lib/foundation/`-ish paths, not under `cmd/`, so the grep-based heuristic above misses YAML schema changes — those need a separate check, ideally a grep for changes in any file that imports the rimsky-yml struct types or references `RIMSKY_CONFIG` paths). Triggers minor. The rule is best-effort by design; misses and false positives both get surfaced as questions at the gate, and the operator's response wins.
4. **Public API** — added/removed/renamed/signature-changed exported Go symbols in `lib/protocols/` and `lib/foundation/`. Skill detects via `git diff` looking for `+func ` or `-func ` lines whose function name starts with a capital letter, and equivalently for types/vars. Triggers minor.
5. **Environment** — added or removed `RIMSKY_*` env var references in code. Skill greps for `RIMSKY_` in the diff. Triggers minor.

The rules are best-effort. False positives are surfaced as questions at the gate ("the diff touches `cmd/rimsky/flags.go` but only the help text — minor bump may not be needed"); the operator's response wins.

### Login preflight specifications

Skill verifies these in step 1, in order, abort on first failure:

```
docker info                                        # docker daemon reachable
docker buildx version                              # buildx plugin
docker scout --help                                # scout plugin
# Hub auth: read ~/.docker/config.json for a "https://index.docker.io/v1/"
# credential entry (or the equivalent keychain helper Docker is configured
# to use). Bootstrap-safe — does not depend on any image existing.
npm whoami                                         # npm auth, expects user in @rimsky-ai scope
gh auth status                                     # gh auth
```

Failure messages:

- "Docker daemon not reachable. Start Docker (or check `DOCKER_HOST`)."
- "buildx not installed. Install Docker Desktop or the buildx plugin manually."
- "scout not installed. Install Docker Desktop or run `docker scout enroll`."
- "Not authenticated to Docker Hub. Run `docker login docker.io`."
- "Not authenticated to npm under @rimsky-ai. Run `npm login`."
- "Not authenticated to GitHub. Run `gh auth login`."

## `make dev-release` Make target

Mechanical pre-release / nightly channel. Triggered by any source (cron, GitHub Action on push, manual). No agentic involvement.

### Version derivation

```
LAST_STABLE = git describe --tags --match='v[0-9]*' --exclude='*-dev*' --abbrev=0
            (e.g. v0.2.1)

NEXT_MINOR_BASE = <major>.<minor+1>.0 from LAST_STABLE
                (e.g. v0.3.0 if LAST_STABLE is v0.2.1)

DATE = $(date -u +%Y%m%d)        (UTC, e.g. 20260527)
SHA  = $(git rev-parse --short=7 HEAD)   (e.g. a1b2c3d)

DEV_VERSION = $(NEXT_MINOR_BASE)-dev.$(DATE).g$(SHA)
            (e.g. v0.3.0-dev.20260527.ga1b2c3d)
```

The `-dev.<date>.g<sha>` suffix is SemVer-2.0-compliant: the entire `-dev.20260527.ga1b2c3d` string is the pre-release identifier (dot-separated, three identifiers), with no SemVer build metadata component. The pre-release identifier ensures the version sorts below `v0.3.0` stable; the dot-joined SHA carries the commit identity.

The SHA lives in the pre-release segment rather than after a `+` build-metadata delimiter on purpose:

- **Docker image tag grammar** (`[a-zA-Z0-9_][a-zA-Z0-9_.-]*`) forbids `+`; a tag containing `+gSHA` is rejected by `docker build -t` / `docker tag`. The dev-release flow would fail at the first `docker build` line.
- **`npm version`** silently strips SemVer build metadata when writing `package.json` (`0.3.0-dev.20260527+ga1b2c3d` becomes `0.3.0-dev.20260527`). The published npm version would diverge from the git tag, breaking strict-lockstep.
- **Go's canonical-version rule** rejects build metadata after `+`; `go get …/lib/protocols@v0.3.0-dev.YYYYMMDD+gSHA` fails. Go-module consumers would have no working pin form.

Dot-joining the SHA inside the pre-release segment sidesteps all three while preserving SemVer-2.0 precedence (pre-release identifiers compare element-wise; a longer pre-release with a SHA tail still sorts below the corresponding stable).

### Flow

```
make dev-release:
    @# Entry preconditions: clean tree. Steps that modify package.json
    @# transiently pass VERSION=$(DEV_VERSION) to dependent Make targets
    @# so the *-dirty arm of check-clean does not trip on the uncommitted
    @# package.json edit.
    1. Compute DEV_VERSION as above.
    2. Tag locally: git tag $(DEV_VERSION); git tag lib/protocols/$(DEV_VERSION)
    3. Temporarily bump lib/protocols/package.json (uncommitted):
       cd lib/protocols && npm version --no-git-tag-version $(DEV_VERSION_NOPREFIX)
    4. Build + scan + push at DEV_VERSION with the :dev floating tag override:
       LATEST_TAG=dev VERSION=$(DEV_VERSION) make release
       (See "Floating :dev tag handling" below. make release's push-images
       step uses $(LATEST_TAG), defaulting to "latest" for formal releases
       and "dev" for this dev path. Result: each image gets :$(DEV_VERSION)
       and :dev pushed; :latest is never moved. The VERSION override lets
       check-clean see the dev version string instead of the dirty suffix
       from step 3's uncommitted package.json bump.)
    5. Push git tags: git push origin $(DEV_VERSION) lib/protocols/$(DEV_VERSION)
    6. npm publish with dev dist-tag:
       VERSION=$(DEV_VERSION) make publish-protocols-dev
       (which wraps `cd lib/protocols && npm publish --tag dev`, mirrors
       the existing publish-protocols target's shape, gated by check-clean.
       The VERSION override sidesteps the *-dirty check for the same
       reason as step 4.)
    7. Revert package.json bump (uncommitted):
       git checkout lib/protocols/package.json
       (And if a lockfile exists at lib/protocols/package-lock.json, also revert
       it: git checkout lib/protocols/package-lock.json. The current tree has no
       lockfile; the conditional handles future addition without script changes.)
    8. Optional: gh release create $(DEV_VERSION) --prerelease --generate-notes
       (decide whether to include; if yes, mark prerelease.)
```

### Floating `:dev` tag handling

`make release`'s `push-images` target currently pushes `:$(VERSION)` AND `:latest` for every image. For dev releases, we want `:$(DEV_VERSION)` AND `:dev` (NOT `:latest`).

The spec picks one shape: **override the floating tag during push** via a `LATEST_TAG` Make variable.

- `push-images` reads `$(LATEST_TAG)`, default `latest`.
- Formal releases (`make release`, or the skill's invocation of it) get the default; `:latest` moves to the new stable.
- `make dev-release` invokes `LATEST_TAG=dev make release`; `:dev` moves to the new dev build instead, and `:latest` is untouched.

No post-push retag step is needed; the override pushes `:dev` directly during build. (The alternative — push `:latest` then retag to `:dev` after — would have already overwritten the stable `:latest` for the duration between push and retag, which is incorrect.)

The `LATEST_TAG` override is the entire floating-tag mechanism; there is no separate `DEV_RELEASE` flag.

### npm dev publish

`npm publish --tag dev` lands the version under the `dev` dist-tag, not `latest`. Consumers install via:
- `npm install @rimsky-ai/protocols@dev` — always gets the latest dev build.
- `npm install @rimsky-ai/protocols@0.3.0-dev.20260527.ga1b2c3d` — exact pin.

Consumers tracking `@latest` (the default) continue to get the last stable release.

### Implementation home

The dev-release logic is heavy enough that pure Makefile becomes awkward (shell-heavy, hard to test). Spec calls for a `tools/dev-release.sh` script that the `make dev-release` target invokes. The script is the source of truth for dev-release logic; the Makefile target is the entry point.

```
tools/dev-release.sh    # shell script implementing the dev-release flow
Makefile:
    dev-release: check-clean
        @./tools/dev-release.sh
```

The script tests can live in the same dir if useful (`tools/dev-release_test.sh`). Decide during implementation.

## Extended `make release` chain

Current state of the `release` target:

```
release: core-images service-images scan push-images
```

The spec extends it to:

```
release: lint license-lint test-all core-images service-images scan push-images
```

Effect:

- `lint` runs first (golangci-lint across root + lib/foundation + lib/protocols + lib/services). Cheap, host-side.
- `license-lint` runs second (`go run ./tools/license-check`). Cheap, host-side.
- `test-all` runs third (full Go test suite across all four modules via `go test ./...` in each; this picks up the scenario tests under `test/scenarios/...` and the testcontainer-using tests inside `lib/services`, which exercise testcontainers + Docker). Slower; requires Docker daemon for the testcontainer tests.
- The existing build + scan + push chain continues unchanged in target shape, with one variable addition: `push-images` consumes a `LATEST_TAG` variable (default `latest`) for the floating tag. Formal releases get the default; `make dev-release` invokes `LATEST_TAG=dev make release` to redirect the floating tag to `:dev`.

Both `/release` skill (step 7.3) and `make dev-release` (step 4) invoke `make release`. Both pay the same gate cost.

The host-side gates do not currently fire as part of any release path. Adding them creates a meaningful gate increase per release — the test-all run especially. This is intentional: `rules.md`'s "Verify the build" rule already says every check fires on every code commit; a release is the moment that discipline matters most. The cost is real but bounded by how often formal releases are cut. For dev releases, the same gate set fires — but dev releases skip the full notes review and (typically) ship more frequently than formal, so dev-release runtime is dominated by the gate chain.

## `releases/` directory

New top-level directory: `releases/`. Contains one markdown file per release tag (`releases/v0.3.0.md`, etc.). Tracked in git.

The directory is created as part of this work. Initial population is empty — the first file is whatever release comes next after this spec lands.

A `releases/README.md` describes the layout and points readers at `RELEASING.md` for the release process.

## RELEASING.md updates

The existing `RELEASING.md` already documents the manual `make release` flow. Spec extends it with:

- A new section: "Cutting a release with `/release`" — describes the skill invocation, what it does, what the operator sees at the gate, what's automated after.
- A new section: "Dev / nightly releases with `make dev-release`" — describes the channel, the version format, how consumers opt in (`docker pull ...:dev`, `npm install @rimsky-ai/protocols@dev`, `go get .../lib/protocols@v0.3.0-dev...`).
- The "Release flow" section updated to mention both paths (skill for formal, Make target for dev) and to clarify that the manual `make release` chain still works for direct invocation by operators who prefer not to use the skill.
- The release-notes template (the one in `## Draft release notes` above) added as a reference section.

## CLAUDE.md updates

The "Release flow" pointer in `CLAUDE.md` (added during the services reintegration work) is rewritten to reflect both deliverables and the extended chain. The current pointer documents `make release` only; the new wording adds the skill, the dev-release Make target, and the extended `lint → license-lint → test-all` prefix:

```
**Release flow** — see `RELEASING.md`. For formal releases, the
`/release` skill (project-local at `.claude/skills/release/`) drives
SemVer judgment, release-notes drafting + review, and the outward
push chain through a single confirmation gate. For pre-release / dev
builds, `make dev-release` runs the same build + scan + push chain
mechanically — version derived as `v<next-minor>.0-dev.<date>.g<sha>`,
no notes, floating `:dev` Hub tag, npm `--tag dev`. Both paths share
the extended `make release` chain (`lint → license-lint → test-all →
core-images → service-images → scan → push-images`).
```

## Design changes

This work adds a new release-notes home (a fifth top-level entry in the repo root) and a new project-local skills home (under the existing operator-tooling tree). The `concept:module-layout` concept's Boundaries section claims ownership of "the four-way top-level directory grouping (binaries / library code / out-of-tree tests + machinery / dev tooling)." Adding a non-code release-notes top-level entry is the kind of repo-root structure choice an adjacent reader might expect the concept to address; the existing image-build-inputs top-level entry sits in the same shape but the concept does not acknowledge it either. This spec clarifies the concept's scope: it owns the four code groups, not the entirety of the repo root.

The new skills home lives under the operator-tooling tree (which holds settings, rules, hooks). Operator tooling is outside `concept:module-layout`'s scope; the skill home does not touch the concept.

- Concept: mutate `concepts/module-layout.md` in place.
  - In "What it is," refine the lead sentence to make explicit that the four-way grouping is a code-only grouping. Then add a follow-on sentence acknowledging that non-code top-level entries coexist for artifact-storage purposes (image build inputs, release notes) and are not part of the grouping the concept owns. Use the concept's existing group-naming convention ("the cmd group", "the lib group") rather than path-form names.
  - In "Boundaries," extend the existing "Owns" clause to read "the four-way top-level code grouping" (explicit code qualifier). Extend the existing inline "Does NOT own:" list with a new entry: artifact-storage top-level entries (image build inputs, release notes); those exist alongside the four code groups but are out of scope for the concept's invariants. (Treat the "Does NOT own" extension as extending the inline list, not as adding a new sub-section.)
  - Append a Notes entry: `2026-05-27 (spec: 2026-05-27-release-skill-design): made the four-way grouping explicit as a code-only invariant. Non-code top-level entries — image build inputs (pre-existing) and per-tag release notes (introduced by this spec) — coexist alongside the four code groups but are out of the concept's scope.`

The Notes entry follows the concept self-containment rule: no file or directory paths, no external doc references. The "image build inputs" / "per-tag release notes" descriptors are role-form, not path-form.

No other concepts are touched. No tensions are resolved or introduced.

## Behavior in edge cases

- **No prior stable tag.** If `git describe --tags --match='v[0-9]*' --exclude='*-dev*'` finds no stable tag, the skill rejects with a clear message asking the operator to cut v0.1.0 manually first. This is a bootstrap case; the skill is not for first-version cuts.
- **Stable tag exists but `HEAD == last-stable-tag`.** No new commits since the last release. Skill rejects: nothing to release.
- **Operator on a non-default branch.** Skill rejects: releases are cut from `main` only.
- **Uncommitted changes in working tree.** Skill rejects via the preflight check.
- **`lib/protocols/` empty (no changes since last release).** Lockstep still bumps; the Go module gets a new tag pointing at the same content as the prior tag. Consumers re-resolve to the new tag; no behavior change but the version moves. This is the cost of strict lockstep.
- **Multiple operators invoking concurrently.** Out of scope. The skill assumes a single operator; concurrent invocations are not protected against. Tag conflicts would surface as errors on the git push step.
- **Network failures mid-publish.** Best-effort: skill reports what completed and what failed. Operator handles cleanup. Most outward actions are idempotent (re-pushing a tag is a no-op if it matches; re-pushing an image is fast); some are not (npm publish on a version already published fails). The skill does not implement retry — operator drives.

## Error handling

- **Skill internal errors.** Surfaced with the failure context. Skill prefers to abort cleanly (revert `package.json` edit, no tags created) rather than leave partial state.
- **`make release` chain failures.** Each surface failure leaves the working tree in a different state:
  - Pre-build (lint/license-lint/test-all): tree has the release commit, no images built. Operator fixes forward and re-runs `/release`; the skill detects the existing commit and continues from build.
  - Build (core-images / service-images): tree has release commit, partial image build state. Operator fixes the build issue and re-runs.
  - Scan: enters CVE remediation per step 7a; either auto-applies and continues, or bails with analysis.
  - Push: partial Hub state. Operator manually retries `make push-images` (the push-images target is idempotent for already-pushed images).
- **Outward push failures (git push / npm publish / gh release create).** Each is recoverable manually:
  - `git push` failures: surface, operator retries.
  - `npm publish` failure on a version that's already on npm: surface, no retry needed (publish was successful in a prior run).
  - `gh release create` failure on a tag that already has a release: surface, no retry.
  Skill does not implement retry logic; operator drives recovery.

## Testing

The skill itself is prose plus optional helper scripts. The mechanical pieces are testable:

- **Diff inspection rules.** Unit tests against curated commit ranges with known classifications. Could live as a `tools/release-inspector/` Go binary with table-driven tests, or as test fixtures the skill invokes via `gh api` against a known commit range. Decide during implementation; the diff-inspection logic can be a Go binary that the skill invokes, in which case it carries its own tests.
- **Dev-release script (`tools/dev-release.sh`).** Shell script tests via shellcheck + bats or similar. Test cases: version derivation against a fixture repo, tag idempotency, package.json bump + revert.
- **`make release` chain extension.** The new gates (`lint`, `license-lint`, `test-all`) are existing targets being added to a target chain. The chain's correctness is verified by `make -n release` showing the expected sequence.
- **Skill end-to-end.** No automated test — the skill is interactive and exercises real services (Docker daemon, Hub, npm, GitHub). Verification is by running it during the next real release.

## Non-goals / YAGNI

- **No abstraction for portability.** The skill is project-local to rimsky-core. Generalization to other projects is deferred per the brainstorm decision.
- **CLI binaries are not release-attached assets.** `make cli-release` builds cross-platform CLI binaries (`rimsky_{linux,darwin}_{amd64,arm64}` plus `rimsky_windows_amd64.exe` — Windows arm64 is not built today) but the spec does NOT attach them to the GitHub Release. CLI consumers install via `go install github.com/rimsky-ai/rimsky-core/cmd/rimsky@vX.Y.Z` or build locally; binary distribution is a separate concern that this spec does not cover. If binary attachment becomes a goal, that's a future spec — the change is well-scoped (build invocation in the skill's post-gate pipeline, additional file arguments to `gh release create`).
- **No CHANGELOG.md.** The per-release files under `releases/` replace the conventional changelog pattern; no single growing file.
- **No automated SemVer enforcement beyond the diff inspector.** The skill does not block releases on commit-message format, version-file existence, or other linting beyond what's in the rules above.
- **No release branch management.** Releases are cut from `main`; the skill does not create or push release branches.
- **No backport workflow.** If a critical bug requires patching v0.3.0 after v0.4.0 is out, the operator does it manually. The skill does not support release branches.
- **No rollback automation.** If a release ships broken, operator handles rollback manually (delete Hub tags, npm-unpublish within 72h, etc.).
- **No multi-operator coordination / locking.** Concurrent `/release` invocations are not protected against.
- **No conventional-commits parsing.** Diff inspection is the source of truth; commit messages are advisory.
- **No release-notes localization.** English only.
- **No release-day automation triggers.** Operator runs `/release` when ready; no scheduled releases.
- **No npm version reverts on partial failure.** If npm publish succeeds but a later step fails, the npm version stays. The 72-hour `npm unpublish` window is operator-driven.
- **No GitHub Discussions or pre-release-poll integrations.** GitHub Release is the only consumer-facing release surface.

## Open questions (resolved during brainstorm, captured for the record)

- **Floating `:dev` Hub tag implementation**: resolved — `LATEST_TAG` Make variable override during push (see "Floating `:dev` tag handling" above). No separate flag, no post-push retag step.
- **Dev-release GitHub pre-release flag**: whether `make dev-release` invokes `gh release create --prerelease` is a small operational choice deferred to implementation. Default lean: yes, with `--generate-notes` for the body.
- **`tools/release-inspector/` as Go binary vs inline skill prose**: implementation decision. Spec describes the rules; the rule engine can live in either form.

## Acceptance criteria

The work is done when:

1. `/release` command exists under `.claude/skills/release/SKILL.md` and walks the documented flow on a real release.
2. `make dev-release` target exists in the `Makefile`, derives a SemVer-2.0-compliant dev version, runs the extended `make release` chain (with `LATEST_TAG=dev` override pushing the floating `:dev` Hub tag), and publishes the protocols npm package via the new `make publish-protocols-dev` target.
3. `make release` chain includes `lint license-lint test-all` as gates before the build.
4. `releases/` directory exists, with `releases/README.md`.
5. `RELEASING.md` extends to document both flows and the release-notes template.
6. `CLAUDE.md`'s "Release flow" pointer mentions both paths.
7. A real formal release cut via `/release` lands all five artifact types in lockstep.
8. A real dev release cut via `make dev-release` lands the dev-tagged artifacts without disturbing `:latest` or `@latest`.
