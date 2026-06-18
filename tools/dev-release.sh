#!/usr/bin/env bash
# Copyright © 2026 Fall Guy Consulting.
# Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
# license. See LICENSE.agpl and COPYRIGHT at the repo root.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

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
        git checkout -- lib/protocols/package.json 2>/dev/null || true
        if [ -f lib/protocols/package-lock.json ]; then
            git checkout -- lib/protocols/package-lock.json 2>/dev/null || true
        fi
    fi
    exit "${rc}"
}
trap cleanup EXIT

LAST_STABLE="$(git describe --tags --match='v[0-9]*' --exclude='*-dev*' --abbrev=0 2>/dev/null || true)"
if [ -z "${LAST_STABLE}" ]; then
    echo "no stable tag found (expected something like v0.X.Y); cut a stable release first" >&2
    exit 1
fi
STABLE_NO_V="${LAST_STABLE#v}"
IFS='.' read -r MAJOR MINOR PATCH <<<"${STABLE_NO_V}"
NEXT_MINOR_BASE="v${MAJOR}.$((MINOR + 1)).0"
DATE="$(date -u +%Y%m%d)"
SHA="$(git rev-parse --short=7 HEAD)"
DEV_VERSION="${NEXT_MINOR_BASE}-dev.${DATE}.g${SHA}"
DEV_VERSION_NOPREFIX="${DEV_VERSION#v}"

echo "dev-release: deriving ${DEV_VERSION} (last stable: ${LAST_STABLE})"

git tag "${DEV_VERSION}"
TAG1_CREATED=1
git tag "lib/protocols/${DEV_VERSION}"
TAG2_CREATED=1

(cd lib/protocols && npm version --no-git-tag-version "${DEV_VERSION_NOPREFIX}")
BUMP_DONE=1

LATEST_TAG=dev VERSION="${DEV_VERSION}" make release

git push --atomic origin "${DEV_VERSION}" "lib/protocols/${DEV_VERSION}"
PUSHED=1

if npm view "@rimsky-ai/protocols@${DEV_VERSION_NOPREFIX}" version >/dev/null 2>&1; then
    echo "dev-release: @rimsky-ai/protocols@${DEV_VERSION_NOPREFIX} already published; skipping npm publish"
else
    VERSION="${DEV_VERSION}" make publish-protocols-dev
fi

git checkout -- lib/protocols/package.json
if [ -f lib/protocols/package-lock.json ]; then
    git checkout -- lib/protocols/package-lock.json
fi

if [ -z "${SKIP_GH_PRERELEASE:-}" ]; then
    if gh release view "${DEV_VERSION}" >/dev/null 2>&1; then
        echo "dev-release: GH release ${DEV_VERSION} already exists; skipping gh release create"
    else
        gh release create "${DEV_VERSION}" --prerelease --generate-notes
    fi
fi

trap - EXIT
echo "dev-release: shipped ${DEV_VERSION}"
