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

Walk these steps in order. Step 0 is argument validation. Steps 1–5
are unsupervised internal work. Step 6 is the single user gate. Steps
7–8 are the post-gate automated pipeline.

### 0. Argument validation

Before any other work:

- If `--major` is present, reject immediately with: "pre-v1 — break
  freely, but no major bumps until v1 ships". Exit without touching the
  working tree.
- If `--dev` is present, reject with: "dev releases use `make
  dev-release`; this skill is for formal releases only".
- Unknown flags are rejected with a usage hint.

### 1. Preflight

Verify the environment can complete a release. Fail fast with the
specific missing prereq:

- Working tree is clean (no uncommitted changes; no staged changes).
  Run `git status --porcelain` and confirm empty output.
- `docker info` exits 0 (Docker daemon reachable).
- `docker buildx version` exits 0 (buildx plugin available).
- `docker scout --help` exits 0 (Scout plugin available).
- Docker Hub auth: read `~/.docker/config.json` and confirm an
  `auths."https://index.docker.io/v1/"` entry exists; if not, fail with
  "Not authenticated to Docker Hub. Run `docker login docker.io`."
  This is a best-effort presence probe. The macOS keychain helper
  (`credsStore: "desktop"`) keeps the URL key in `config.json` with an
  empty `{}` value even after the actual credential in the OS keychain
  has been lost (sign-outs, helper crashes); the presence probe cannot
  distinguish that case from a healthy auth. Probing a public image
  (e.g. `docker manifest inspect docker.io/rimskyai/rimsky:latest`)
  does not help — `docker manifest inspect` against a public image
  returns exit 0 with no credentials at all, so it would silently pass
  the same broken-keychain case. The honest contract: if the eventual
  `make release` push step fails with a 401, the operator re-runs
  `docker login docker.io` and retries the skill (the post-gate
  pipeline detects the existing release commit + tags and resumes).
  Surface this limitation in the preflight report so an operator who
  hits the 401 later knows the check was not exhaustive.
- `npm whoami` returns a username with publish rights to the
  `@rimsky-ai` scope.
- `gh auth status` returns logged in.
- `goreleaser --version` exits 0, and `syft version` exits 0 (the
  goreleaser `sboms` step catalogs each CLI archive with syft; the
  release fails without it — `brew install syft`).

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

````
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

## CLI

Prebuilt archives (`linux`/`darwin`, `amd64`/`arm64`, each with a
published SBOM) attached to this GitHub Release. See `RELEASING.md`
for install paths.
````

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
- Git push: current branch + tags vX.Y.Z, lib/protocols/vX.Y.Z to origin (atomic)
- npm publish: @rimsky-ai/protocols@X.Y.Z to @latest (via make publish-protocols)
- GitHub release: vX.Y.Z with releases/vX.Y.Z.md as notes
- Fast-forward main: advance main to the release commit + push to origin (skipped when releasing from main; surfaced, never forced, if main has diverged)

Questions for you:
<any flagged questions; empty if none>

Reply: go | revise <what> | abort
```

On `go` → proceed to step 7. On `revise <something>` → iterate from
the relevant step. On `abort` → revert the step-3 working-tree
mutations and exit cleanly:

- `git checkout -- lib/protocols/package.json` (the version bump).
- `git checkout -- lib/protocols/package-lock.json` if the lockfile
  exists (the refresh from step 3).

No tags or commits exist before the gate, so nothing else needs
cleanup. No Hub push.

### 7. Automated pipeline (post-gate)

1. Run the pre-build static gates BEFORE staging the commit. The
   gates that don't depend on Docker images or the test-all
   testcontainers boot — `lint` and `license-lint` — run against
   the working tree (which contains the step-3 bump but no commit
   yet) so pre-existing drift fails fast on a clean tree rather
   than stranding the release half-committed with tags on a commit
   that can't pass its own gate.
   ```
   make lint license-lint
   ```
   Failure handling:
   - **License-header drift or simple lint violations**: mechanical.
     The fix shape is "copy the canonical header from a sibling
     file in the same classification" (the `tools/license-check`
     binary names which file is wrong and how — missing header,
     wrong license, etc.). Apply the fix to the working tree
     alongside the un-committed bump artifacts and re-run the
     gate. Do NOT stage or commit yet. On clean re-run, proceed
     to sub-step 2.
   - **Anything non-mechanical** (genuine lint failures requiring
     judgment, novel license classifications, etc.): bail to the
     operator. The working tree carries the bump but no commit
     and no tags, so `git checkout -- lib/protocols/package.json`
     plus `rm releases/vX.Y.Z.md` reverts cleanly.

   `test-all` is left inside the `make release` invocation in
   sub-step 4 below rather than pre-run here: it's an order of
   magnitude more expensive than the static gates, it requires
   Docker for testcontainers, and unlike license-lint a test-all
   failure is rarely pre-existing tree drift.

