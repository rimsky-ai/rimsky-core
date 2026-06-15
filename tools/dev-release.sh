#!/usr/bin/env bash
# Copyright © 2026 Fall Guy Consulting.
# Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
# license. See LICENSE.agpl and COPYRIGHT at the repo root.
set -euo pipefail

# @agent-contract: Mechanical dev-channel release driver invoked by
# `make dev-release`. Derives a SemVer-2.0 pre-release version of the
# form v<next-minor>.0-dev.<YYYYMMDD>.g<sha> from the latest stable tag,
# then drives the same `make release` chain a formal release uses, with
# LATEST_TAG=dev so the floating :dev Hub tag moves (not :latest). Also
# bumps lib/protocols/package.json transiently for the npm publish, then
# reverts. Does NOT handle: preconditions (clean tree, docker/npm/gh
# login, branch) — the Make targets it invokes catch the dirty-tree case
# via VERSION override, broader preconditions are the operator's concern.
#
# @constraint: SHA folded into the SemVer pre-release identifier
# (dot-joined after the date) rather than carried in `+gSHA` SemVer build
# metadata. Build metadata after `+` is invalid in Docker image tag
# grammar ([a-zA-Z0-9_][a-zA-Z0-9_.-]*), is stripped silently by
# `npm version` (so the package.json bump would diverge from the git
# tag), and is rejected by `go get` (Go canonical-version rule). Keeping
# the SHA inside the pre-release segment preserves SemVer-2.0 ordering
# (it still sorts below the corresponding stable) without tripping any
# of those tools.
#
# @deliberate: `make release`'s host-side gates (lint, license-lint,
# test-all) run AFTER step 3's transient package.json bump. The
# dev-release flow depends on those gates being tolerant of an in-flight
# package.json version change — lint targets Go, license-lint scans
# source headers, test-all runs Go tests, none of which read
# package.json. If a future gate ever reads package.json, the bump must
# move to after the gates.

cd "$(git rev-parse --show-toplevel)"

# @deliberate: Cleanup trap with per-step gate flags. If any step after
# DEV_VERSION is derived fails, undo the transient package.json bump and
# remove any locally-created tags. The script is intended to be cron- or
# CI-driven; without this trap a partial failure leaves a dirty tree +
# local tags that block the next run. Successful completion clears the
# trap before exiting.
#
# Per-step flags gate each cleanup action so we only undo work this run
# actually performed:
#   - BUMP_DONE=1  → step-3 npm version ran; revert package.json (and lockfile).
#   - TAG1_CREATED=1 / TAG2_CREATED=1 → step-2 tag creation succeeded.
#   - PUSHED=1     → step-5 git push completed; do NOT delete the
#                    now-published tags from local refs, since they need
#                    to remain in lockstep with origin.
# Without these flags the trap could clobber an operator's unrelated
# working-tree edits (rare for a cron run, but cheap insurance) and could
# delete tags that exist for other reasons.
DEV_VERSION=""
BUMP_DONE=0
TAG1_CREATED=0
TAG2_CREATED=0
PUSHED=0
cleanup() {
    local rc=$?
    if [ "${PUSHED}" -eq 0 ]; then
        if [ "${TAG1_CREATED}" -eq 1 ] && [ -n "${DEV_VERSION}" ]; then
            git tag -d "${DEV_VERSION}" 2>/dev/null || true
        fi
        if [ "${TAG2_CREATED}" -eq 1 ] && [ -n "${DEV_VERSION}" ]; then
            git tag -d "lib/protocols/${DEV_VERSION}" 2>/dev/null || true
        fi
    fi
    if [ "${BUMP_DONE}" -eq 1 ]; then
        # @deliberate: `git checkout -- <path>` is a no-op when the
        # working-tree copy already matches HEAD. The 2>/dev/null swallows
        # "did not match any file(s) known to git" if the path is somehow
        # missing.
        git checkout -- lib/protocols/package.json 2>/dev/null || true
        if [ -f lib/protocols/package-lock.json ]; then
            git checkout -- lib/protocols/package-lock.json 2>/dev/null || true
        fi
    fi
    exit "${rc}"
}
trap cleanup EXIT

