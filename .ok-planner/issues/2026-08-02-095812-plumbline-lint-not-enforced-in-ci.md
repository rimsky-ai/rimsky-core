---
issue: plumbline-lint-not-enforced-in-ci
kind: audit
category: test-coverage
artifacts:
  - decision:coding-style
status: verified
opened: 2026-08-02T09:58:12Z
---

# CI never runs the plumbline lint; its purpose-built test silently self-skips there

The coding-style decision claims "CI invokes the same lint against the full tree" (`decision:coding-style`), but no CI workflow or Makefile target ever runs the plumbline lint (the comment-hygiene and citation-resolution checker). The test built for exactly that purpose (`code:test/plumbline/clean_test.go::TestPlumblineClean`) resolves the lint binary only from `env:PLUMBLINE_BIN` or `env:CLAUDE_PLUGIN_ROOT`; CI sets neither, so the test unconditionally skips — CI stays green whether or not the tree passes the lint.

Enforcement today is edit-time only: a hook lints every file edit in interactive sessions. Anything that bypasses that hook — a direct push, an editor outside the harness — can land comment or citation violations invisibly. One fact changes the calculus, found during verification: the lint binary is already vendored and git-tracked in the repo, runs standalone with plain `node`, and exits clean against the current tree right now — so wiring it into CI is one env-var line in the workflow, not new tooling.

The ruling decides whether CI enforcement becomes real or the decision walks its claim back to edit-time-only.

## Options

- Set the binary path env var in the CI workflow so the existing test executes. Cost: a currently-skipped test starts gating PRs, and a checked-in plugin artifact becomes a CI gate (provenance and version-drift concerns are now load-bearing).
- Amend the decision to describe enforcement as edit-time only. Cost: the "maintainer verifies the codebase passes full enforcement" promise (`story:clean-lint`) stops holding for anything that bypasses the hook.

## Ruling

> Recommended ruling (/verify-issues): wire it up — set the vendored binary's path in the CI workflow so the existing test runs instead of skipping. The decision stands as written.
>
> Rationale: the decision's CI claim is the whole point of having a repo-wide style contract — edit-time-only enforcement is exactly the hole a drive-by push falls through — and the tree passes clean today, so turning the gate on costs nothing now and only ever flags real regressions. The provenance concern is real but the binary is already trusted enough to run on every local edit via the hook. Flip case: if the owner doesn't want a vendored plugin artifact acting as a PR gate (trust or drift grounds), amend the decision to edit-time-only and say so explicitly.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