2. Stage and commit the release. The commit body is intentionally
   short — a one-line subject plus a pointer to the per-tag notes
   file. The full notes ship via `gh release create --notes-file`
   in sub-step 7 below, so embedding them in the commit too
   would duplicate Markdown into `git log` where `## Section`
   headers and multi-line bullets look out of place against
   single-line subject lines from other commits.
   ```
   git add lib/protocols/package.json releases/vX.Y.Z.md
   git commit -m "release vX.Y.Z

   See releases/vX.Y.Z.md for full notes.

   Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
   ```
3. Tag locally:
   ```
   git tag vX.Y.Z
   git tag lib/protocols/vX.Y.Z
   ```
4. Invoke `make release`. The formal path does NOT set `LATEST_TAG`;
   it uses the default `latest`. The static gates `lint` and
   `license-lint` were already cleared pre-commit (sub-step 1) and
   re-run idempotently here; `test-all`, the image builds, scan,
   and push-images run for the first time. Failure handling:
   - Test-all failure → abort. The release commit is committed
     (see `git log -1`); leave for the operator to address.
     Rollback shape if desired: `git reset --soft HEAD~1 && git tag
     -d vX.Y.Z lib/protocols/vX.Y.Z`.
   - Build failure → abort with the build output. Same disposition.
   - Scan failure → enter CVE remediation (step 7a).
   - Push failure → abort. Hub state may be partial; surface clearly.
5. Push the release branch and both git tags together. `--atomic` is
   load-bearing: without it, a partial push (a later ref fails on a
   transient network glitch after an earlier ref has already landed on
   origin) leaves the remote inconsistent — an orphan tag pointing at a
   commit the pushed branch doesn't yet reach, or a branch pushed without
   its tags — that no cleanup can heal and a re-run can't auto-heal. With
   `--atomic`, either the branch and both tags all land or none do,
   keeping the pushed branch and the tags it carries consistent. The
   branch is resolved from `HEAD` rather than hard-coded, since releases
   are no longer restricted to `main`.
   ```
   branch=$(git rev-parse --abbrev-ref HEAD)
   git push --atomic origin "$branch" vX.Y.Z lib/protocols/vX.Y.Z
   ```
6. npm publish:
   ```
   make publish-protocols
   ```
   (lands on `@latest`).
7. GitHub Release (goreleaser owns creation; it uploads the CLI
   binaries + checksums and uses the curated notes as the release body).
   Run from the `vX.Y.Z` tagged commit created in sub-step 5. goreleaser
   does NOT read `gh`'s keyring token on its own — it requires
   `GITHUB_TOKEN` in the environment and fails immediately with
   "missing GITHUB_TOKEN, GITLAB_TOKEN and GITEA_TOKEN" otherwise —
   so pass it explicitly from the authenticated `gh`:
   ```
   GITHUB_TOKEN=$(gh auth token) goreleaser release --clean --release-notes=releases/vX.Y.Z.md
   ```
   The archives are `rimsky_X.Y.Z_{linux,darwin}_{amd64,arm64}.tar.gz`
   (no Windows — the CLI embeds Unix-only process control). Config lives
   in `.goreleaser.yaml`; verify it any time with `make cli-snapshot`.
8. Fast-forward `main` to the release. A formal release is the new
   stable line, so `main` should always point at the most recent
   release commit — but releases are cut from the current branch
   (often `dev`), and sub-step 5 pushes only that branch, which leaves
   `main` behind unless it is advanced explicitly. (This is exactly how
   v0.5.0 shipped on `dev` while `main` sat at v0.4.1.) After the
   release commit + tags are on origin and the publish steps have run:
   - If the release was cut from `main` (`$branch` == `main`),
     sub-step 5 already advanced `main`; skip and log "released from
     main; no fast-forward needed".
   - Otherwise advance `main` only when it is a clean fast-forward of
     the release commit. A non-fast-forwardable `main` carries commits
     the release branch does not — never force-push it; surface it for
     manual reconciliation instead.
     ```
     git fetch origin main
     if git merge-base --is-ancestor origin/main HEAD; then
       git push origin HEAD:main   # FF-only; a non-FF update is rejected
       git branch -f main HEAD     # advance the local ref too (main is not checked out)
     else
       echo "main has diverged from $branch; cannot fast-forward — reconcile manually"
     fi
     ```
   - **Expected on the direct push to `main`:** GitHub reports
     `Bypassed rule violations for refs/heads/main: Required status
     check "Rimsky-Cert sign-off" is expected.` This is normal, not a
     failure. The contributor-cert workflow
     (`.github/workflows/contributor-cert.yml`) triggers on
     `pull_request` only, so a direct release push never produces that
     check, yet `main`'s ruleset requires it — the maintainer's push
     bypasses it. Release commits intentionally carry no `Rimsky-Cert`
     trailer: the certificate is an external-contributor rights grant
     to Fall Guy, and a maintainer self-certifying their own release
     work is not its purpose. Do NOT "fix" the bypass by adding a
     `Rimsky-Cert` trailer to the release commit.