# @deliberate: --- 1. Derive DEV_VERSION ---
LAST_STABLE="$(git describe --tags --match='v[0-9]*' --exclude='*-dev*' --abbrev=0 2>/dev/null || true)"
if [ -z "${LAST_STABLE}" ]; then
    echo "no stable tag found (expected something like v0.X.Y); cut a stable release first" >&2
    exit 1
fi
# @deliberate: LAST_STABLE is vMAJOR.MINOR.PATCH; bump minor, zero patch for the dev base.
STABLE_NO_V="${LAST_STABLE#v}"
IFS='.' read -r MAJOR MINOR PATCH <<<"${STABLE_NO_V}"
NEXT_MINOR_BASE="v${MAJOR}.$((MINOR + 1)).0"
DATE="$(date -u +%Y%m%d)"
SHA="$(git rev-parse --short=7 HEAD)"
# @constraint: SHA folded into the pre-release segment (dot-joined)
# rather than carried as `+gSHA` SemVer build metadata. See header
# comment for the reasoning.
DEV_VERSION="${NEXT_MINOR_BASE}-dev.${DATE}.g${SHA}"
DEV_VERSION_NOPREFIX="${DEV_VERSION#v}"

echo "dev-release: deriving ${DEV_VERSION} (last stable: ${LAST_STABLE})"

# @deliberate: --- 2. Tag locally ---
# Idempotent against the cleanup trap from a previous failed run (which
# would have deleted any locally-created tags before exit). If the tag
# already exists here it indicates a remote-side conflict, not a
# half-finished local run — let `git tag` fail loudly so the operator can
# diagnose.
git tag "${DEV_VERSION}"
TAG1_CREATED=1
git tag "lib/protocols/${DEV_VERSION}"
TAG2_CREATED=1

# @deliberate: --- 3. Transient package.json bump ---
(cd lib/protocols && npm version --no-git-tag-version "${DEV_VERSION_NOPREFIX}")
BUMP_DONE=1

# @deliberate: --- 4. Build + scan + push with LATEST_TAG=dev ---
# VERSION override stops check-clean from seeing the dirty suffix from step 3.
LATEST_TAG=dev VERSION="${DEV_VERSION}" make release

# @constraint: --- 5. Push git tags ---
# `--atomic` is load-bearing: without it, a partial-push (e.g. the second
# tag fails on a transient network glitch after the first has already
# landed on origin) leaves an orphan tag on the remote that the cleanup
# trap can't undo and a re-run can't auto-heal. With `--atomic`, either
# both refs land or neither does. `git push` is otherwise idempotent for
# matching tags, so a clean retry just re-publishes what's already there.
git push --atomic origin "${DEV_VERSION}" "lib/protocols/${DEV_VERSION}"
PUSHED=1

# @deliberate: --- 6. npm publish with dev dist-tag ---
# Guard against re-publish of the same version (npm forbids this and
# errors out). If the version is already on the registry from a prior
# partial-success run, skip the publish step and continue.
if npm view "@rimsky-ai/protocols@${DEV_VERSION_NOPREFIX}" version >/dev/null 2>&1; then
    echo "dev-release: @rimsky-ai/protocols@${DEV_VERSION_NOPREFIX} already published; skipping npm publish"
else
    VERSION="${DEV_VERSION}" make publish-protocols-dev
fi

# @deliberate: --- 7. Revert transient package.json bump ---
# This runs on the happy path; the cleanup trap handles the failure path
# (which would also revert these files). Doing it explicitly here means
# the bump does not survive a successful run either.
git checkout -- lib/protocols/package.json
if [ -f lib/protocols/package-lock.json ]; then
    git checkout -- lib/protocols/package-lock.json
fi

# @deliberate: --- 8. (Optional) GitHub prerelease ---
# Toggleable via env var: set SKIP_GH_PRERELEASE=1 to skip this step.
# Idempotent: if a release for this tag already exists from a prior
# partial-success run, `gh release view` returns 0 and we skip.
if [ -z "${SKIP_GH_PRERELEASE:-}" ]; then
    if gh release view "${DEV_VERSION}" >/dev/null 2>&1; then
        echo "dev-release: GH release ${DEV_VERSION} already exists; skipping gh release create"
    else
        gh release create "${DEV_VERSION}" --prerelease --generate-notes
    fi
fi

# @deliberate: Clear the trap — success path doesn't want the cleanup
# to fire (and blow away the now-published tags).
trap - EXIT
echo "dev-release: shipped ${DEV_VERSION}"