### 7a. CVE remediation (when scan fails)

1. For each failing image, identify each CVE's package, severity,
   fixed version, and whether it lives in a swappable layer (base
   image, npm/go dep) or a bundled upstream artifact (single-file
   downloaded CLI, statically-linked binary). Run
   `docker scout recommendations <img>:<version>` for the
   swappable-layer reading.
2. Classify each finding:
   - **Patch-level base bump** (e.g. `node:20-alpine3.19` →
     `node:20-alpine3.20`, or an npm-pinned tool version bump
     within the same major where the fixed transitive is known to
     ship in a specific upstream release): mechanical, apply
     automatically. Edit the relevant `FROM` line, `npm install -g`
     pin, or analogous source.
   - **Transitive in bundled upstream artifact, no override path**:
     the CVE lives inside a downloaded blob or single-file bundle
     that our `npm install` / `docker build` can't override
     (the canonical case is `@anthropic-ai/claude-code`, whose
     npm package is a 151KB wrapper that fetches a ~150MB CLI
     bundle at install time — bundled transitives like `hono` and
     `undici` are inlined into that blob and Scout sees them via
     embedded SBOM metadata). Bumping the upstream pin to a newer
     release MAY pick up the fix; verify by rebuilding the one
     image and re-scanning. If the bump doesn't help and the
     severity-vs-exposure tradeoff is acceptable for the image's
     role, surface to the operator with the full finding set and
     the recommendation to allowlist via
     `.scout-accepted-cves.txt` — see sub-step 3 below.
   - **Anything else** (major version jumps, multi-line
     recommendations, non-mechanical changes): bail to operator
     with the analysis. Don't allowlist as an escape hatch.
3. Apply the chosen remediation:
   - **Mechanical fixes only**: amend the release commit to absorb
     the source edits and move both tags onto the amended commit
     so the rerun sees a clean tree. `push-images: check-clean`
     (`Makefile`) rejects any `VERSION` ending in `-dirty`, so
     leaving the source edits uncommitted would dead-end the
     chain at the publish guard.
     ```
     git add <changed-files>...
     git commit --amend --no-edit
     git tag -f vX.Y.Z
     git tag -f lib/protocols/vX.Y.Z
     ```
     Then re-run the chain from build forward (`make core-images
     service-images scan push-images`) — skips `lint /
     license-lint / test-all` because the rerun is image-build-only
     and those gates already passed against the same Go code.
   - **Allowlist for bundled-upstream-no-override**: add one
     `<image>:<CVE-ID>  # <rationale>` line per accepted finding
     to `.scout-accepted-cves.txt` at the repo root. The header
     comment in that file governs what's legitimate — bundled
     upstream + no override path only; not pinned-by-business-
     choice. Amend the release commit to include the allowlist
     change (same shape as the mechanical-fix amend above), move
     both tags forward, then re-run from `scan` (`make scan
     push-images`). Document the accepted entries in the release
     notes' Internal section so the audit trail ships with the
     release.
   On clean rescan, push-images completes and the post-gate
   pipeline (step 7.5 onward) continues. If still failing, bail
   with new analysis.
4. If any failing image had a non-mechanical recommendation and
   no allowlist-eligible finding: bail without applying any
   change; surface the full recommendation set.

On bail: the partial state (release commit committed, tags local) is
left for the operator. They can fix forward and re-run (the skill
detects the existing commit + tags and continues), or manually roll
back with `git reset --soft HEAD~1 && git tag -d vX.Y.Z lib/protocols/vX.Y.Z`.

### 8. Final report

```
Released vX.Y.Z

Hub: 15 images at docker.io/rimskyai/{rimsky, rimsky-all-in-one, ...}:vX.Y.Z (and :latest)
Branch: <current-branch> (pushed to origin)
main: fast-forwarded to vX.Y.Z and pushed (or: released from main / NOT advanced — main diverged, reconcile manually)
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
- **Uncommitted changes in working tree.** Reject in preflight.
- **Concurrent invocations.** Out of scope. The skill assumes a single
  operator.
- **`--dry-run`.** Runs steps 1–6 with the gate as "this is what would
  happen, run for real?"; skips step 7. Before exiting, revert any
  working-tree mutations the skill made:
  - `git checkout -- lib/protocols/package.json` (the step-3 bump).
  - `git checkout -- lib/protocols/package-lock.json` if the lockfile
    exists (the step-3 refresh).
  - `rm releases/vX.Y.Z.md` (the step-4 notes draft, which is untracked
    so `git checkout` would not touch it).
  The dry-run path must leave the working tree exactly as it found it;
  an operator running a dry-run twice should not accumulate dirty
  state.
- **`--major`.** Rejected at step 0: pre-v1 — break freely, but no
  major bumps until v1 ships.
- **`--dev`.** Rejected at step 0: dev releases use `make dev-release`.
